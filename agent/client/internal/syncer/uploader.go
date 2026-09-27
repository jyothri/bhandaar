package syncer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/jyothri/bhandaar/agent/client/internal/remote"
	"github.com/jyothri/bhandaar/agent/client/internal/store"
	"github.com/jyothri/bhandaar/agent/wire"
)

// Tokens hands out access tokens (creds.Session).
type Tokens interface {
	AccessToken(ctx context.Context) (string, error)
	Renew(ctx context.Context, stale string) (string, error)
}

// Backoff between retries of a failing request: 1 s, doubling, capped.
const (
	firstBackoff = time.Second
	maxBackoff   = 30 * time.Second
)

// DefaultRemoteTimeout is how long the remote may fail before the upload
// gives up (--remote-timeout).
const DefaultRemoteTimeout = 2 * time.Minute

// Uploader uploads drives' change feeds, one drive at a time. The caller
// holds each drive's upload lock (see Lock) while uploading it, and runs
// the preflight (health, handshake) first; Limits come from the handshake.
type Uploader struct {
	Store   *store.Store
	Feed    *Feed
	Client  *remote.Client
	Tokens  Tokens
	AgentID string
	Limits  wire.Limits
	// RemoteTimeout: a request is retried until this long has passed with
	// no successful request; 0 means DefaultRemoteTimeout.
	RemoteTimeout time.Duration
	// Log receives notes (retries, a new stream); nil discards them.
	Log io.Writer
	// Progress, if set, is called after each acknowledged batch with the
	// drive's total so far.
	Progress func(driveID string, uploaded int)

	// For tests.
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error

	// failingSince is when the current run of failed requests began (zero
	// while requests succeed): the give-up budget counts from there, so
	// idle time between requests (a scan with nothing new) never counts.
	failingSince time.Time
	// For Status, read while a scan uploads.
	uploaded, cursor, retryUntil atomic.Int64
}

// Status is a running upload's progress, for a progress line.
type Status struct {
	// Uploaded counts the entries the server has applied this run.
	Uploaded int64
	// Cursor is the version the current session has uploaded up to.
	Cursor int64
	// RetryLeft is how long the uploader will keep retrying a failing
	// remote before giving up; 0 while requests succeed.
	RetryLeft time.Duration
}

// Status returns the upload's progress; safe to call from another
// goroutine.
func (u *Uploader) Status() Status {
	st := Status{Uploaded: u.uploaded.Load(), Cursor: u.cursor.Load()}
	if until := u.retryUntil.Load(); until != 0 {
		st.RetryLeft = max(0, time.Unix(0, until).Sub(u.timeNow()))
	}
	return st
}

// Result is what uploading one drive did.
type Result struct {
	DriveID string
	// Uploaded counts the entries in batches the server applied (not
	// duplicates); Rejected, those it rejected.
	Uploaded, Rejected int
	// Pending counts the drive's entries still not on the server when the
	// upload ended; -1 if it couldn't be counted.
	Pending int64
	// NewStream, if not empty, says why the drive was started over.
	NewStream string
	// Drive is the server's last answer to opening the drive.
	Drive wire.DriveOpenResponse
}

// errReopen ends an upload session that needs the drive re-opened and
// reconciled: 404 DRIVE_NOT_OPEN, 409 STREAM_MISMATCH, the server losing
// coverage mid-session, or the local stream changing.
var errReopen = errors.New("the drive needs re-opening")

// maxReopens bounds re-opening one drive in one run.
const maxReopens = 3

func (u *Uploader) log() io.Writer {
	if u.Log == nil {
		return io.Discard
	}
	return u.Log
}

func (u *Uploader) timeNow() time.Time {
	if u.now != nil {
		return u.now()
	}
	return time.Now()
}

