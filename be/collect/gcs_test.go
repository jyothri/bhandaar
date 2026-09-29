package collect

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/notification"
	"google.golang.org/api/option"
)

// fakeGcs serves Resource Manager's projects.search and Cloud Storage's
// JSON API, as far as the tests need them, and records the requests.
type fakeGcs struct {
	t        *testing.T
	projects []map[string]any
	buckets  map[string][]map[string]any // by project
	// Objects by bucket; an item with "_soft" set lists only with
	// softDeleted=true, one with "timeDeleted" only with versions=true.
	objects map[string][]map[string]any
	// Buckets whose live ("live") or soft-deleted ("soft") listing
	// answers 403.
	denied map[string]string
	// Answered instead, when set: status and body.
	fail     func(r *http.Request) (int, string)
	mu       sync.Mutex
	requests []string
}

func newFakeGcs(t *testing.T) *fakeGcs {
	t.Helper()
	f := &fakeGcs{t: t, buckets: map[string][]map[string]any{}, objects: map[string][]map[string]any{},
		denied: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v3/projects:search", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") != "state:ACTIVE" {
			t.Errorf("projects.search query = %q, want state:ACTIVE", r.URL.Query().Get("query"))
		}
		// Two pages: the first project, then the rest.
		if r.URL.Query().Get("pageToken") == "" && len(f.projects) > 1 {
			json.NewEncoder(w).Encode(map[string]any{"projects": f.projects[:1], "nextPageToken": "2"})
			return
		}
		rest := f.projects
		if len(rest) > 1 {
			rest = rest[1:]
		}
		json.NewEncoder(w).Encode(map[string]any{"projects": rest})
	})
	mux.HandleFunc("GET /storage/v1/b", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"kind": "storage#buckets",
			"items": f.buckets[r.URL.Query().Get("project")]})
	})
	mux.HandleFunc("GET /storage/v1/b/{bucket}", func(w http.ResponseWriter, r *http.Request) {
		for _, list := range f.buckets {
			for _, b := range list {
				if b["name"] == r.PathValue("bucket") {
					json.NewEncoder(w).Encode(b)
					return
				}
			}
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": {"code": 404, "message": "The specified bucket does not exist."}}`))
	})
	// Two items a page.
	mux.HandleFunc("GET /storage/v1/b/{bucket}/o", func(w http.ResponseWriter, r *http.Request) {
		bucket, q := r.PathValue("bucket"), r.URL.Query()
		soft, versions := q.Get("softDeleted") == "true", q.Get("versions") == "true"
		if (soft && f.denied[bucket] == "soft") || (!soft && f.denied[bucket] == "live") {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error": {"code": 403, "message": "denied"}}`))
			return
		}
		var match []map[string]any
		for _, o := range f.objects[bucket] {
			_, isSoft := o["_soft"]
			_, noncurrent := o["timeDeleted"]
			if isSoft != soft || (noncurrent && !versions) || !strings.HasPrefix(o["name"].(string), q.Get("prefix")) {
				continue
			}
			item := map[string]any{"bucket": bucket}
			for k, v := range o {
				if k != "_soft" {
					item[k] = v
				}
			}
			match = append(match, item)
		}
		start, _ := strconv.Atoi(q.Get("pageToken"))
		end := min(start+2, len(match))
		page := map[string]any{"kind": "storage#objects", "items": match[start:end]}
		if end < len(match) {
			page["nextPageToken"] = strconv.Itoa(end)
		}
		json.NewEncoder(w).Encode(page)
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		if f.fail != nil {
			if status, body := f.fail(r); status != 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				w.Write([]byte(body))
				return
			}
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	originalOptions, originalAccount := gcsOptions, linkedAccount
	gcsOptions = func(refreshToken string, api string) []option.ClientOption {
		if refreshToken != "rt-gcs" {
			t.Errorf("client for refresh token %q, want the linked account's", refreshToken)
		}
		endpoint := server.URL + "/"
		if api == "storage" {
			endpoint = server.URL + "/storage/v1/"
		}
		return []option.ClientOption{option.WithEndpoint(endpoint), option.WithoutAuthentication()}
	}
	linkedAccount = func(userID int64, clientKey string) (db.PrivateToken, error) {
		if userID == 7 && clientKey == "k1" {
			return db.PrivateToken{Client_key: "k1", RefreshToken: "rt-gcs", DisplayName: "alice"}, nil
		}
		return db.PrivateToken{}, errors.New("not found")
	}
	t.Cleanup(func() { gcsOptions, linkedAccount = originalOptions, originalAccount })
	return f
}

func TestGcsProjects(t *testing.T) {
	f := newFakeGcs(t)
	f.projects = []map[string]any{
		{"projectId": "zeta-1234", "displayName": "Zeta", "state": "ACTIVE"},
		{"projectId": "backup-276614", "displayName": "personal-backup", "state": "ACTIVE"},
		{"projectId": "alpha-9999", "displayName": "Zeta", "state": "ACTIVE"},
	}
	projects, err := GcsProjects(7, "k1")
	if err != nil {
		t.Fatal(err)
	}
	want := []GcsProject{{"backup-276614", "personal-backup"}, {"alpha-9999", "Zeta"}, {"zeta-1234", "Zeta"}}
	if len(projects) != len(want) {
		t.Fatalf("projects = %+v, want %+v", projects, want)
	}
	for i := range want {
		if projects[i] != want[i] {
			t.Errorf("projects[%d] = %+v, want %+v", i, projects[i], want[i])
		}
	}
	if _, err := GcsProjects(8, "k1"); err == nil {
		t.Error("user 8 listed user 7's projects")
	}
}

func TestGcsBuckets(t *testing.T) {
	f := newFakeGcs(t)
	f.buckets["backup-276614"] = []map[string]any{
		{"name": "jyo-pics", "location": "US-WEST1", "storageClass": "STANDARD",
			"softDeletePolicy": map[string]any{"retentionDurationSeconds": "604800"}},
		{"name": "jyo-archive", "location": "US-WEST1", "storageClass": "ARCHIVE",
			"versioning": map[string]any{"enabled": true}, "billing": map[string]any{"requesterPays": true},
			"softDeletePolicy": map[string]any{"retentionDurationSeconds": "0"}},
	}
	buckets, err := GcsBuckets(7, "k1", "backup-276614")
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 {
		t.Fatalf("buckets = %+v", buckets)
	}
	archive, pics := buckets[0], buckets[1]
	if archive.Name != "jyo-archive" || archive.StorageClass != "ARCHIVE" || !archive.Versioning ||
		!archive.RequesterPays || archive.SoftDeleteDays != nil {
		t.Errorf("archive = %+v", archive)
	}
	if pics.Name != "jyo-pics" || pics.Location != "US-WEST1" || pics.Versioning || pics.RequesterPays ||
		pics.SoftDeleteDays == nil || *pics.SoftDeleteDays != 7 {
		t.Errorf("pics = %+v", pics)
	}
}

func TestGcsErrorsSayWhatGoogleRefused(t *testing.T) {
	f := newFakeGcs(t)
	for name, tc := range map[string]struct {
		status int
		body   string
		want   GcsError
	}{
		"API disabled": {403, `{"error": {"code": 403, "message": "Cloud Resource Manager API has not been used",
			"details": [{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": "SERVICE_DISABLED"}]}}`,
			GcsError{http.StatusBadGateway, "Google refused to list the account's projects: an API isn't enabled for this app's Google Cloud project."}},
		"no permission": {403, `{"error": {"code": 403, "message": "denied"}}`,
			GcsError{http.StatusForbidden, "This Google account isn't allowed to list the account's projects."}},
		"other": {400, `{"error": {"code": 400, "message": "bad query"}}`,
			GcsError{http.StatusBadGateway, "Google couldn't list the account's projects: bad query"}},
	} {
		f.fail = func(*http.Request) (int, string) { return tc.status, tc.body }
		_, err := GcsProjects(7, "k1")
		var gcsErr *GcsError
		if !errors.As(err, &gcsErr) || *gcsErr != tc.want {
			t.Errorf("%s: err = %v, want %+v", name, err, tc.want)
		}
	}
}

func TestValidProjectIdAndBucketName(t *testing.T) {
	for id, want := range map[string]bool{
		"personal-backup-276614": true, "example.com:my-project": true, "abcdef": true,
		"abc": false, "Upper-case1": false, "ends-with-": false, "1starts-digit": false, "": false,
	} {
		if got := ValidProjectId(id); got != want {
			t.Errorf("ValidProjectId(%q) = %v, want %v", id, got, want)
		}
	}
	for name, want := range map[string]bool{
		"jyo-archive": true, "a_b": true, "my.bucket.example.com": true,
		"ab": false, "-leading": false, "UPPER": false, "has space": false,
		"a234567890123456789012345678901234567890123456789012345678901234": false, // 64, no dots
	} {
		if got := ValidBucketName(name); got != want {
			t.Errorf("ValidBucketName(%q) = %v, want %v", name, got, want)
		}
	}
}

// gcsRecord records what a scan writes to the record, in memory.
type gcsRecord struct {
	mu       sync.Mutex
	objects  map[string]db.GcsObject // "bucket/name#generation/state"
	deletes  map[string][]string     // bucket → states deleted-unseen
	rebuilt  []string
	buckets  map[string]db.GcsBucketRecord
	scanRows map[string]db.GcsScanBucket
}

func recordGcs(t *testing.T) *gcsRecord {
	t.Helper()
	rec := &gcsRecord{objects: map[string]db.GcsObject{}, deletes: map[string][]string{},
		buckets: map[string]db.GcsBucketRecord{}, scanRows: map[string]db.GcsScanBucket{}}
	u, d, r, b, s := upsertGcsObjects, deleteUnseenGcsObjects, rebuildGcsPrefixTotals, saveGcsBucket, saveGcsScanBucket
	t.Cleanup(func() {
		upsertGcsObjects, deleteUnseenGcsObjects, rebuildGcsPrefixTotals, saveGcsBucket, saveGcsScanBucket = u, d, r, b, s
	})
	upsertGcsObjects = func(clientKey, bucket string, scanId int, objects []db.GcsObject) error {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		for _, o := range objects {
			rec.objects[bucket+"/"+o.Name+"#"+strconv.FormatInt(o.Generation, 10)+"/"+o.State] = o
		}
		return nil
	}
	deleteUnseenGcsObjects = func(clientKey, bucket string, scanId int, prefix string, states []string) (int64, error) {
		rec.deletes[bucket] = states
		return 0, nil
	}
	rebuildGcsPrefixTotals = func(clientKey, bucket string) error {
		rec.rebuilt = append(rec.rebuilt, bucket)
		return nil
	}
	saveGcsBucket = func(clientKey string, b db.GcsBucketRecord, scanId int) error {
		rec.buckets[b.Bucket] = b
		return nil
	}
	saveGcsScanBucket = func(scanId int, row db.GcsScanBucket) error {
		rec.scanRows[row.Bucket] = row
		return nil
	}
	return rec
}

func b64(b ...byte) string { return base64.StdEncoding.EncodeToString(b) }

// A versioned bucket (live, noncurrent, composite and soft-deleted
// objects), a Requester Pays bucket, and one the account can't list.
func gcsTestProject(f *fakeGcs) []GcsBucket {
	f.buckets["backup-276614"] = []map[string]any{
		{"name": "versioned", "location": "US-WEST1", "storageClass": "STANDARD",
			"versioning": map[string]any{"enabled": true}, "softDeletePolicy": map[string]any{"retentionDurationSeconds": "604800"}},
		{"name": "payer", "billing": map[string]any{"requesterPays": true}},
		{"name": "locked"},
	}
	f.objects["versioned"] = []map[string]any{
		{"name": "docs/a.txt", "generation": "2", "size": "10", "storageClass": "STANDARD",
			"md5Hash": b64(0xde, 0xad, 0xbe, 0xef), "crc32c": b64(0, 0, 0, 7), "updated": "2026-09-01T10:00:00Z"},
		{"name": "docs/a.txt", "generation": "1", "size": "3", "storageClass": "STANDARD",
			"timeDeleted": "2026-09-01T10:00:00Z", "updated": "2026-08-01T10:00:00Z"},
		{"name": "cold/b.txt", "generation": "5", "size": "5", "storageClass": "NEARLINE"},
		{"name": "composite.txt", "generation": "6", "size": "15", "storageClass": "STANDARD", "componentCount": 2},
		{"name": "tmp/c.txt", "generation": "9", "size": "5", "storageClass": "STANDARD", "_soft": true,
			"softDeleteTime": "2026-09-02T10:00:00Z", "hardDeleteTime": "2026-09-09T10:00:00Z"},
	}
	f.denied["locked"] = "live"
	return []GcsBucket{
		{Name: "versioned", Location: "US-WEST1", StorageClass: "STANDARD", Versioning: true},
		{Name: "payer", RequesterPays: true},
		{Name: "locked"},
	}
}

func gcsTestClient(t *testing.T) *storage.Client {
	t.Helper()
	client, err := gcsClient(context.Background(), "rt-gcs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func TestScanGcsListsEveryStateIntoTheRecord(t *testing.T) {
	f := newFakeGcs(t)
	buckets := gcsTestProject(f)
	rec := recordGcs(t)
	scan := GStorageScan{ClientKey: "k1", ProjectId: "backup-276614", Versions: true, SoftDeleted: true}
	clientKey := "gcs-" + t.Name()
	events, unsubscribe := notification.Subscribe(clientKey)
	defer unsubscribe()

	if err := scanGcs(gcsTestClient(t), 3, clientKey, scan, buckets); err != nil {
		t.Fatalf("scanGcs: %v", err)
	}

	want := map[string]string{
		"versioned/docs/a.txt#2/live":        "10 STANDARD deadbeef 00000007 updated",
		"versioned/docs/a.txt#1/noncurrent":  "3 STANDARD   updated deleted",
		"versioned/cold/b.txt#5/live":        "5 NEARLINE  ",
		"versioned/composite.txt#6/live":     "15 STANDARD  ",
		"versioned/tmp/c.txt#9/soft_deleted": "5 STANDARD   hard-delete",
	}
	if len(rec.objects) != len(want) {
		t.Errorf("record has %d objects, want %d: %v", len(rec.objects), len(want), rec.objects)
	}
	for key, w := range want {
		o, ok := rec.objects[key]
		got := fmt.Sprintf("%d %s %s %s", o.Size, o.StorageClass, o.Md5Hash, o.Crc32c)
		if o.Updated != nil {
			got += " updated"
		}
		if o.TimeDeleted != nil {
			got += " deleted"
		}
		if o.HardDeleteTime != nil {
			got += " hard-delete"
		}
		if !ok || got != w {
			t.Errorf("%s = %q (found %v), want %q", key, got, ok, w)
		}
	}
	if got := rec.deletes["versioned"]; strings.Join(got, ",") != "live,noncurrent,soft_deleted" {
		t.Errorf("deleted unseen in states %v, want all three", got)
	}
	if len(rec.deletes) != 1 || strings.Join(rec.rebuilt, ",") != "versioned" {
		t.Errorf("deletes %v, rebuilt %v: want only the completed bucket", rec.deletes, rec.rebuilt)
	}
	if b := rec.buckets["versioned"]; b.ProjectId != "backup-276614" || !b.Versioning || b.Location != "US-WEST1" {
		t.Errorf("bucket record = %+v", b)
	}

	v, p, l := rec.scanRows["versioned"], rec.scanRows["payer"], rec.scanRows["locked"]
	if v.Status != db.GcsBucketCompleted || v.Error != "" || v.LiveObjects != 3 || v.LiveBytes != 30 ||
		v.NoncurrentObjects != 1 || v.NoncurrentBytes != 3 || v.SoftDeletedObjects != 1 || v.SoftDeletedBytes != 5 ||
		v.BytesByClass["STANDARD"] != 25 || v.BytesByClass["NEARLINE"] != 5 {
		t.Errorf("versioned = %+v", v)
	}
	if p.Status != db.GcsBucketSkipped || !strings.Contains(p.Error, "Requester Pays") {
		t.Errorf("payer = %+v", p)
	}
	if l.Status != db.GcsBucketFailed || !strings.Contains(l.Error, "isn't allowed to list the objects of locked") {
		t.Errorf("locked = %+v", l)
	}
	select {
	case progress := <-events:
		if progress.ScanId != 3 || progress.ProcessedCount != 5 {
			t.Errorf("progress = %+v, want scan 3 with 5 objects", progress)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no progress published")
	}
}

func TestScanGcsWithoutVersionsOrSoftDeleted(t *testing.T) {
	f := newFakeGcs(t)
	gcsTestProject(f)
	rec := recordGcs(t)
	scan := GStorageScan{ClientKey: "k1", ProjectId: "backup-276614", Bucket: "versioned", Prefix: "docs/"}
	if err := scanGcs(gcsTestClient(t), 3, "k1", scan, []GcsBucket{{Name: "versioned"}}); err != nil {
		t.Fatal(err)
	}
	if len(rec.objects) != 1 || rec.objects["versioned/docs/a.txt#2/live"].Size != 10 {
		t.Errorf("record = %v, want only the live docs/a.txt", rec.objects)
	}
	// Noncurrent and soft-deleted rows weren't listed, so they stay.
	if got := rec.deletes["versioned"]; strings.Join(got, ",") != "live" {
		t.Errorf("deleted unseen in states %v, want live only", got)
	}
	if row := rec.scanRows["versioned"]; row.Prefix != "docs/" || row.LiveObjects != 1 {
		t.Errorf("scan row = %+v", row)
	}
}

func TestScanGcsSoftDeletedPassCanFailAlone(t *testing.T) {
	f := newFakeGcs(t)
	gcsTestProject(f)
	f.denied["versioned"] = "soft"
	rec := recordGcs(t)
	scan := GStorageScan{ClientKey: "k1", ProjectId: "backup-276614", Versions: true, SoftDeleted: true}
	if err := scanGcs(gcsTestClient(t), 3, "k1", scan, []GcsBucket{{Name: "versioned"}}); err != nil {
		t.Fatal(err)
	}
	row := rec.scanRows["versioned"]
	if row.Status != db.GcsBucketCompleted || !strings.HasPrefix(row.Error, "Soft-deleted objects couldn't be listed") {
		t.Errorf("row = %+v", row)
	}
	if got := rec.deletes["versioned"]; strings.Join(got, ",") != "live,noncurrent" {
		t.Errorf("deleted unseen in states %v, want live and noncurrent", got)
	}
}

func TestScanGcsFailsWhenEveryBucketDoes(t *testing.T) {
	f := newFakeGcs(t)
	gcsTestProject(f)
	recordGcs(t)
	scan := GStorageScan{ClientKey: "k1", ProjectId: "backup-276614"}
	err := scanGcs(gcsTestClient(t), 3, "k1", scan, []GcsBucket{{Name: "locked"}, {Name: "payer", RequesterPays: true}})
	if err == nil || !strings.Contains(err.Error(), "no bucket could be scanned") {
		t.Errorf("err = %v, want every bucket failed", err)
	}
}

func TestScanTargets(t *testing.T) {
	f := newFakeGcs(t)
	gcsTestProject(f)
	client := gcsTestClient(t)
	all, err := scanTargets(context.Background(), client, GStorageScan{ProjectId: "backup-276614"})
	if err != nil || len(all) != 3 {
		t.Errorf("all buckets = %+v, %v", all, err)
	}
	one, err := scanTargets(context.Background(), client, GStorageScan{ProjectId: "backup-276614", Bucket: "versioned"})
	if err != nil || len(one) != 1 || !one[0].Versioning || one[0].SoftDeleteDays == nil {
		t.Errorf("one bucket = %+v, %v", one, err)
	}
	_, err = scanTargets(context.Background(), client, GStorageScan{ProjectId: "backup-276614", Bucket: "missing"})
	var gcsErr *GcsError
	if !errors.As(err, &gcsErr) || gcsErr.Status != http.StatusNotFound {
		t.Errorf("missing bucket: err = %v, want a 404 GcsError", err)
	}
}

func TestGcsSearchPathAndIncludes(t *testing.T) {
	all := GStorageScan{ProjectId: "p-123456", Versions: true, SoftDeleted: true}
	one := GStorageScan{ProjectId: "p-123456", Bucket: "b1", Prefix: "docs/"}
	if got := gcsSearchPath(all); got != "p-123456 (all buckets)" {
		t.Errorf("all: %q", got)
	}
	if got := gcsSearchPath(one); got != "p-123456/b1/docs/" {
		t.Errorf("one: %q", got)
	}
	if got := gcsIncludes(all); got != "live, noncurrent versions, soft-deleted" {
		t.Errorf("includes: %q", got)
	}
	if got := gcsIncludes(one); got != "live" {
		t.Errorf("includes: %q", got)
	}
}
