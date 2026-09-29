package web

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/collect"
	"github.com/jyothri/hdd/db"
)

// gcsRouter serves the API as alice (user 7), whose accounts are "both"
// (Cloud Storage and its projects), "storage-only" (no project list) and
// "gmail-only".
func gcsRouter(t *testing.T) *mux.Router {
	t.Helper()
	fakeSessions(t)
	originalAccount, originalProjects, originalBuckets := accountFor, gcsProjects, gcsBuckets
	t.Cleanup(func() { accountFor, gcsProjects, gcsBuckets = originalAccount, originalProjects, originalBuckets })
	accountFor = func(userID int64, clientKey string) (db.PrivateToken, error) {
		scopes := map[string]string{
			"both": "https://www.googleapis.com/auth/devstorage.read_only " +
				"https://www.googleapis.com/auth/cloudplatformprojects.readonly",
			"storage-only": "https://www.googleapis.com/auth/devstorage.read_only",
			"gmail-only":   "https://www.googleapis.com/auth/gmail.readonly",
		}
		if scope, ok := scopes[clientKey]; ok && userID == 7 {
			return db.PrivateToken{Client_key: clientKey, Scope: scope}, nil
		}
		return db.PrivateToken{}, fmt.Errorf("no account %s: %w", clientKey, sql.ErrNoRows)
	}
	gcsProjects = func(userID int64, clientKey string) ([]collect.GcsProject, error) {
		return []collect.GcsProject{{ProjectId: "backup-276614", DisplayName: "personal-backup"}}, nil
	}
	gcsBuckets = func(userID int64, clientKey string, project string) ([]collect.GcsBucket, error) {
		if project == "no-access-1" {
			return nil, &collect.GcsError{Status: http.StatusForbidden, Message: "This Google account isn't allowed to list the buckets of no-access-1."}
		}
		return []collect.GcsBucket{{Name: "jyo-pics", Location: "US-WEST1"}}, nil
	}
	r := mux.NewRouter()
	r.Use(authenticate)
	api(r)
	return r
}

func TestGcsRoutes(t *testing.T) {
	r := gcsRouter(t)
	for path, want := range map[string]struct {
		status int
		body   string
	}{
		"/api/gcs/both/projects":                               {200, `"projectId":"backup-276614"`},
		"/api/gcs/storage-only/projects":                       {409, "Type a project ID instead"},
		"/api/gcs/gmail-only/projects":                         {400, `hasn't granted Google Cloud Storage access. Use "Grant Cloud Storage access" first.`},
		"/api/gcs/someone-elses/projects":                      {400, "isn't linked"},
		"/api/gcs/storage-only/projects/backup-276614/buckets": {200, `"name":"jyo-pics"`},
		"/api/gcs/both/projects/Not_A_Project/buckets":         {400, "That isn't a Google Cloud project ID."},
		"/api/gcs/both/projects/no-access-1/buckets":           {403, "isn't allowed to list the buckets"},
		"/api/gcs/gmail-only/projects/backup-276614/buckets":   {400, "hasn't granted Google Cloud Storage access"},
	} {
		rec := serve(r, withSession(httptest.NewRequest(http.MethodGet, path, nil), "good"))
		if rec.Code != want.status || !strings.Contains(rec.Body.String(), want.body) {
			t.Errorf("GET %s: %d %q, want %d with %q", path, rec.Code, rec.Body, want.status, want.body)
		}
	}
}

func TestCheckGcsScanRequest(t *testing.T) {
	gcsRouter(t)
	for name, tc := range map[string]struct {
		scan   collect.GStorageScan
		status int
		msg    string
	}{
		"whole project":  {collect.GStorageScan{ClientKey: "both", ProjectId: "backup-276614"}, 200, ""},
		"bucket, prefix": {collect.GStorageScan{ClientKey: "storage-only", ProjectId: "backup-276614", Bucket: "jyo-pics", Prefix: "2024/"}, 200, ""},
		"no account":     {collect.GStorageScan{ProjectId: "backup-276614"}, 400, "Pick an account."},
		"bad project":    {collect.GStorageScan{ClientKey: "both", ProjectId: "No"}, 400, "That isn't a Google Cloud project ID."},
		"bad bucket":     {collect.GStorageScan{ClientKey: "both", ProjectId: "backup-276614", Bucket: "Bad Bucket"}, 400, "That isn't a bucket name."},
		"prefix only":    {collect.GStorageScan{ClientKey: "both", ProjectId: "backup-276614", Prefix: "a/"}, 400, "A prefix needs a bucket."},
		"long prefix":    {collect.GStorageScan{ClientKey: "both", ProjectId: "backup-276614", Bucket: "b12", Prefix: strings.Repeat("a", 1025)}, 400, "The prefix is too long"},
		"not granted":    {collect.GStorageScan{ClientKey: "gmail-only", ProjectId: "backup-276614"}, 400, "hasn't granted Google Cloud Storage access"},
		"someone else's": {collect.GStorageScan{ClientKey: "theirs", ProjectId: "backup-276614"}, 400, "isn't linked"},
	} {
		status, msg := checkScanRequest(DoScanRequest{ScanType: "GStorage", GStorageScan: tc.scan}, 7)
		if status != tc.status || !strings.Contains(msg, tc.msg) {
			t.Errorf("%s: %d %q, want %d with %q", name, status, msg, tc.status, tc.msg)
		}
	}
}

func TestGcsScanBucketsRoute(t *testing.T) {
	r := gcsRouter(t)
	originalOwner, originalBuckets := scanOwnedBy, gcsScanBuckets
	t.Cleanup(func() { scanOwnedBy, gcsScanBuckets = originalOwner, originalBuckets })
	scanOwnedBy = func(scanId int, userID int64) (bool, error) { return userID == 7 && scanId == 42, nil }
	gcsScanBuckets = func(scanId int) ([]db.GcsScanBucket, error) {
		return []db.GcsScanBucket{{Bucket: "jyo-pics", Status: db.GcsBucketCompleted,
			GcsTotals: db.GcsTotals{LiveObjects: 2, LiveBytes: 30, BytesByClass: map[string]int64{"STANDARD": 30}}}}, nil
	}
	rec := serve(r, withSession(httptest.NewRequest(http.MethodGet, "/api/gcs/42", nil), "good"))
	for _, want := range []string{`"bucket":"jyo-pics"`, `"live_bytes":30`, `"bytes_by_class":{"STANDARD":30}`} {
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("GET /api/gcs/42: %d %s, want %s", rec.Code, rec.Body, want)
		}
	}
	if rec := serve(r, withSession(httptest.NewRequest(http.MethodGet, "/api/gcs/43", nil), "good")); rec.Code != 404 {
		t.Errorf("another user's scan: %d, want 404", rec.Code)
	}
}
