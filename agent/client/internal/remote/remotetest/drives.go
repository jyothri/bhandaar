package remotetest

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/jyothri/bhandaar/agent/wire"
)

// drives is the fake's drive and change-feed state: an in-memory version of
// agentserver's apply algorithm (docs/specs/remote-sync-server.md), for
// one agent.
type drives struct {
	// Drives holds each drive's state, by drive id.
	Drives map[string]*Drive
	// Batches records every change batch received, decoded, in order.
	Batches []wire.ChangeBatch

	// Reject, if set, rejects entries it returns a reason for.
	Reject func(c wire.Change) string
	// MaxChanges > 0 answers a batch with more changes with nginx's HTML 413.
	MaxChanges int
	// FailChanges are statuses to answer the next change posts with, before
	// looking at them (one each): 401 is TOKEN_EXPIRED, 404 DRIVE_NOT_OPEN,
	// 409 STREAM_MISMATCH; 413, 502 and 504 have nginx's HTML body; anything
	// else is a JSON error.
	FailChanges []int
	// DropChanges answers the next n change posts that apply with a 502
	// after committing them: a lost response.
	DropChanges int
	// AfterApply is called, with the server locked, after each applied batch.
	AfterApply func(s *Server)

	idem map[string]idemRecord
}

// Drive is one drive's state on the fake server.
type Drive struct {
	Open     wire.DriveOpenRequest
	StreamID string
	Ranges   []wire.Range
	// Rows maps each key (see Key) to its latest version; deletes stay as
	// tombstones.
	Rows map[string]Row
}

// Row is a key's latest entry on the fake server.
type Row struct {
	V       int64
	Deleted bool
	Change  wire.Change
}

type idemRecord struct {
	hash [32]byte
	resp wire.ChangesResponse
}

func newDrives() drives {
	return drives{Drives: map[string]*Drive{}, idem: map[string]idemRecord{}}
}

// Key is the fake's key for a change: kind, then the raw path and child
// bytes (or the run id).
func Key(c wire.Change) string {
	if c.Kind == wire.KindScanRun {
		return "scan_run\x00" + strconv.FormatInt(*c.RunID, 10)
	}
	name := func(p *string, b64 string) string {
		if b64 != "" {
			b, _ := base64.StdEncoding.DecodeString(b64)
			return string(b)
		}
		if p == nil {
			return ""
		}
		return *p
	}
	return c.Kind + "\x00" + name(c.Path, c.PathB64) + "\x00" + name(c.Child, c.ChildB64)
}

// Live returns the drive's live rows' versions, by key (nil if there's no
// such drive).
func (s *Server) Live(driveID string) map[string]int64 {
	s.Lock()
	defer s.Unlock()
	d := s.Drives[driveID]
	if d == nil {
		return nil
	}
	out := map[string]int64{}
	for k, r := range d.Rows {
		if !r.Deleted {
			out[k] = r.V
		}
	}
	return out
}

// DriveRanges returns the drive's acked ranges.
func (s *Server) DriveRanges(driveID string) []wire.Range {
	s.Lock()
	defer s.Unlock()
	if d := s.Drives[driveID]; d != nil {
		return append([]wire.Range{}, d.Ranges...)
	}
	return nil
}

// Snapshot copies the drive state, for Restore: a server backup.
func (s *Server) Snapshot() map[string]*Drive {
	s.Lock()
	defer s.Unlock()
	return s.SnapshotLocked()
}

// SnapshotLocked is Snapshot for a caller that holds the lock (AfterApply).
func (s *Server) SnapshotLocked() map[string]*Drive {
	out := map[string]*Drive{}
	for id, d := range s.Drives {
		c := *d
		c.Ranges = append([]wire.Range{}, d.Ranges...)
		c.Rows = make(map[string]Row, len(d.Rows))
		for k, r := range d.Rows {
			c.Rows[k] = r
		}
		out[id] = &c
	}
	return out
}

// Restore puts back a snapshot, as a database restore would; the
// idempotency records go too.
func (s *Server) Restore(snap map[string]*Drive) {
	s.Lock()
	defer s.Unlock()
	s.RestoreLocked(snap)
}

