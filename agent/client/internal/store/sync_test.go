package store

import (
	"errors"
	"reflect"
	"testing"

	"github.com/jyothri/bhandaar/agent/wire"
)

// Tests for the synced marker's primitives.

const streamA, streamB = "0b7ae2b4-5b1f-4c1e-9d3a-000000000001", "0b7ae2b4-5b1f-4c1e-9d3a-000000000002"

func TestSetStreamIsCompareAndSwap(t *testing.T) {
	st := quietOpen(t, t.TempDir())
	st.UpsertDrive("d1", "/mnt/d1", "")
	if err := st.SetStream("d1", streamA, streamB); !errors.Is(err, ErrStreamChanged) {
		t.Fatalf("swap from a stream the drive doesn't have: %v", err)
	}
	if err := st.SetStream("d1", "", streamA); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceMarker("d1", streamA, []wire.Range{{0, 5}, {8, 9}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStream("d1", "", streamB); !errors.Is(err, ErrStreamChanged) {
		t.Fatalf("swap from no stream after one was set: %v", err)
	}
	st.db.Exec(`INSERT INTO sync_rejected (drive_id, row_version, kind, relative_path, reason, rejected_at) VALUES ('d1', 3, 'file', 'x', 'bad', '2026-01-01')`)

	if err := st.SetStream("d1", streamA, streamB); err != nil {
		t.Fatal(err)
	}
	d, err := st.SyncDrive("d1")
	if err != nil {
		t.Fatal(err)
	}
	if d.Marker.StreamID != streamB || d.Marker.Watermark != 0 || len(d.Marker.Ranges) != 0 || !d.Marker.SyncedAt.IsZero() {
		t.Errorf("marker after a new stream = %+v", d.Marker)
	}
	if rej, _ := st.Rejected("d1"); len(rej) != 0 {
		t.Errorf("sync_rejected kept %v", rej)
	}
}

func TestReplaceMarker(t *testing.T) {
	st := quietOpen(t, t.TempDir())
	st.UpsertDrive("d1", "/mnt/d1", "")
	st.UpsertFiles(ctx, []FileRecord{rec("d1", "a", 1, "h"), rec("d1", "b", 1, "h"), rec("d1", "c", 1, "h")}) // versions 1-3
	st.DeleteFiles(ctx, "d1", []string{"a"})                                                                  // tombstone at 4
	st.DeleteFiles(ctx, "d1", []string{"b"})                                                                  // tombstone at 5
	st.SetStream("d1", "", streamA)

	// Only the stream it was read under.
	if err := st.ReplaceMarker("d1", streamB, []wire.Range{{0, 4}}, nil); !errors.Is(err, ErrStreamChanged) {
		t.Fatalf("other stream: %v", err)
	}

	// Rejected entries are recorded; synced tombstones pruned.
	ack := &Ack{Rejected: []RejectedEntry{{FeedKey: FeedKey{Kind: "file", Path: "c", V: 3}, Reason: "too odd"}}}
	if err := st.ReplaceMarker("d1", streamA, []wire.Range{{0, 4}, {6, 9}}, ack); err != nil {
		t.Fatal(err)
	}
	d, _ := st.SyncDrive("d1")
	if d.Marker.Watermark != 4 || !reflect.DeepEqual(d.Marker.Ranges, []wire.Range{{6, 9}}) || d.Marker.SyncedAt.IsZero() {
		t.Errorf("marker = %+v", d.Marker)
	}
	if !reflect.DeepEqual(d.Marker.AckedRanges(), []wire.Range{{0, 4}, {6, 9}}) {
		t.Errorf("acked ranges = %v", d.Marker.AckedRanges())
	}
	if st.tombstone(t, "d1", "file", "a", "") != 0 || st.tombstone(t, "d1", "file", "b", "") != 5 {
		t.Error("want the tombstone at 4 pruned and the one at 5 kept")
	}
	rej, _ := st.Rejected("d1")
	if len(rej) != 1 || rej[0].Path != "c" || rej[0].V != 3 || rej[0].Reason != "too odd" {
		t.Fatalf("rejected = %+v", rej)
	}

	// A newer version of the key, stored, removes the row; an older one doesn't.
	st.ReplaceMarker("d1", streamA, []wire.Range{{0, 9}}, &Ack{Stored: []FeedKey{{Kind: "file", Path: "c", V: 2}}})
	if rej, _ := st.Rejected("d1"); len(rej) != 1 {
		t.Errorf("an older stored version removed the rejected row")
	}
	st.ReplaceMarker("d1", streamA, []wire.Range{{0, 9}}, &Ack{Stored: []FeedKey{{Kind: "file", Path: "c", V: 7}}})
	if rej, _ := st.Rejected("d1"); len(rej) != 0 {
		t.Errorf("rejected row kept after a newer version was stored: %+v", rej)
	}
	if st.tombstone(t, "d1", "file", "b", "") != 0 {
		t.Error("tombstone under the new watermark kept")
	}
}

func TestClearMarkerAndClearDrive(t *testing.T) {
	st := quietOpen(t, t.TempDir())
	st.UpsertDrive("d1", "/mnt/d1", "")
	st.SetStream("d1", "", streamA)
	st.ReplaceMarker("d1", streamA, []wire.Range{{0, 4}, {6, 9}}, nil)

	if err := st.ClearMarker("d1", streamB); !errors.Is(err, ErrStreamChanged) {
		t.Fatalf("other stream: %v", err)
	}
	if err := st.ClearMarker("d1", streamA); err != nil {
		t.Fatal(err)
	}
	d, _ := st.SyncDrive("d1")
	if d.Marker.StreamID != streamA || len(d.Marker.AckedRanges()) != 0 {
		t.Errorf("marker = %+v", d.Marker)
	}

	// A concurrent --replace-root makes the uploader's next write fail.
	st.ReplaceMarker("d1", streamA, []wire.Range{{0, 4}}, nil)
	st.ClearDrive("d1")
	if err := st.ReplaceMarker("d1", streamA, []wire.Range{{0, 8}}, nil); !errors.Is(err, ErrStreamChanged) {
		t.Fatalf("after ClearDrive: %v", err)
	}
}

func TestDriveIDsAndPendingCount(t *testing.T) {
	st := quietOpen(t, t.TempDir())
	st.UpsertDrive("b", "/mnt/b", "")
	st.UpsertDrive("a", "/mnt/a", "")
	ids, err := st.DriveIDs()
	if err != nil || !reflect.DeepEqual(ids, []string{"a", "b"}) {
		t.Fatalf("DriveIDs = %v, %v", ids, err)
	}
	st.UpsertFiles(ctx, []FileRecord{rec("a", "x", 1, "h"), rec("a", "y", 1, "h"), rec("a", "z", 1, "h")})
	st.SetStream("a", "", streamA)
	st.ReplaceMarker("a", streamA, []wire.Range{{0, 1}}, nil)
	if n, err := st.PendingCount("a"); err != nil || n != 2 {
		t.Errorf("pending = %d, %v", n, err)
	}
	if c, _ := st.Clock(); c != 3 {
		t.Errorf("clock = %d", c)
	}
}
