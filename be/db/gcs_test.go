package db

import (
	"encoding/json"
	"sort"
	"strconv"
	"testing"
	"time"
)

// gcsRecordOf lists the record's objects of a bucket, as "name#gen/state size".
func gcsRecordOf(t *testing.T, clientKey, bucket string) []string {
	t.Helper()
	var rows []struct {
		Name       string `db:"name"`
		Generation int64  `db:"generation"`
		State      string `db:"state"`
		Size       int64  `db:"size"`
		Parent     string `db:"parent"`
	}
	if err := db.Select(&rows, `SELECT name, generation, state, size, parent FROM gcs_objects
		WHERE client_key = $1 AND bucket = $2`, clientKey, bucket); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		out = append(out, r.Name+"#"+fmtInt(r.Generation)+"/"+r.State+" "+fmtInt(r.Size)+" in "+r.Parent)
	}
	sort.Strings(out)
	return out
}

func fmtInt(n int64) string { return strconv.FormatInt(n, 10) }

func obj(name string, gen int64, state string, size int64, class string) GcsObject {
	when := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	return GcsObject{Name: name, Generation: gen, State: state, Size: size, StorageClass: class, Updated: &when}
}

func gcsScan(t *testing.T, userID int64) int {
	t.Helper()
	scanId, err := LogStartScan("gcs", userID)
	if err != nil {
		t.Fatal(err)
	}
	return scanId
}

func TestGcsRecordAcrossScans(t *testing.T) {
	migrated(t)
	alice := addUser(t, "alice")

	// Scan 1 sees everything.
	s1 := gcsScan(t, alice)
	if err := UpsertGcsObjects("k1", "b1", s1, []GcsObject{
		obj("a.txt", 1, GcsLive, 10, "STANDARD"),
		obj("docs/x", 2, GcsLive, 20, "STANDARD"),
		obj("docs/x", 1, GcsNoncurrent, 5, "STANDARD"),
		obj("tmp/gone", 3, GcsSoftDeleted, 7, "STANDARD"),
		obj("docs/old", 4, GcsLive, 1, "STANDARD"),
	}); err != nil {
		t.Fatal(err)
	}
	// Another account's bucket of the same name is its own.
	if err := UpsertGcsObjects("k2", "b1", s1, []GcsObject{obj("theirs", 1, GcsLive, 1, "STANDARD")}); err != nil {
		t.Fatal(err)
	}

	// Scan 2 lists live objects only: a.txt changed, docs/old gone, new.txt added.
	s2 := gcsScan(t, alice)
	if err := UpsertGcsObjects("k1", "b1", s2, []GcsObject{
		obj("a.txt", 1, GcsLive, 11, "NEARLINE"),
		obj("docs/x", 2, GcsLive, 20, "STANDARD"),
		obj("new.txt", 5, GcsLive, 3, "STANDARD"),
	}); err != nil {
		t.Fatal(err)
	}
	deleted, err := DeleteUnseenGcsObjects("k1", "b1", s2, "", []string{GcsLive})
	if err != nil || deleted != 1 {
		t.Fatalf("deleted %d, %v; want docs/old only", deleted, err)
	}
	want := []string{
		"a.txt#1/live 11 in ", "docs/x#1/noncurrent 5 in docs/", "docs/x#2/live 20 in docs/",
		"new.txt#5/live 3 in ", "tmp/gone#3/soft_deleted 7 in tmp/",
	}
	if got := gcsRecordOf(t, "k1", "b1"); !equal(got, want) {
		t.Errorf("after scan 2: %v, want %v", got, want)
	}

	// Scan 3 lists every state, under docs/ only: docs/x's old version is
	// gone; nothing outside docs/ is touched.
	s3 := gcsScan(t, alice)
	if err := UpsertGcsObjects("k1", "b1", s3, []GcsObject{obj("docs/x", 2, GcsLive, 20, "STANDARD")}); err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteUnseenGcsObjects("k1", "b1", s3, "docs/", []string{GcsLive, GcsNoncurrent, GcsSoftDeleted}); err != nil {
		t.Fatal(err)
	}
	want = []string{"a.txt#1/live 11 in ", "docs/x#2/live 20 in docs/", "new.txt#5/live 3 in ", "tmp/gone#3/soft_deleted 7 in tmp/"}
	if got := gcsRecordOf(t, "k1", "b1"); !equal(got, want) {
		t.Errorf("after scan 3: %v, want %v", got, want)
	}
	if got := gcsRecordOf(t, "k2", "b1"); len(got) != 1 {
		t.Errorf("k2's bucket = %v, want untouched", got)
	}
}

