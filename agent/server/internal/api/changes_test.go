package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/jyothri/bhandaar/agent/wire"
)

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(b)
	zw.Close()
	return buf.Bytes()
}

// postRaw sends a raw body to the changes endpoint.
func (c *client) postRaw(drive string, body []byte, gzipped bool, key string) *httptest.ResponseRecorder {
	c.t.Helper()
	extra := []string{}
	if gzipped {
		extra = append(extra, "Content-Encoding", "gzip")
	}
	if key != "" {
		extra = append(extra, wire.HeaderIdempotencyKey, key)
	}
	return c.do("POST", "/agent/v1/drives/"+drive+"/changes", string(body), extra...)
}

func goldenBatch(t *testing.T) (wire.ChangeBatch, []byte) {
	b, err := os.ReadFile("../../../wire/testdata/changes-batch.json")
	if err != nil {
		t.Fatal(err)
	}
	var batch wire.ChangeBatch
	if err := json.Unmarshal(b, &batch); err != nil {
		t.Fatal(err)
	}
	return batch, b
}

func TestChangesFlow(t *testing.T) {
	srv, _ := dbServer(t)
	cl := loggedIn(t, srv)
	batch, raw := goldenBatch(t)

	// Before PUT: 404.
	wantStatus(t, cl.postRaw("seagate2", gz(t, raw), true, "k1"), 404, wire.CodeDriveNotOpen)

	wantStatus(t, cl.do("PUT", "/agent/v1/drives/seagate2", wire.DriveOpenRequest{StreamID: batch.StreamID, DriveRoot: "/mnt/seagate2"}), 200, "")
	rec := cl.postRaw("seagate2", gz(t, raw), true, "k1")
	wantStatus(t, rec, 200, "")
	resp := decode[wire.ChangesResponse](t, rec)
	if resp.Applied != 8 || resp.Duplicate || len(resp.AckedRanges) != 1 || !strings.Contains(rec.Body.String(), `"rejected":[]`) {
		t.Fatalf("response = %s", rec.Body)
	}

	// A lost response and a retry: a duplicate.
	resp = decode[wire.ChangesResponse](t, cl.postRaw("seagate2", gz(t, raw), true, "k1"))
	if !resp.Duplicate {
		t.Errorf("replay = %+v", resp)
	}

	// Plain JSON works too.
	next, _ := json.Marshal(wire.ChangeBatch{StreamID: batch.StreamID, FromVersion: 19233, ToVersion: 19300, Changes: []wire.Change{}})
	wantStatus(t, cl.postRaw("seagate2", next, false, "k2"), 200, "")

	// The listing shows the acked range.
	list := decode[[]wire.Drive](t, cl.do("GET", "/agent/v1/drives", nil))
	if len(list) != 1 || len(list[0].AckedRanges) != 1 || list[0].AckedRanges[0] != (wire.Range{18234, 19300}) || list[0].LastSyncedAt == nil {
		t.Errorf("drives = %+v", list)
	}

	// Another stream: 409 with the current stream id.
	other, _ := json.Marshal(wire.ChangeBatch{StreamID: uuid.NewString(), FromVersion: 0, ToVersion: 1, Changes: []wire.Change{}})
	rec = cl.postRaw("seagate2", other, false, "k3")
	wantStatus(t, rec, 409, wire.CodeStreamMismatch)
	if !strings.Contains(rec.Body.String(), batch.StreamID) {
		t.Errorf("409 without the stream id: %s", rec.Body)
	}
}

func TestChangesRequestErrors(t *testing.T) {
	srv, _ := dbServer(t)
	cl := loggedIn(t, srv)
	stream := uuid.NewString()
	wantStatus(t, cl.do("PUT", "/agent/v1/drives/d", wire.DriveOpenRequest{StreamID: stream, DriveRoot: "/d"}), 200, "")
	enc := func(b wire.ChangeBatch) []byte { j, _ := json.Marshal(b); return j }
	up := func(v int64) wire.Change {
		return wire.Change{V: v, Kind: wire.KindFile, Op: wire.OpDelete, Path: wire.Ptr("x")}
	}

	// No Idempotency-Key.
	wantStatus(t, cl.postRaw("d", enc(wire.ChangeBatch{StreamID: stream, FromVersion: 0, ToVersion: 1}), false, ""), 400, wire.CodeInvalidRequest)

	for name, b := range map[string]wire.ChangeBatch{
		"from == to":    {StreamID: stream, FromVersion: 5, ToVersion: 5},
		"from > to":     {StreamID: stream, FromVersion: 6, ToVersion: 5},
		"no stream":     {FromVersion: 0, ToVersion: 5},
		"unsorted":      {StreamID: stream, FromVersion: 0, ToVersion: 10, Changes: []wire.Change{up(5), up(3)}},
		"repeated v":    {StreamID: stream, FromVersion: 0, ToVersion: 10, Changes: []wire.Change{up(5), up(5)}},
		"v at from":     {StreamID: stream, FromVersion: 5, ToVersion: 10, Changes: []wire.Change{up(5)}},
		"v above to":    {StreamID: stream, FromVersion: 0, ToVersion: 10, Changes: []wire.Change{up(11)}},
		"negative from": {StreamID: stream, FromVersion: -1, ToVersion: 10},
	} {
		if rec := cl.postRaw("d", enc(b), false, "k-"+name); rec.Code != 400 || errCode(t, rec) != wire.CodeInvalidBatch {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	wantStatus(t, cl.postRaw("d", []byte(`{"stream_id":"`+stream+`","from_version":0,"to_version":1,"changes":[],"extra":1}`), false, "k-u"),
		400, wire.CodeInvalidBatch)
	wantStatus(t, cl.postRaw("d", []byte("not gzip"), true, "k-g"), 400, wire.CodeInvalidRequest)
}

func TestChangesSizeLimits(t *testing.T) {
	srv, _ := dbServer(t)
	cl := loggedIn(t, srv)
	stream := uuid.NewString()
	wantStatus(t, cl.do("PUT", "/agent/v1/drives/d", wire.DriveOpenRequest{StreamID: stream, DriveRoot: "/d"}), 200, "")

	// A gzip bomb: 20 MiB of zeros compresses to ~20 KiB.
	bomb := gz(t, make([]byte, 20<<20))
	if len(bomb) > MaxChangesBody {
		t.Fatalf("bomb is %d bytes compressed", len(bomb))
	}
	wantStatus(t, cl.postRaw("d", bomb, true, "k1"), 413, wire.CodePayloadTooLarge)

	// Over 2 MiB on the wire.
	wantStatus(t, cl.postRaw("d", bytes.Repeat([]byte(" "), MaxChangesBody+1), false, "k2"), 413, wire.CodePayloadTooLarge)

	// More changes than a batch may have: 413, so the agent halves.
	b := wire.ChangeBatch{StreamID: stream, FromVersion: 0, ToVersion: 2000}
	for v := int64(1); v <= MaxChangesPerBatch+1; v++ {
		b.Changes = append(b.Changes, wire.Change{V: v, Kind: wire.KindFile, Op: wire.OpDelete, Path: wire.Ptr("x")})
	}
	j, _ := json.Marshal(b)
	wantStatus(t, cl.postRaw("d", gz(t, j), true, "k3"), 413, wire.CodePayloadTooLarge)
}
