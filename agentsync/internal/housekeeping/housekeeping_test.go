package housekeeping_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jyothri/bhandaar/agentsync/internal/housekeeping"
	"github.com/jyothri/bhandaar/agentsync/internal/testdb"
)

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func count(t *testing.T, pool *pgxpool.Pool, sql string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// seed adds a user and an agent, and returns the user id.
func seed(t *testing.T, pool *pgxpool.Pool) {
	exec(t, pool, "INSERT INTO agent_users (id, username, password_hash) VALUES (1, 'u', 'h')")
	exec(t, pool, "INSERT INTO agent_agents (id, user_id) VALUES ('00000000-0000-4000-8000-000000000001', 1)")
}

func task(t *testing.T, pool *pgxpool.Pool, name string) housekeeping.Task {
	for _, tk := range housekeeping.Tasks(pool) {
		if tk.Name == name {
			return tk
		}
	}
	t.Fatalf("no task %s", name)
	return housekeeping.Task{}
}

func TestCutoffs(t *testing.T) {
	pool := testdb.New(t).Pool
	seed(t, pool)
	// One row just past each cutoff, one just inside it.
	exec(t, pool, `INSERT INTO agent_idempotency_keys (user_id, key, request_sha256, status_code, response_body, created_at) VALUES
		(1, 'old', '\x00', 200, '\x00', now() - interval '7 days 1 minute'),
		(1, 'new', '\x00', 200, '\x00', now() - interval '6 days 23 hours')`)
	exec(t, pool, `INSERT INTO agent_login_failures (username, client_ip, failed_at) VALUES
		('u', '198.51.100.1', now() - interval '1 day 1 minute'),
		('u', '198.51.100.1', now() - interval '23 hours')`)
	exec(t, pool, `INSERT INTO agent_refresh_tokens (token_hash, family_id, user_id, agent_id, issued_at, expires_at) VALUES
		('\x01', gen_random_uuid(), 1, '00000000-0000-4000-8000-000000000001', now() - interval '40 days', now() - interval '7 days 1 minute'),
		('\x02', gen_random_uuid(), 1, '00000000-0000-4000-8000-000000000001', now() - interval '37 days', now() - interval '6 days 23 hours'),
		('\x03', gen_random_uuid(), 1, '00000000-0000-4000-8000-000000000001', now(), now() + interval '30 days')`)

	for name, want := range map[string]int64{"idempotency_keys": 1, "login_failures": 1, "refresh_tokens": 1} {
		n, err := task(t, pool, name).Run(context.Background())
		if err != nil || n != want {
			t.Errorf("%s: deleted %d, %v; want %d", name, n, err, want)
		}
	}
	if n := count(t, pool, "SELECT count(*) FROM agent_idempotency_keys WHERE key = 'new'"); n != 1 {
		t.Error("idempotency key inside the cutoff was deleted")
	}
	if n := count(t, pool, "SELECT count(*) FROM agent_login_failures"); n != 1 {
		t.Errorf("%d login failures left, want 1", n)
	}
	if n := count(t, pool, "SELECT count(*) FROM agent_refresh_tokens"); n != 2 {
		t.Errorf("%d refresh tokens left, want 2", n)
	}
}

func TestDeletesInSeveralBatches(t *testing.T) {
	pool := testdb.New(t).Pool
	const rows = 2*housekeeping.BatchSize + 17
	exec(t, pool, `INSERT INTO agent_login_failures (username, client_ip, failed_at)
		SELECT 'u' || g, '198.51.100.1', now() - interval '2 days' FROM generate_series(1, $1) g`, rows)
	exec(t, pool, `INSERT INTO agent_login_failures (username, client_ip, failed_at) VALUES ('keep', '198.51.100.1', now())`)
	n, err := task(t, pool, "login_failures").Run(context.Background())
	if err != nil || n != rows {
		t.Fatalf("deleted %d, %v; want %d", n, err, rows)
	}
	if left := count(t, pool, "SELECT count(*) FROM agent_login_failures"); left != 1 {
		t.Errorf("%d rows left, want 1", left)
	}
}

// A refresh token can be deleted while the token that replaced it stays.
func TestRefreshTokenParentDeleted(t *testing.T) {
	pool := testdb.New(t).Pool
	seed(t, pool)
	exec(t, pool, `INSERT INTO agent_refresh_tokens (id, token_hash, family_id, user_id, agent_id, issued_at, expires_at, rotated_at) VALUES
		(1, '\x01', '00000000-0000-4000-8000-0000000000aa', 1, '00000000-0000-4000-8000-000000000001', now() - interval '40 days', now() - interval '10 days', now() - interval '39 days')`)
	exec(t, pool, `INSERT INTO agent_refresh_tokens (id, token_hash, family_id, parent_id, user_id, agent_id, issued_at, expires_at) VALUES
		(2, '\x02', '00000000-0000-4000-8000-0000000000aa', 1, 1, '00000000-0000-4000-8000-000000000001', now() - interval '39 days', now() + interval '1 day')`)
	if n, err := task(t, pool, "refresh_tokens").Run(context.Background()); err != nil || n != 1 {
		t.Fatalf("deleted %d, %v", n, err)
	}
	if n := count(t, pool, "SELECT count(*) FROM agent_refresh_tokens WHERE id = 2 AND parent_id IS NULL"); n != 1 {
		t.Error("the successor should stay, with parent_id cleared")
	}
}

func TestFailingTaskDoesNotStopOthers(t *testing.T) {
	var ran []string
	tasks := []housekeeping.Task{
		{Name: "a", Run: func(context.Context) (int64, error) { ran = append(ran, "a"); return 0, errors.New("boom") }},
		{Name: "b", Run: func(context.Context) (int64, error) { ran = append(ran, "b"); return 3, nil }},
	}
	if failed := housekeeping.RunOnce(context.Background(), tasks); failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
	if len(ran) != 2 {
		t.Errorf("ran %v, want both tasks", ran)
	}
}