func TestGcsPrefixTotals(t *testing.T) {
	migrated(t)
	alice := addUser(t, "alice")
	s := gcsScan(t, alice)
	if err := UpsertGcsObjects("k1", "b1", s, []GcsObject{
		obj("top.txt", 1, GcsLive, 1, "STANDARD"),
		obj("docs/a", 1, GcsLive, 10, "STANDARD"),
		obj("docs/2024/b", 1, GcsLive, 100, "NEARLINE"),
		obj("docs/2024/b", 0, GcsNoncurrent, 50, "NEARLINE"),
		obj("docs//odd", 1, GcsLive, 1000, "STANDARD"),
		obj("tmp/c", 1, GcsSoftDeleted, 7, "STANDARD"),
	}); err != nil {
		t.Fatal(err)
	}
	for range 2 { // rebuilding again replaces, not adds
		if err := RebuildGcsPrefixTotals("k1", "b1"); err != nil {
			t.Fatal(err)
		}
	}
	var rows []struct {
		Prefix  string  `db:"prefix"`
		Parent  *string `db:"parent"`
		LiveN   int64   `db:"live_objects"`
		LiveB   int64   `db:"live_bytes"`
		NoncurB int64   `db:"noncurrent_bytes"`
		SoftB   int64   `db:"soft_deleted_bytes"`
		ByClass string  `db:"bytes_by_class"`
	}
	if err := db.Select(&rows, `SELECT prefix, parent, live_objects, live_bytes, noncurrent_bytes,
			soft_deleted_bytes, bytes_by_class::text FROM gcs_prefix_totals
		WHERE client_key = 'k1' AND bucket = 'b1' ORDER BY prefix`); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rows {
		parent := "<none>"
		if r.Parent != nil {
			parent = *r.Parent
		}
		var byClass map[string]int64
		json.Unmarshal([]byte(r.ByClass), &byClass)
		got[r.Prefix] = parent + " " + fmtInt(r.LiveN) + "/" + fmtInt(r.LiveB) + " nc " + fmtInt(r.NoncurB) +
			" sd " + fmtInt(r.SoftB) + " S" + fmtInt(byClass["STANDARD"]) + " N" + fmtInt(byClass["NEARLINE"])
	}
	want := map[string]string{
		"":           "<none> 4/1111 nc 50 sd 7 S1011 N100",
		"docs/":      " 3/1110 nc 50 sd 0 S1010 N100",
		"docs/2024/": "docs/ 1/100 nc 50 sd 0 S0 N100",
		"docs//":     "docs/ 1/1000 nc 0 sd 0 S1000 N0",
		"tmp/":       " 0/0 nc 0 sd 7 S0 N0",
	}
	if len(got) != len(want) {
		t.Errorf("prefixes = %v, want %v", got, want)
	}
	for prefix, w := range want {
		if got[prefix] != w {
			t.Errorf("%q = %q, want %q", prefix, got[prefix], w)
		}
	}
}

func TestGcsScanBucketsAndSummary(t *testing.T) {
	migrated(t)
	alice := addUser(t, "alice")
	s := gcsScan(t, alice)
	if err := SaveScanMetadata("alice", "k1", "p-123456 (all buckets)", "live", s); err != nil {
		t.Fatal(err)
	}
	var t1 GcsTotals
	t1.Add(GcsLive, "STANDARD", 10)
	t1.Add(GcsLive, "NEARLINE", 5)
	t1.Add(GcsNoncurrent, "STANDARD", 3)
	t1.Add(GcsSoftDeleted, "STANDARD", 2)
	for _, row := range []GcsScanBucket{
		{Bucket: "b1", ProjectId: "p-123456", Status: GcsBucketCompleted, GcsTotals: t1},
		{Bucket: "b0", ProjectId: "p-123456", Status: GcsBucketFailed, Error: "denied"},
	} {
		if err := SaveGcsScanBucket(s, row); err != nil {
			t.Fatal(err)
		}
	}
	buckets, err := GcsScanBuckets(s)
	if err != nil || len(buckets) != 2 || buckets[0].Bucket != "b0" || buckets[0].Error != "denied" ||
		buckets[1].LiveBytes != 15 || buckets[1].BytesByClass["NEARLINE"] != 5 {
		t.Errorf("scan buckets = %+v, %v", buckets, err)
	}
	summary, err := GetScanSummary(s)
	if err != nil || summary.ItemCount != 2 || summary.TotalBytes != 15 || summary.NoncurrentCount != 1 ||
		summary.NoncurrentBytes != 3 || summary.SoftDeletedCount != 1 || summary.SoftDeletedBytes != 2 {
		t.Errorf("summary = %+v, %v", summary, err)
	}

	// Deleting the scan keeps the record, and its bucket loses the scan.
	if err := UpsertGcsObjects("k1", "b1", s, []GcsObject{obj("a", 1, GcsLive, 10, "STANDARD")}); err != nil {
		t.Fatal(err)
	}
	if err := SaveGcsBucket("k1", GcsBucketRecord{Bucket: "b1", ProjectId: "p-123456"}, s); err != nil {
		t.Fatal(err)
	}
	if err := DeleteScan(s); err != nil {
		t.Fatal(err)
	}
	if got := gcsRecordOf(t, "k1", "b1"); len(got) != 1 {
		t.Errorf("record after DeleteScan = %v, want kept", got)
	}
	var lastScan *int
	if err := db.Get(&lastScan, `SELECT last_scan_id FROM gcs_buckets WHERE client_key = 'k1' AND bucket = 'b1'`); err != nil || lastScan != nil {
		t.Errorf("last_scan_id = %v, %v; want NULL", lastScan, err)
	}
}

