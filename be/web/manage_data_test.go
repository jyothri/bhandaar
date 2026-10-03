package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/db"
)

// fakeDeletions fakes what the Manage data handlers use, for alice (user 7):
// account "mine" (named jyo****ri@gmail.com) and agent drive 1. Jobs run
// at once, and what they do is recorded in calls.
type fakeDeletions struct {
	calls    []string
	running  int  // a running scan of "mine", or 0
	inFlight bool // a job for the target is already running
	finished []string
}

func newFakeDeletions(t *testing.T) *fakeDeletions {
	t.Helper()
	fakeBrowseOwners(t)
	f := &fakeDeletions{}
	saved := []any{requestAccounts, runningScanOf, agentDriveLabel, startDeletion, finishDeletion,
		deleteAgentDrive, deleteService, deleteAccount, refreshTokenOf, runJob, revokeEndpoint}
	t.Cleanup(func() {
		requestAccounts = saved[0].(func(int64) ([]db.Account, error))
		runningScanOf = saved[1].(func(int64, string) (int, error))
		agentDriveLabel = saved[2].(func(int64) (string, error))
		startDeletion = saved[3].(func(int64, string, string, string) (db.DeletionJob, bool, error))
		finishDeletion = saved[4].(func(int64, map[string]int64, string, error) error)
		deleteAgentDrive = saved[5].(func(int64) (map[string]int64, error))
		deleteService = saved[6].(func(int64, string, string) (map[string]int64, error))
		deleteAccount = saved[7].(func(int64, string) (map[string]int64, error))
		refreshTokenOf = saved[8].(func(int64, string) (string, error))
		runJob = saved[9].(func(func()))
		revokeEndpoint = saved[10].(string)
	})
	requestAccounts = func(int64) ([]db.Account, error) {
		return []db.Account{{ClientKey: "mine", DisplayName: "jyo****ri@gmail.com"}}, nil
	}
	runningScanOf = func(int64, string) (int, error) { return f.running, nil }
	agentDriveLabel = func(int64) (string, error) { return "seagate1 (mbp)", nil }
	startDeletion = func(user int64, kind, target, label string) (db.DeletionJob, bool, error) {
		f.calls = append(f.calls, fmt.Sprintf("start %s %s %q", kind, target, label))
		return db.DeletionJob{ID: 42, Kind: kind, Target: target, Label: label, Status: db.JobRunning}, !f.inFlight, nil
	}
	finishDeletion = func(id int64, counts map[string]int64, revoke string, err error) error {
		f.finished = append(f.finished, fmt.Sprintf("%d %v %q %v", id, counts, revoke, err))
		return nil
	}
	deleteAgentDrive = func(pk int64) (map[string]int64, error) {
		f.calls = append(f.calls, fmt.Sprintf("delete drive %d", pk))
		return map[string]int64{"files": 3}, nil
	}
	deleteService = func(user int64, key string, service string) (map[string]int64, error) {
		f.calls = append(f.calls, "delete "+service+" "+key)
		return map[string]int64{"scans": 2}, nil
	}
	deleteAccount = func(user int64, key string) (map[string]int64, error) {
		f.calls = append(f.calls, "delete account "+key)
		return map[string]int64{"scans": 1}, nil
	}
	refreshTokenOf = func(int64, string) (string, error) { return "rt", nil }
	runJob = func(job func()) { job() }
	return f
}

