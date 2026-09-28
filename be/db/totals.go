package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

// inQuery expands a query's "IN (?)" for a slice, for this database.
func inQuery(query string, args ...any) (string, []any, error) {
	q, a, err := sqlx.In(query, args...)
	if err != nil {
		return "", nil, err
	}
	return db.Rebind(q), a, nil
}

// Every folder's total size and file count, cached per source so that
// opening a folder never adds up its subtree. See docs/specs/browse.md,
// "Folder totals cache".

func migrateBrowseTotals() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS browse_folder_totals (
			source  TEXT NOT NULL,
			folder  TEXT NOT NULL,
			files   BIGINT NOT NULL,
			bytes   BIGINT NOT NULL,
			PRIMARY KEY (source, folder)
		)`,
		`CREATE TABLE IF NOT EXISTS browse_totals_state (
			source    TEXT PRIMARY KEY,
			version   BIGINT NOT NULL DEFAULT 0,
			built_at  TIMESTAMPTZ,
			building  BOOLEAN NOT NULL DEFAULT false
		)`,
		// A rebuild cut off by a restart is done again by the first check.
		`UPDATE browse_totals_state SET building = false WHERE building`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to add the folder totals cache to the schema: %w", err)
		}
	}
	return nil
}

func init() {
	afterDriveScan = func(clientKey string) {
		if err := rebuildDriveTotals(clientKey); err != nil {
			slog.Error("Failed to rebuild Drive folder totals", "client_key", clientKey, "error", err)
		}
	}
}

// rebuildMu keeps rebuilds one at a time: a scan's and the checker's could
// otherwise replace the same source's rows at once.
var rebuildMu sync.Mutex

func driveSource(clientKey string) string { return "drive:" + clientKey }
func agentSource(drivePk int64) string    { return "agent:" + strconv.FormatInt(drivePk, 10) }

// totalsState is what a source's cached totals were built from.
type totalsState struct {
	Version  int64 `db:"version"`
	Built    bool  `db:"built"`
	Building bool  `db:"building"`
}

// getTotalsState returns source's state; a source never built has none.
func getTotalsState(source string) (totalsState, error) {
	var s totalsState
	err := db.Get(&s, `SELECT version, built_at IS NOT NULL AS built, building
		FROM browse_totals_state WHERE source = $1`, source)
	if errors.Is(err, sql.ErrNoRows) {
		return totalsState{}, nil
	}
	return s, err
}

// rebuildTotals replaces source's cached totals with query's rows (folder,
// files, bytes; its arguments start at $2), in one transaction, and
// records version.
func rebuildTotals(source string, version int64, query string, args ...any) error {
	rebuildMu.Lock()
	defer rebuildMu.Unlock()
	if _, err := db.Exec(`INSERT INTO browse_totals_state (source, building) VALUES ($1, true)
		ON CONFLICT (source) DO UPDATE SET building = true`, source); err != nil {
		return fmt.Errorf("failed to mark totals of %s as building: %w", source, err)
	}
	err := func() error {
		tx, err := db.Beginx()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`DELETE FROM browse_folder_totals WHERE source = $1`, source); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO browse_folder_totals (source, folder, files, bytes)
			SELECT $1, folder, files, bytes FROM (`+query+`) t`, append([]any{source}, args...)...); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE browse_totals_state SET version = $2, built_at = now(), building = false
			WHERE source = $1`, source, version); err != nil {
			return err
		}
		return tx.Commit()
	}()
	if err != nil {
		db.Exec(`UPDATE browse_totals_state SET building = false WHERE source = $1`, source)
		return fmt.Errorf("failed to rebuild totals of %s: %w", source, err)
	}
	return nil
}

// agentTotalsQuery adds up drive $2's files in every folder above them,
// keyed by the folder's path (” for the drive's root).
const agentTotalsQuery = `SELECT array_to_string(f.parts[1:n], '/') AS folder, count(*) AS files,
		COALESCE(sum(f.size), 0) AS bytes
	FROM (SELECT string_to_array(relative_path, '/') AS parts, size FROM agent_files WHERE drive_pk = $2) f,
		generate_series(0, array_length(f.parts, 1) - 1) n
	GROUP BY 1`

// agentVersion is what an agent drive's totals are built from: its
// highest file row_version, which every upload raises (a deletion leaves a
// tombstone at its version).
func agentVersion(drivePk int64) (int64, error) {
	var v int64
	err := db.Get(&v, `SELECT GREATEST(
			(SELECT COALESCE(max(row_version), 0) FROM agent_files WHERE drive_pk = $1),
			(SELECT COALESCE(max(row_version), 0) FROM agent_tombstones WHERE drive_pk = $1 AND kind = 'file'))`,
		drivePk)
	return v, err
}

func rebuildAgentTotals(drivePk int64, version int64) error {
	start := time.Now()
	err := rebuildTotals(agentSource(drivePk), version, agentTotalsQuery, drivePk)
	if err == nil {
		slog.Info("Rebuilt folder totals", "source", agentSource(drivePk), "version", version, "took", time.Since(start))
	}
	return err
}

// driveTotalsQuery adds up account $2's files (not trashed) in every
// folder above them, keyed by folder ID, plus the whole account as ”.
const driveTotalsQuery = `WITH RECURSIVE up (folder, size, depth) AS (
		SELECT parent_id, size, 0 FROM drive_items
		WHERE client_key = $2 AND NOT is_dir AND NOT trashed AND parent_id IS NOT NULL
	UNION ALL
		SELECT p.parent_id, u.size, u.depth + 1 FROM up u
		JOIN drive_items p ON p.client_key = $2 AND p.file_id = u.folder
		WHERE p.parent_id IS NOT NULL AND u.depth < 100
	)
	SELECT folder, count(*) AS files, COALESCE(sum(size), 0) AS bytes FROM up GROUP BY folder
	UNION ALL
	SELECT '', count(*), COALESCE(sum(size), 0) FROM drive_items
	WHERE client_key = $2 AND NOT is_dir AND NOT trashed`

func rebuildDriveTotals(clientKey string) error {
	return rebuildTotals(driveSource(clientKey), 0, driveTotalsQuery, clientKey)
}

// FolderTotals is the files under a folder, at any depth, and their size.
type FolderTotals struct {
	Files int64 `db:"files" json:"files"`
	Bytes int64 `db:"bytes" json:"bytes"`
}

// cachedTotals returns source's cached totals of folders.
func cachedTotals(source string, folders []string) (map[string]FolderTotals, error) {
	totals := map[string]FolderTotals{}
	if len(folders) == 0 {
		return totals, nil
	}
	rows := []struct {
		Folder string `db:"folder"`
		FolderTotals
	}{}
	query, args, err := inQuery(`SELECT folder, files, bytes FROM browse_folder_totals
		WHERE source = ? AND folder IN (?)`, source, folders)
	if err != nil {
		return nil, err
	}
	if err := db.Select(&rows, query, args...); err != nil {
		return nil, fmt.Errorf("failed to read folder totals of %s: %w", source, err)
	}
	for _, r := range rows {
		totals[r.Folder] = r.FolderTotals
	}
	return totals, nil
}

// TotalsChecker rebuilds, every interval, the totals of each agent drive
// whose version changed, one drive at a time, and of any Drive account
// never built. The first check runs at once.
func TotalsChecker(ctx context.Context, interval time.Duration) {
	for {
		checkTotals(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func checkTotals(ctx context.Context) {
	var drives []int64
	if err := db.Select(&drives, `SELECT id FROM agent_drives ORDER BY id`); err != nil {
		slog.Error("Failed to list agent drives for folder totals", "error", err)
	}
	for _, pk := range drives {
		if ctx.Err() != nil {
			return
		}
		version, err := agentVersion(pk)
		if err != nil {
			slog.Error("Failed to check an agent drive's version", "drive", pk, "error", err)
			continue
		}
		state, err := getTotalsState(agentSource(pk))
		if err != nil {
			slog.Error("Failed to read folder totals state", "drive", pk, "error", err)
			continue
		}
		if state.Built && state.Version == version {
			continue
		}
		if err := rebuildAgentTotals(pk, version); err != nil {
			slog.Error("Failed to rebuild agent drive folder totals", "drive", pk, "error", err)
		}
	}
	var accounts []string
	if err := db.Select(&accounts, `SELECT a.client_key FROM drive_accounts a
		WHERE NOT EXISTS (SELECT 1 FROM browse_totals_state s
			WHERE s.source = 'drive:' || a.client_key AND s.built_at IS NOT NULL)`); err != nil {
		slog.Error("Failed to list Drive accounts for folder totals", "error", err)
	}
	for _, clientKey := range accounts {
		if err := rebuildDriveTotals(clientKey); err != nil {
			slog.Error("Failed to rebuild Drive folder totals", "client_key", clientKey, "error", err)
		}
	}
}