// RestoreLocked is Restore for a caller that holds the lock.
func (s *Server) RestoreLocked(snap map[string]*Drive) {
	s.idem = map[string]idemRecord{}
	// A copy, so the snapshot can be restored again.
	s.Drives = (&Server{drives: drives{Drives: snap}}).SnapshotLocked()
}

// ForgetIdempotencyKeys drops the stored responses, as housekeeping does
// after 7 days.
func (s *Server) ForgetIdempotencyKeys() {
	s.Lock()
	defer s.Unlock()
	s.idem = map[string]idemRecord{}
}

// ExpireAccessTokens makes every access token issued so far answer 401
// TOKEN_EXPIRED.
func (s *Server) ExpireAccessTokens() {
	s.Lock()
	defer s.Unlock()
	for t := range s.access {
		s.access[t] = false
	}
}

// serveDrives handles the drive endpoints; false if r isn't one.
func (s *Server) serveDrives(w http.ResponseWriter, r *http.Request, body []byte) bool {
	rest, ok := strings.CutPrefix(r.URL.Path, "/agent/v1/drives")
	if !ok {
		return false
	}
	var id string
	var changes bool
	switch {
	case rest == "" && r.Method == http.MethodGet:
	case strings.HasSuffix(rest, "/changes") && r.Method == http.MethodPost:
		id, changes = strings.TrimSuffix(strings.TrimPrefix(rest, "/"), "/changes"), true
	case strings.HasPrefix(rest, "/") && r.Method == http.MethodPut:
		id = rest[1:]
	default:
		return false
	}
	if changes && len(s.FailChanges) > 0 {
		status := s.FailChanges[0]
		s.FailChanges = s.FailChanges[1:]
		failWith(w, status)
		return true
	}
	if !s.authorized(w, r) {
		return true
	}
	switch {
	case changes:
		s.postChanges(w, r, id, body)
	case id == "":
		out := []wire.Drive{}
		ids := make([]string, 0, len(s.Drives))
		for id := range s.Drives {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			d := s.Drives[id]
			out = append(out, wire.Drive{DriveID: id, StreamID: d.StreamID, AckedRanges: ranges(d.Ranges),
				DriveRoot: d.Open.DriveRoot, BackupRoot: d.Open.BackupRoot, PhysicalDrive: physical(d)})
		}
		writeJSON(w, 200, out)
	default:
		var req wire.DriveOpenRequest
		if err := json.Unmarshal(body, &req); err != nil || req.StreamID == "" || req.DriveRoot == "" {
			writeError(w, 400, wire.CodeInvalidRequest)
			return true
		}
		d := s.Drives[id]
		reset := false
		if d == nil {
			d = &Drive{StreamID: req.StreamID, Rows: map[string]Row{}}
			s.Drives[id] = d
		} else if d.StreamID != req.StreamID {
			d.StreamID, d.Ranges, d.Rows, reset = req.StreamID, nil, map[string]Row{}, true
		}
		d.Open = req
		writeJSON(w, 200, wire.DriveOpenResponse{DriveID: id, StreamID: d.StreamID, AckedRanges: ranges(d.Ranges),
			Reset: reset, PhysicalDrive: physical(d)})
	}
	return true
}

func ranges(r []wire.Range) []wire.Range {
	return append([]wire.Range{}, r...)
}

// physical links a drive with a filesystem ID to a made-up copy on a Mac.
func physical(d *Drive) *wire.PhysicalDrive {
	if d.Open.Identity == nil || d.Open.Identity.FSUUID == "" {
		return nil
	}
	return &wire.PhysicalDrive{ID: 7, Linked: []wire.LinkedDrive{{Hostname: "macbook", DriveID: "mac-" + d.Open.Identity.FSUUID}}}
}

func (s *Server) authorized(w http.ResponseWriter, r *http.Request) bool {
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	live, ok := s.access[tok]
	switch {
	case !ok:
		writeError(w, 401, wire.CodeInvalidToken)
	case !live:
		writeError(w, 401, wire.CodeTokenExpired)
	}
	return ok && live
}