// call sends a request as alice through the Manage data routes.
func call(method, path, body string) *httptest.ResponseRecorder {
	r := mux.NewRouter()
	manageDataRoutes(r.PathPrefix("/api/").Subrouter())
	req := withUser(httptest.NewRequest(method, path, strings.NewReader(body)), db.User{ID: 7, Username: "alice"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// fakeRevoke answers revocations with status and body, and records tokens.
func fakeRevoke(t *testing.T, status int, body string) *[]string {
	t.Helper()
	var tokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		tokens = append(tokens, r.PostFormValue("token"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	revokeEndpoint = server.URL
	return &tokens
}

func TestDeleteAgentDriveStartsAJob(t *testing.T) {
	f := newFakeDeletions(t)
	rec := call("DELETE", "/api/agent-drives/1", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var job db.DeletionJob
	json.Unmarshal(rec.Body.Bytes(), &job)
	if job.ID != 42 || job.Label != "seagate1 (mbp)" {
		t.Errorf("job %+v", job)
	}
	want := []string{`start agent_drive 1 "seagate1 (mbp)"`, "delete drive 1"}
	if strings.Join(f.calls, "|") != strings.Join(want, "|") || len(f.finished) != 1 {
		t.Errorf("calls %q, finished %q", f.calls, f.finished)
	}
	// Another user's drive, or a drive that isn't there, is 404.
	if rec := call("DELETE", "/api/agent-drives/2", ""); rec.Code != http.StatusNotFound {
		t.Errorf("another drive: %d, want 404", rec.Code)
	}
}

func TestDeleteAService(t *testing.T) {
	f := newFakeDeletions(t)
	if rec := call("DELETE", "/api/accounts/theirs/gmail", ""); rec.Code != http.StatusNotFound {
		t.Errorf("another user's account: %d, want 404", rec.Code)
	}
	f.running = 9
	rec := call("DELETE", "/api/accounts/mine/gmail", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "Scan 9") {
		t.Errorf("with a scan running: %d %q, want 409 naming scan 9", rec.Code, rec.Body)
	}
	f.running = 0
	if rec := call("DELETE", "/api/accounts/mine/gmail", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("status %d", rec.Code)
	}
	for _, service := range []string{"drive", "gcs", "photos"} {
		if rec := call("DELETE", "/api/accounts/mine/"+service, ""); rec.Code != http.StatusAccepted {
			t.Fatalf("%s: status %d", service, rec.Code)
		}
	}
	want := []string{
		`start gmail mine "jyo****ri@gmail.com · Gmail"`, "delete gmail mine",
		`start drive mine "jyo****ri@gmail.com · Google Drive"`, "delete drive mine",
		`start gcs mine "jyo****ri@gmail.com · Cloud Storage"`, "delete gcs mine",
		`start photos mine "jyo****ri@gmail.com · Google Photos"`, "delete photos mine",
	}
	if strings.Join(f.calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls %q", f.calls)
	}
	// Not a service.
	if rec := call("DELETE", "/api/accounts/mine/calendar", ""); rec.Code == http.StatusAccepted {
		t.Errorf("an unknown service was accepted")
	}
}

func TestDisconnectNeedsTheAccountsName(t *testing.T) {
	f := newFakeDeletions(t)
	tokens := fakeRevoke(t, http.StatusOK, "{}")
	for _, body := range []string{`{}`, `{"confirm": "jyo****ri"}`, `{"confirm": "JYO****RI@GMAIL.COM"}`, `not json`} {
		if rec := call("DELETE", "/api/accounts/mine", body); rec.Code != http.StatusBadRequest {
			t.Errorf("confirm %s: %d, want 400", body, rec.Code)
		}
	}
	if len(f.calls) != 0 || len(*tokens) != 0 {
		t.Fatalf("something ran without the name: %q, %q", f.calls, *tokens)
	}
	// Spaces around the name are fine.
	rec := call("DELETE", "/api/accounts/mine", `{"confirm": " jyo****ri@gmail.com "}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if strings.Join(*tokens, ",") != "rt" || !strings.Contains(strings.Join(f.calls, "|"), "delete account mine") {
		t.Errorf("revoked %q, calls %q", *tokens, f.calls)
	}
	if len(f.finished) != 1 || !strings.Contains(f.finished[0], `"revoked"`) {
		t.Errorf("finished %q, want revoked", f.finished)
	}
}

func TestDisconnectWhileAScanRuns(t *testing.T) {
	f := newFakeDeletions(t)
	tokens := fakeRevoke(t, http.StatusOK, "{}")
	f.running = 3
	if rec := call("DELETE", "/api/accounts/mine", `{"confirm": "jyo****ri@gmail.com"}`); rec.Code != http.StatusConflict {
		t.Errorf("status %d, want 409", rec.Code)
	}
	if len(*tokens) != 0 || len(f.calls) != 0 {
		t.Errorf("revoked %q, calls %q; want nothing", *tokens, f.calls)
	}
}

func TestADeletionAlreadyRunningIsReturned(t *testing.T) {
	f := newFakeDeletions(t)
	f.inFlight = true
	if rec := call("DELETE", "/api/agent-drives/1", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("status %d", rec.Code)
	}
	// The running job is returned; nothing new runs.
	if len(f.calls) != 1 || len(f.finished) != 0 {
		t.Errorf("calls %q, finished %q", f.calls, f.finished)
	}
}

func TestRevokeToken(t *testing.T) {
	newFakeDeletions(t)
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{http.StatusOK, `{}`, "revoked"},
		{http.StatusBadRequest, `{"error": "invalid_token"}`, "already revoked"},
		{http.StatusInternalServerError, `{"error": "backend"}`, "failed: Google answered 500 Internal Server Error (backend)"},
	}
	for _, c := range cases {
		fakeRevoke(t, c.status, c.body)
		if got := revokeToken("rt"); got != c.want {
			t.Errorf("Google answering %d %s: %q, want %q", c.status, c.body, got, c.want)
		}
	}
	if got := revokeToken(""); got != "already revoked" {
		t.Errorf("no token: %q", got)
	}
	revokeEndpoint = "http://127.0.0.1:1/revoke" // nothing listens there
	if got := revokeToken("rt"); !strings.HasPrefix(got, "failed: ") {
		t.Errorf("Google unreachable: %q, want a failure", got)
	}
}

func TestDeletionHandler(t *testing.T) {
	newFakeDeletions(t)
	saved := getDeletion
	t.Cleanup(func() { getDeletion = saved })
	getDeletion = func(user int64, id int64) (db.DeletionJob, error) {
		if user == 7 && id == 42 {
			return db.DeletionJob{ID: 42, Status: db.JobDone}, nil
		}
		return db.DeletionJob{}, db.ErrNotFound
	}
	if rec := call("GET", "/api/deletions/42", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"done"`) {
		t.Errorf("own job: %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/api/deletions/43", "/api/deletions/x"} {
		if rec := call("GET", path, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, rec.Code)
		}
	}
}

func TestDisconnectDoesntRevokeWhenTheDeletionFails(t *testing.T) {
	f := newFakeDeletions(t)
	tokens := fakeRevoke(t, http.StatusOK, "{}")
	deleteAccount = func(int64, string) (map[string]int64, error) {
		return nil, db.ErrScanRunning
	}
	if rec := call("DELETE", "/api/accounts/mine", `{"confirm": "jyo****ri@gmail.com"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("status %d", rec.Code)
	}
	// The account stays usable: nothing was revoked.
	if len(*tokens) != 0 {
		t.Errorf("revoked %q although the deletion failed", *tokens)
	}
	if len(f.finished) != 1 || !strings.Contains(f.finished[0], "a scan of this account started") {
		t.Errorf("finished %q, want the failure", f.finished)
	}
}

func TestAnotherDeletionOfTheAccountIsConflict(t *testing.T) {
	f := newFakeDeletions(t)
	startDeletion = func(user int64, kind, target, label string) (db.DeletionJob, bool, error) {
		f.calls = append(f.calls, "start "+kind)
		// The account's own deletion is running.
		return db.DeletionJob{ID: 41, Kind: db.DeleteAccount, Target: target, Label: "jyo****ri@gmail.com", Status: db.JobRunning}, false, nil
	}
	rec := call("DELETE", "/api/accounts/mine/gmail", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "Another deletion of this account") {
		t.Errorf("status %d %q, want 409", rec.Code, rec.Body)
	}
	if len(f.finished) != 0 {
		t.Errorf("a job ran: %q", f.finished)
	}
}
