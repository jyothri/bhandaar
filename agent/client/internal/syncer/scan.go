package syncer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/store"
	"github.com/jyothri/bhandaar/agent/wire"
)

// Uploading during a scan (docs/specs/remote-sync-agent.md, "Upload during
// scan"): the scan's session covers (from, ∞), where from is at or above S,
// the clock read before the scan wrote anything. It never reads entries at
// or below from, so the drive's history is left to sync.

// ScanFrom works out where a scan's session starts, given S. Let e be the
// end of the highest acked range at or below S (0 if none): the session
// continues that range (from = e) if the drive has no entry in (e, S],
// which is uncovered, so anything there is pending history; otherwise it
// starts a new range above that history (from = S).
func (u *Uploader) ScanFrom(ctx context.Context, d store.SyncDrive, S int64) (int64, error) {
	var e int64
	for _, r := range d.Marker.AckedRanges() {
		if r[1] <= S {
			e = max(e, r[1])
		}
	}
	if e >= S {
		return S, nil
	}
	pending, err := u.Feed.Any(ctx, d.DriveID, wire.Range{e, S})
	if err != nil || pending {
		return S, err
	}
	return e, nil
}

// tick is how often a scan's session looks for new entries, besides the
// wakeups after each of the scan's flushes.
var tick = 2 * time.Second

// Stream is a scan's upload session: it uploads the drive's entries above
// from as the scan writes them, woken by wake (after each of the scan's
// flushes) and a 2 s tick. Once done is closed (the scan has returned, so
// everything it wrote is committed), it uploads what's left and returns
// nil. A 404 DRIVE_NOT_OPEN or 409 STREAM_MISMATCH re-opens the drive, and
// the session carries on from its cursor if the server still has what it
// sent, else from the same from. The caller holds the drive's upload lock,
// and has opened the drive with Open.
func (u *Uploader) Stream(ctx context.Context, d store.SyncDrive, from int64, wake, done <-chan struct{}, res *Result) error {
	ss := u.newSession(d, math.MaxInt64, false)
	cursor := from
	u.cursor.Store(cursor)
	finishing := false
	reopens := 0
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		to, sent, err := u.sendNext(ctx, ss, cursor, res)
		switch {
		case errors.Is(err, errReopen):
			if reopens++; reopens > maxReopens {
				return fmt.Errorf("drive %q: re-opened %d times in one run; giving up", d.DriveID, maxReopens)
			}
			nd, err := u.Open(ctx, d.DriveID, res)
			if err != nil {
				return err
			}
			if !covers(nd.Marker.AckedRanges(), wire.Range{from, cursor}) {
				// The server started the drive over (a reset, or a new
				// stream): this session re-sends what it had sent.
				cursor = from
				u.cursor.Store(cursor)
			}
			ss = u.newSession(nd, math.MaxInt64, false)
			continue
		case err != nil:
			return err
		case sent:
			cursor = to
			continue
		case finishing:
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		case <-t.C:
		case <-done:
			finishing = true // one more pass sees everything the scan wrote
		}
	}
}

// DrivesWithHistory counts the drives in state.db with pending entries up
// to head, for scan's startup hint. It checks each gap with one EXISTS on
// the feed indexes, never counting.
func (u *Uploader) DrivesWithHistory(ctx context.Context, head int64) (int, error) {
	ids, err := u.Store.DriveIDs()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		d, err := u.Store.SyncDrive(id)
		if err != nil {
			return 0, err
		}
		for _, g := range gaps(d.Marker.AckedRanges(), head) {
			pending, err := u.Feed.Any(ctx, id, g)
			if err != nil {
				return 0, err
			}
			if pending {
				n++
				break
			}
		}
	}
	return n, nil
}