func failWith(w http.ResponseWriter, status int) {
	switch status {
	case 401:
		writeError(w, 401, wire.CodeTokenExpired)
	case 404:
		writeError(w, 404, wire.CodeDriveNotOpen)
	case 409:
		writeError(w, 409, wire.CodeStreamMismatch)
	case 413, 502, 504:
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(status)
		io.WriteString(w, "<html><body><center><h1>"+http.StatusText(status)+"</h1></center><hr><center>nginx</center></body></html>")
	default:
		writeError(w, status, "FAKE_"+strconv.Itoa(status))
	}
}

// postChanges follows the server's apply algorithm.
func (s *Server) postChanges(w http.ResponseWriter, r *http.Request, id string, body []byte) {
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			writeError(w, 400, wire.CodeInvalidRequest)
			return
		}
		if body, err = io.ReadAll(zr); err != nil {
			writeError(w, 400, wire.CodeInvalidRequest)
			return
		}
	}
	var b wire.ChangeBatch
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil || !validEnvelope(b) {
		writeError(w, 400, wire.CodeInvalidBatch)
		return
	}
	if s.MaxChanges > 0 && len(b.Changes) > s.MaxChanges {
		failWith(w, 413)
		return
	}
	s.Batches = append(s.Batches, b)
	d := s.Drives[id]
	switch {
	case d == nil:
		writeError(w, 404, wire.CodeDriveNotOpen)
		return
	case d.StreamID != b.StreamID:
		writeError(w, 409, wire.CodeStreamMismatch)
		return
	}
	for _, rg := range d.Ranges {
		if rg[0] <= b.FromVersion && b.ToVersion <= rg[1] {
			writeJSON(w, 200, wire.ChangesResponse{AckedRanges: ranges(d.Ranges), Skipped: len(b.Changes), Duplicate: true, Rejected: []wire.Rejected{}})
			return
		}
	}
	key := r.Header.Get(wire.HeaderIdempotencyKey)
	sum := sha256.Sum256(body)
	if rec, ok := s.idem[key]; ok {
		if rec.hash != sum {
			writeError(w, 422, wire.CodeIdempotencyKeyReused)
			return
		}
		writeJSON(w, 200, rec.resp)
		return
	}

	resp := wire.ChangesResponse{Rejected: []wire.Rejected{}}
	for _, c := range b.Changes {
		if inRanges(d.Ranges, c.V) {
			resp.Skipped++
			continue
		}
		if s.Reject != nil {
			if why := s.Reject(c); why != "" {
				resp.Rejected = append(resp.Rejected, wire.Rejected{V: c.V, Reason: why})
				if c.Kind == wire.KindScanRun {
					continue
				}
				c.Op = wire.OpDelete // the server keeps the key as missing, not stale
			}
		}
		k := Key(c)
		if cur, ok := d.Rows[k]; ok && cur.V >= c.V {
			resp.Skipped++
			continue
		}
		d.Rows[k] = Row{V: c.V, Deleted: c.Op == wire.OpDelete, Change: c}
		resp.Applied++
	}
	d.Ranges = merge(d.Ranges, wire.Range{b.FromVersion, b.ToVersion})
	resp.AckedRanges = ranges(d.Ranges)
	s.idem[key] = idemRecord{hash: sum, resp: resp}
	if s.AfterApply != nil {
		s.AfterApply(s)
	}
	if s.DropChanges > 0 {
		s.DropChanges--
		failWith(w, 502)
		return
	}
	writeJSON(w, 200, resp)
}

func validEnvelope(b wire.ChangeBatch) bool {
	if b.StreamID == "" || b.FromVersion < 0 || b.FromVersion >= b.ToVersion {
		return false
	}
	prev := b.FromVersion
	for _, c := range b.Changes {
		if c.V <= prev || c.V > b.ToVersion {
			return false
		}
		prev = c.V
	}
	return true
}

func inRanges(rs []wire.Range, v int64) bool {
	for _, r := range rs {
		if r[0] < v && v <= r[1] {
			return true
		}
	}
	return false
}

func merge(rs []wire.Range, add wire.Range) []wire.Range {
	all := append(append([]wire.Range{}, rs...), add)
	sort.Slice(all, func(i, j int) bool { return all[i][0] < all[j][0] })
	var out []wire.Range
	for _, r := range all {
		if n := len(out); n > 0 && r[0] <= out[n-1][1] {
			out[n-1][1] = max(out[n-1][1], r[1])
			continue
		}
		out = append(out, r)
	}
	return out
}
