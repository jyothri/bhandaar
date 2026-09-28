package db

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
)

// agentserverDDL is the part of agentserver's schema (its migration 0002)
// that be reads.
const agentserverDDL = `
CREATE TABLE agent_users (
  id             BIGSERIAL PRIMARY KEY,
  username       TEXT NOT NULL UNIQUE,
  password_hash  TEXT NOT NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  disabled_at    TIMESTAMPTZ
);
CREATE TABLE agent_login_failures (
  username   TEXT NOT NULL,
  client_ip  INET NOT NULL,
  failed_at  TIMESTAMPTZ NOT NULL
);`

// useTestDB points the package at a fresh schema in the database named by
// BE_TEST_DB (a postgres:// URL), and skips the test without it. The
// migrations' information_schema checks aren't limited to one schema, so
// use a database of its own.
func useTestDB(t *testing.T) {
	t.Helper()
	dsn := os.Getenv("BE_TEST_DB")
	if dsn == "" {
		t.Skip("BE_TEST_DB not set")
	}
	b := make([]byte, 6)
	rand.Read(b)
	schema := "be_test_" + hex.EncodeToString(b)

	admin, err := sqlx.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec("DROP SCHEMA " + schema + " CASCADE") })

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	conn, err := sqlx.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	original := db
	db = conn
	t.Cleanup(func() {
		conn.Close()
		db = original
	})
}

func addUser(t *testing.T, username string) int64 {
	t.Helper()
	var id int64
	if err := db.Get(&id, `INSERT INTO agent_users (username, password_hash) VALUES ($1, 'x') RETURNING id`,
		username); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMigrateNeedsAgentserversUsers(t *testing.T) {
	useTestDB(t)
	err := migrateDB()
	if err == nil || !strings.Contains(err.Error(), "agent_users") {
		t.Fatalf("migrateDB without agent_users: err = %v, want one naming agent_users", err)
	}
	// Once agentserver has created its tables, the migration completes.
	if _, err := db.Exec(agentserverDDL); err != nil {
		t.Fatal(err)
	}
	if err := migrateDB(); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}
	if err := migrateDB(); err != nil {
		t.Fatalf("migrateDB again: %v", err)
	}
}

func migrated(t *testing.T) {
	t.Helper()
	useTestDB(t)
	if _, err := db.Exec(agentserverDDL); err != nil {
		t.Fatal(err)
	}
	if err := migrateDB(); err != nil {
		t.Fatal(err)
	}
}