func TestGcsParent(t *testing.T) {
	for name, want := range map[string]string{"a.txt": "", "docs/a": "docs/", "docs/2024/b": "docs/2024/", "docs//x": "docs//", "dir/": "dir/"} {
		if got := GcsParent(name); got != want {
			t.Errorf("GcsParent(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestGcsChildren(t *testing.T) {
	migrated(t)
	alice := addUser(t, "alice")
	s := gcsScan(t, alice)
	for bucket, objects := range map[string][]GcsObject{
		"pics": {
			obj("top.txt", 1, GcsLive, 1, "STANDARD"),
			obj("docs/a", 1, GcsLive, 10, "STANDARD"),
			obj("docs/a", 0, GcsNoncurrent, 4, "STANDARD"),
			obj("docs/2024/b", 1, GcsLive, 100, "NEARLINE"),
		},
		"archive": {obj("x", 1, GcsLive, 1000, "ARCHIVE")},
	} {
		if err := UpsertGcsObjects("k1", bucket, s, objects); err != nil {
			t.Fatal(err)
		}
		if err := RebuildGcsPrefixTotals("k1", bucket); err != nil {
			t.Fatal(err)
		}
		if err := SaveGcsBucket("k1", GcsBucketRecord{Bucket: bucket, ProjectId: "p-123456", Location: "US-WEST1",
			StorageClass: "STANDARD"}, s); err != nil {
			t.Fatal(err)
		}
	}

	roots, err := GcsChildren("k1", "", 1)
	if err != nil || len(roots.Folders) != 2 || roots.Folders[0].Id != "archive/" || roots.Folders[0].Bytes != 1000 ||
		roots.Folders[1].Id != "pics/" || roots.Folders[1].Files != 3 || roots.Folders[1].Detail != "US-WEST1 · STANDARD" ||
		roots.Totals.Bytes != 1111 || roots.Gcs.NoncurrentBytes != 4 || len(roots.Files) != 0 {
		t.Errorf("roots = %+v, %v", roots, err)
	}

	bucket, err := GcsChildren("k1", "pics/", 1)
	if err != nil || len(bucket.Folders) != 1 || bucket.Folders[0].Id != "pics/docs/" || bucket.Folders[0].Bytes != 110 ||
		len(bucket.Files) != 1 || bucket.Files[0].Name != "top.txt" || bucket.Totals.Bytes != 111 ||
		len(bucket.Path) != 1 || bucket.Path[0].Name != "pics" {
		t.Errorf("pics/ = %+v, %v", bucket, err)
	}

	docs, err := GcsChildren("k1", "pics/docs/", 1)
	if err != nil || len(docs.Folders) != 1 || docs.Folders[0].Name != "2024" || docs.Folders[0].Id != "pics/docs/2024/" {
		t.Fatalf("pics/docs/ = %+v, %v", docs, err)
	}
	// Both versions of a, largest first, with class and state.
	if len(docs.Files) != 2 || docs.Files[0].Name != "a" || docs.Files[0].State != GcsLive ||
		docs.Files[1].State != GcsNoncurrent || docs.Files[1].StorageClass != "STANDARD" || docs.Files[0].Id == docs.Files[1].Id {
		t.Errorf("pics/docs/ files = %+v", docs.Files)
	}
	if docs.Gcs.NoncurrentBytes != 4 || docs.Gcs.BytesByClass["NEARLINE"] != 100 ||
		len(docs.Path) != 2 || docs.Path[1] != (PathPart{Id: "pics/docs/", Name: "docs"}) {
		t.Errorf("pics/docs/ totals and path = %+v %+v", docs.Gcs, docs.Path)
	}

	for _, missing := range []string{"nope/", "pics/nope/", "pics", "pics/docs"} {
		if _, err := GcsChildren("k1", missing, 1); err != ErrNotFound {
			t.Errorf("GcsChildren(%q) err = %v, want ErrNotFound", missing, err)
		}
	}
	if theirs, err := GcsChildren("k2", "", 1); err != nil || len(theirs.Folders) != 0 {
		t.Errorf("another account's buckets = %+v, %v", theirs, err)
	}

	totals, err := gcsServiceTotals("k1")
	if err != nil || *totals.Files != 4 || *totals.Bytes != 1111 || totals.UpdatedAt == nil {
		t.Errorf("service totals = %+v, %v", totals, err)
	}
	if none, err := gcsServiceTotals("k2"); err != nil || none.Files != nil {
		t.Errorf("no buckets: %+v, %v", none, err)
	}
}
