package collect

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jyothri/hdd/db"
	"google.golang.org/api/option"
)

// fakeGcs serves Resource Manager's projects.search and Cloud Storage's
// JSON API, as far as the tests need them, and records the requests.
type fakeGcs struct {
	t        *testing.T
	projects []map[string]any
	buckets  map[string][]map[string]any // by project
	// Answered instead, when set: status and body.
	fail     func(r *http.Request) (int, string)
	mu       sync.Mutex
	requests []string
}

func newFakeGcs(t *testing.T) *fakeGcs {
	t.Helper()
	f := &fakeGcs{t: t, buckets: map[string][]map[string]any{}}
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
