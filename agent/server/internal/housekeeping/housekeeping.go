// Package housekeeping deletes rows agentserver no longer needs. The server
// runs it hourly; "agentserver housekeeping" runs it once.
package housekeeping

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Retention periods and the batch size are constants, not configuration.
const (
	IdempotencyKeyRetention = 7 * 24 * time.Hour
	LoginFailureRetention   = 24 * time.Hour
	RefreshTokenRetention   = 7 * 24 * time.Hour // after expiry
	BatchSize               = 5000

	FirstRunDelay = 5 * time.Minute
	Interval      = time.Hour
)

// Task deletes one kind of expired row, returning how many it deleted.
type Task struct {
	Name string
	Run  func(ctx context.Context) (int64, error)
}

// Tasks returns the housekeeping tasks, in the order they run.
func Tasks(pool *pgxpool.Pool) []Task {
	return []Task{
		{"idempotency_keys", func(ctx context.Context) (int64, error) {
			return deleteInBatches(ctx, pool, `
				DELETE FROM agent_idempotency_keys
				 WHERE ctid IN (SELECT ctid FROM agent_idempotency_keys
				                 WHERE created_at < now() - make_interval(secs => $1) LIMIT $2)`,
				IdempotencyKeyRetention.Seconds())
		}},
		{"login_failures", func(ctx context.Context) (int64, error) {
			return deleteInBatches(ctx, pool, `
				DELETE FROM agent_login_failures
				 WHERE ctid IN (SELECT ctid FROM agent_login_failures
				                 WHERE failed_at < now() - make_interval(secs => $1) LIMIT $2)`,
				LoginFailureRetention.Seconds())
		}},
		{"tombstones", func(ctx context.Context) (int64, error) {
			// A tombstone at or below its drive's watermark protects nothing:
			// every older change for its key is skipped on arrival. The
			// watermark only rises, so this is safe during uploads.
			rows, err := pool.Query(ctx, `SELECT id FROM agent_drives WHERE acked_version > 0 ORDER BY id`)
			if err != nil {
				return 0, err
			}
			drives, err := pgx.CollectRows(rows, pgx.RowTo[int64])
			if err != nil {
				return 0, err
			}
			var total int64
			for _, pk := range drives {
				n, err := deleteInBatches(ctx, pool, `
					DELETE FROM agent_tombstones
					 WHERE ctid IN (SELECT t.ctid FROM agent_tombstones t
					                 WHERE t.drive_pk = $1
					                   AND t.row_version <= (SELECT acked_version FROM agent_drives WHERE id = $1)
					                 LIMIT $2)`, pk)
				total += n
				if err != nil {
					return total, err
				}
			}
			return total, nil
		}},
		{"refresh_tokens", func(ctx context.Context) (int64, error) {
			return deleteInBatches(ctx, pool, `
				DELETE FROM agent_refresh_tokens
				 WHERE ctid IN (SELECT ctid FROM agent_refresh_tokens
				                 WHERE expires_at < now() - make_interval(secs => $1) LIMIT $2)`,
				RefreshTokenRetention.Seconds())
		}},
	}
}

// deleteInBatches runs a DELETE (whose last parameter is the batch size)
// until a batch deletes fewer than BatchSize rows. Each batch is its own
// short transaction, so no delete holds locks long enough to slow an upload.
func deleteInBatches(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) (int64, error) {
	var total int64
	args = append(args, BatchSize)
	for {
		tag, err := pool.Exec(ctx, sql, args...)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < BatchSize {
			return total, nil
		}
	}
}

// RunOnce runs every task, one after another, logging a line for each. A
// failing task doesn't stop the others; it's retried on the next run. It
// returns the number of tasks that failed.
func RunOnce(ctx context.Context, tasks []Task) int {
	failed := 0
	for _, t := range tasks {
		start := time.Now()
		n, err := t.Run(ctx)
		took := time.Since(start).Round(time.Millisecond)
		if err != nil {
			failed++
			slog.Error("housekeeping", "task", t.Name, "deleted", n, "took", took, "error", err)
			continue
		}
		slog.Info("housekeeping", "task", t.Name, "deleted", n, "took", took)
	}
	return failed
}

// Loop runs the tasks FirstRunDelay after it starts, then every Interval,
// until ctx is done.
func Loop(ctx context.Context, tasks []Task) {
	timer := time.NewTimer(FirstRunDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	RunOnce(ctx, tasks)

	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			RunOnce(ctx, tasks)
		}
	}
}