func TestAssignUnownedTo(t *testing.T) {
	migrated(t)
	// Rows from before users have no user_id.
	if _, err := db.Exec(`INSERT INTO scans (scan_type, created_on, scan_start_time) VALUES ('local', now(), now());
		INSERT INTO privatetokens (access_token, refresh_token, display_name, client_key, created_on, scope, expires_in, token_type)
			VALUES ('at', 'rt', 'jy***ri@example.com', 'k1', now(), '', 0, 'Bearer')`); err != nil {
		t.Fatal(err)
	}
	unowned := func() (n int) {
		t.Helper()
		if err := db.Get(&n, `SELECT (SELECT count(*) FROM scans WHERE user_id IS NULL)
			+ (SELECT count(*) FROM privatetokens WHERE user_id IS NULL)`); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// The owner doesn't exist yet: nothing changes, and it's not an error.
	if err := AssignUnownedTo("jyothri"); err != nil {
		t.Fatal(err)
	}
	if n := unowned(); n != 2 {
		t.Fatalf("unowned rows = %d, want 2", n)
	}

	id := addUser(t, "jyothri")
	if err := AssignUnownedTo("jyothri"); err != nil {
		t.Fatal(err)
	}
	if n := unowned(); n != 0 {
		t.Fatalf("unowned rows = %d, want 0", n)
	}
	scans, count, err := GetScansFromDb(id, 1)
	if err != nil || count != 1 || len(scans) != 1 {
		t.Fatalf("GetScansFromDb(jyothri) = %d scans, count %d, %v; want 1", len(scans), count, err)
	}
	if _, err := GetOAuthToken(id, "k1"); err != nil {
		t.Fatalf("GetOAuthToken(jyothri, k1): %v", err)
	}
}

func TestScansAndAccountsBelongToTheirUser(t *testing.T) {
	migrated(t)
	alice, bob := addUser(t, "alice"), addUser(t, "bob")
	scanId, err := LogStartScan("local", alice)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveOAuthToken(alice, "at", "rt", "al***ce@example.com", "key-a", "", 0, "Bearer"); err != nil {
		t.Fatal(err)
	}

	if owned, err := ScanOwnedBy(scanId, alice); err != nil || !owned {
		t.Errorf("ScanOwnedBy(alice) = %v, %v; want true", owned, err)
	}
	if owned, err := ScanOwnedBy(scanId, bob); err != nil || owned {
		t.Errorf("ScanOwnedBy(bob) = %v, %v; want false", owned, err)
	}
	if _, count, _ := GetScansFromDb(bob, 1); count != 0 {
		t.Errorf("bob sees %d scans, want 0", count)
	}
	if _, err := GetOAuthToken(bob, "key-a"); err == nil {
		t.Error("bob can use alice's linked account")
	}
	if accounts, _ := GetRequestAccountsFromDb(bob); len(accounts) != 0 {
		t.Errorf("bob sees accounts %v, want none", accounts)
	}
	if accounts, _ := GetRequestAccountsFromDb(alice); len(accounts) != 1 {
		t.Errorf("alice sees accounts %v, want one", accounts)
	}
}

func TestSessions(t *testing.T) {
	migrated(t)
	id := addUser(t, "alice")
	if err := CreateSession(id, "token-1"); err != nil {
		t.Fatal(err)
	}
	u, err := SessionUser("token-1")
	if err != nil || u.ID != id || u.Username != "alice" {
		t.Fatalf("SessionUser = %+v, %v; want alice", u, err)
	}
	if _, err := SessionUser("token-2"); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("SessionUser(unknown) err = %v, want ErrNoSuchUser", err)
	}

	// agentserver's `user disable` ends web sessions at once.
	if _, err := db.Exec(`UPDATE agent_users SET disabled_at = now() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := SessionUser("token-1"); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("SessionUser(disabled user) err = %v, want ErrNoSuchUser", err)
	}
	if _, err := db.Exec(`UPDATE agent_users SET disabled_at = NULL WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}

	// Expired sessions don't work, and the next login deletes them.
	if _, err := db.Exec(`UPDATE web_sessions SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err := SessionUser("token-1"); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("SessionUser(expired) err = %v, want ErrNoSuchUser", err)
	}
	if err := CreateSession(id, "token-3"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Get(&n, `SELECT count(*) FROM web_sessions`); err != nil || n != 1 {
		t.Errorf("sessions after login = %d, %v; want 1", n, err)
	}

	if err := DeleteSession("token-3"); err != nil {
		t.Fatal(err)
	}
	if _, err := SessionUser("token-3"); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("SessionUser(logged out) err = %v, want ErrNoSuchUser", err)
	}
}

func TestLoginLockout(t *testing.T) {
	migrated(t)
	ip := netip.MustParseAddr("198.51.100.7")
	for i := 0; i < LockoutFailures-1; i++ {
		if err := RecordLoginFailure("alice", ip); err != nil {
			t.Fatal(err)
		}
	}
	if d, err := LoginLockedFor("alice", ip); err != nil || d != 0 {
		t.Fatalf("after %d failures: locked for %v, %v; want 0", LockoutFailures-1, d, err)
	}
	if err := RecordLoginFailure("alice", ip); err != nil {
		t.Fatal(err)
	}
	if d, err := LoginLockedFor("alice", ip); err != nil || d <= 0 {
		t.Fatalf("after %d failures: locked for %v, %v; want > 0", LockoutFailures, d, err)
	}
	if d, _ := LoginLockedFor("alice", netip.MustParseAddr("198.51.100.8")); d != 0 {
		t.Errorf("another IP is locked for %v, want 0", d)
	}
	if err := ClearLoginFailures("alice", ip); err != nil {
		t.Fatal(err)
	}
	if d, _ := LoginLockedFor("alice", ip); d != 0 {
		t.Errorf("after clearing: locked for %v, want 0", d)
	}
}