func (u *Uploader) wait(ctx context.Context, d time.Duration) error {
	if u.sleep != nil {
		return u.sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (u *Uploader) remoteTimeout() time.Duration {
	if u.RemoteTimeout > 0 {
		return u.RemoteTimeout
	}
	return DefaultRemoteTimeout
}

func (u *Uploader) maxChanges() int {
	if n := u.Limits.MaxChangesPerBatch; n > 0 && n < DefaultMaxChanges {
		return n
	}
	return DefaultMaxChanges
}

func (u *Uploader) maxBytes() int {
	if n := u.Limits.MaxBatchBytes; n > 0 && n < DefaultMaxBytes {
		return n
	}
	return DefaultMaxBytes
}

// call runs one request, f, with an access token, until it succeeds or
// fails for good. A transient failure is retried with exponential backoff
// and jitter (honouring Retry-After) until RemoteTimeout has passed with no
// successful request; a 401 TOKEN_EXPIRED or INVALID_TOKEN refreshes the
// token once.
func (u *Uploader) call(ctx context.Context, f func(token string) error) error {
	var token string
	renewed := false
	backoff := firstBackoff
	for {
		var err error
		if token == "" {
			token, err = u.Tokens.AccessToken(ctx)
		}
		if err == nil {
			err = f(token)
		}
		if err == nil {
			u.failingSince = time.Time{}
			u.retryUntil.Store(0)
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		if remote.Refreshable(err) {
			if renewed {
				return fmt.Errorf(`the server refused a refreshed token; run "driveagent login": %w`, err)
			}
			fresh, rerr := u.Tokens.Renew(ctx, token)
			if rerr == nil {
				token, renewed = fresh, true
				continue
			}
			err = rerr
		}
		if !errors.Is(err, remote.ErrTransient) {
			return err
		}
		if u.failingSince.IsZero() {
			u.failingSince = u.timeNow()
			u.retryUntil.Store(u.failingSince.Add(u.remoteTimeout()).UnixNano())
		}
		left := u.remoteTimeout() - u.timeNow().Sub(u.failingSince)
		if left <= 0 {
			return fmt.Errorf("remote unavailable for %s: %w", u.remoteTimeout(), err)
		}
		d := backoff*3/4 + rand.N(backoff/2+1) // ±25%
		var re *remote.Error
		if errors.As(err, &re) && re.RetryAfter > d {
			d = re.RetryAfter
		}
		d = min(d, left)
		fmt.Fprintf(u.log(), "remote: retrying in %s (%s left): %v\n", d.Round(100*time.Millisecond), left.Round(time.Second), err)
		if err := u.wait(ctx, d); err != nil {
			return err
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// statusIs reports whether err is a response with this status (and code,
// if code isn't empty).
func statusIs(err error, status int, code string) bool {
	var re *remote.Error
	return errors.As(err, &re) && re.Status == status && (code == "" || re.Code == code)
}

func permanent(err error) error {
	return &remote.Error{Kind: remote.ErrPermanent, Message: err.Error()}
}

func identityOf(id store.DriveIdentity) *wire.Identity {
	if id.FSUUID == "" && id.FSType == "" && id.FSUUIDSource == "" && id.HWSerial == "" {
		return nil
	}
	return &wire.Identity{FSUUID: id.FSUUID, FSType: id.FSType, FSUUIDSource: id.FSUUIDSource, HWSerial: id.HWSerial}
}

// Open opens the drive on the server (PUT /drives/{id}) and reconciles the
// local marker with the server's ranges (the agent spec's "Reconciling"):
// adopting ranges the server acknowledged after the last local update,
// clearing the marker when the server started the stream over, and
// starting a new stream when either side went back in time. A drive not
// uploaded before gets its first stream id here. It returns the drive as it
// stands afterwards.
func (u *Uploader) Open(ctx context.Context, driveID string, res *Result) (store.SyncDrive, error) {
	for attempt := 0; attempt <= maxReopens; attempt++ {
		d, err := u.Store.SyncDrive(driveID)
		if err != nil {
			return d, err
		}
		stream := d.Marker.StreamID
		if stream == "" {
			if err := u.Store.SetStream(driveID, "", uuid.NewString()); err != nil && !errors.Is(err, store.ErrStreamChanged) {
				return d, err
			}
			continue
		}
		clock, err := u.Store.Clock()
		if err != nil {
			return d, err
		}
		req := wire.DriveOpenRequest{StreamID: stream, DriveRoot: d.DriveRoot, BackupRoot: d.BackupRoot, Identity: identityOf(d.Identity)}
		var resp wire.DriveOpenResponse
		if err := u.call(ctx, func(token string) (err error) {
			resp, err = u.Client.OpenDrive(ctx, token, driveID, req)
			return err
		}); err != nil {
			return d, err
		}
		if err := checkRanges(resp.AckedRanges); err != nil {
			return d, permanent(err)
		}
		res.Drive = resp

		act, why := reconcile(d.Marker, clock, resp)
		switch act {
		case keepMarker:
			return d, nil
		case adoptServer:
			err = u.Store.ReplaceMarker(driveID, stream, resp.AckedRanges, nil)
		case clearMarker:
			err = u.Store.ClearMarker(driveID, stream)
		case mintStream:
			fmt.Fprintf(u.log(), "%s: starting over with a new stream, re-uploading the drive: %s\n", driveID, why)
			res.NewStream = why
			if err = u.Store.SetStream(driveID, stream, uuid.NewString()); err == nil {
				continue // PUT the new stream; the server starts the drive over
			}
		}
		if errors.Is(err, store.ErrStreamChanged) {
			continue
		}
		if err != nil {
			return d, err
		}
		return u.Store.SyncDrive(driveID)
	}
	return store.SyncDrive{}, fmt.Errorf("drive %q: its stream kept changing while opening it", driveID)
}

// SyncDrive uploads the drive's pending history (driveagent sync): it opens
// and reconciles the drive, then uploads each gap in its marker up to the
// clock as it stands after opening (or re-opening), oldest first, one
// session per gap. A
// gap with no pending entries is closed with an empty batch, so the
// server's ranges merge. The caller holds the drive's upload lock.
func (u *Uploader) SyncDrive(ctx context.Context, driveID string) (Result, error) {
	res := Result{DriveID: driveID, Pending: -1}
	err := u.syncDrive(ctx, driveID, &res)
	if n, perr := u.pending(context.WithoutCancel(ctx), driveID); perr == nil {
		res.Pending = n
	}
	return res, err
}

func (u *Uploader) syncDrive(ctx context.Context, driveID string, res *Result) error {
	d, err := u.Open(ctx, driveID, res)
	if err != nil {
		return err
	}
	head, err := u.Store.Clock()
	if err != nil {
		return err
	}
	reopens := 0
	var last *wire.Range
	for {
		gap, ok := firstGap(d.Marker.AckedRanges(), head)
		if !ok {
			return nil
		}
		if last != nil && gap == *last {
			return fmt.Errorf("drive %q: uploading %v didn't close the gap", driveID, gap)
		}
		err := u.upload(ctx, d, gap[0], gap[1], true, res)
		if errors.Is(err, errReopen) {
			if reopens++; reopens > maxReopens {
				return fmt.Errorf("drive %q: re-opened %d times in one run; giving up", driveID, maxReopens)
			}
			if d, err = u.Open(ctx, driveID, res); err != nil {
				return err
			}
			// After a new stream everything is history again, up to now.
			if head, err = u.Store.Clock(); err != nil {
				return err
			}
			last = nil
			continue
		}
		if err != nil {
			return err
		}
		last = &gap
		if d, err = u.Store.SyncDrive(driveID); err != nil {
			return err
		}
	}
}

// pending counts the drive's entries the marker doesn't cover, up to the
// clock now.
func (u *Uploader) pending(ctx context.Context, driveID string) (int64, error) {
	d, err := u.Store.SyncDrive(driveID)
	if err != nil {
		return 0, err
	}
	head, err := u.Store.Clock()
	if err != nil {
		return 0, err
	}
	return u.Feed.Count(ctx, driveID, gaps(d.Marker.AckedRanges(), head))
}

// upload is one upload session: the drive's entries in (from, upper], in
// batches, each recorded in the marker once the server acknowledges it.
// With closeGap, the last batch's to_version is upper, even with no entry
// there.
func (u *Uploader) upload(ctx context.Context, d store.SyncDrive, from, upper int64, closeGap bool, res *Result) error {
	ss := u.newSession(d, upper, closeGap)
	for cursor := from; cursor < upper; {
		to, sent, err := u.sendNext(ctx, ss, cursor, res)
		if err != nil || !sent {
			return err
		}
		cursor = to
	}
	return nil
}

// sessionState is an upload session's state between batches.
type sessionState struct {
	session
	marker   []wire.Range // the server's ranges as last acknowledged
	pageSize int
}

func (u *Uploader) newSession(d store.SyncDrive, upper int64, closeGap bool) *sessionState {
	return &sessionState{
		session:  session{agentID: u.AgentID, driveID: d.DriveID, streamID: d.Marker.StreamID, upper: upper, closeGap: closeGap},
		marker:   d.Marker.AckedRanges(),
		pageSize: u.maxChanges(),
	}
}

// sendNext sends the next batch after cursor and records its
// acknowledgement. sent is false when there was nothing to send; to is the
// new cursor.
func (u *Uploader) sendNext(ctx context.Context, ss *sessionState, cursor int64, res *Result) (to int64, sent bool, err error) {
	for {
		page, more, err := u.Feed.Page(ctx, ss.driveID, cursor, ss.upper, ss.pageSize)
		if err != nil {
			return cursor, false, err
		}
		b, err := ss.build(cursor, page, more, u.maxBytes())
		if err != nil || b == nil {
			return cursor, false, err
		}

		var resp wire.ChangesResponse
		err = u.call(ctx, func(token string) (err error) {
			resp, err = u.Client.PostChanges(ctx, token, ss.driveID, b.key, b.body)
			return err
		})
		switch {
		case statusIs(err, http.StatusRequestEntityTooLarge, ""):
			if len(b.entries) <= 1 {
				return cursor, false, fmt.Errorf("the server refuses even a batch of one entry as too large: %w", err)
			}
			ss.pageSize = max(1, len(b.entries)/2)
			fmt.Fprintf(u.log(), "%s: batch too large for the server; sending %d changes per batch\n", ss.driveID, ss.pageSize)
			continue
		case statusIs(err, http.StatusNotFound, wire.CodeDriveNotOpen), statusIs(err, http.StatusConflict, wire.CodeStreamMismatch):
			return cursor, false, errReopen
		case err != nil:
			return cursor, false, err
		}
		if err := checkRanges(resp.AckedRanges); err != nil {
			return cursor, false, permanent(err)
		}

		// The answer must still cover everything the server acknowledged
		// before, and this batch. If it doesn't, the server went back in
		// time mid-session: copying its ranges would erase the evidence, so
		// the marker stays, and re-opening mints a new stream.
		if !covers(resp.AckedRanges, append(ss.marker, wire.Range{b.from, b.to})...) {
			fmt.Fprintf(u.log(), "%s: the server's acked ranges %v no longer cover %v and (%d, %d]; re-opening the drive\n",
				ss.driveID, resp.AckedRanges, ss.marker, b.from, b.to)
			return cursor, false, errReopen
		}
		var ack *store.Ack
		if !resp.Duplicate {
			ack = ackFor(b, resp, u.timeNow())
		}
		if err := u.Store.ReplaceMarker(ss.driveID, ss.streamID, resp.AckedRanges, ack); err != nil {
			if errors.Is(err, store.ErrStreamChanged) {
				return cursor, false, errReopen
			}
			return cursor, false, err
		}
		ss.marker = resp.AckedRanges
		if !resp.Duplicate {
			res.Uploaded += len(b.entries)
			res.Rejected += len(ack.Rejected)
			u.uploaded.Add(int64(len(b.entries)))
		}
		u.cursor.Store(b.to)
		if u.Progress != nil {
			u.Progress(ss.driveID, res.Uploaded)
		}
		return b.to, true, nil
	}
}

// ackFor sorts a batch's entries into rejected and stored.
func ackFor(b *batch, resp wire.ChangesResponse, now time.Time) *store.Ack {
	why := make(map[int64]string, len(resp.Rejected))
	for _, r := range resp.Rejected {
		why[r.V] = r.Reason
	}
	ack := &store.Ack{}
	for _, e := range b.entries {
		if reason, ok := why[e.Change.V]; ok {
			ack.Rejected = append(ack.Rejected, store.RejectedEntry{FeedKey: e.Key, Reason: reason, At: now})
		} else {
			ack.Stored = append(ack.Stored, e.Key)
		}
	}
	return ack
}
