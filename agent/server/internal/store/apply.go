package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jyothri/bhandaar/agent/wire"
)

// Errors from ApplyChanges.
var (
	ErrIdempotencyKeyReused = errors.New("idempotency key reused with a different request")
)

// StreamMismatchError means the batch is for another stream than the
// drive's current one; the agent must re-open the drive.
type StreamMismatchError struct{ StreamID string }

func (e *StreamMismatchError) Error() string {
	return "stream mismatch; the drive's stream is " + e.StreamID
}

// ChangesRequest is one change batch, as received.
type ChangesRequest struct {
	UserID         int64
	AgentID        string
	DriveID        string
	IdempotencyKey string
	RequestSHA256  []byte // of the decompressed body
	Batch          wire.ChangeBatch
}

// applyHook lets tests inject a failure before a group's bulk statement.
var applyHook func(group string) error

// ApplyChanges applies one change batch in one transaction, following the
// server spec's apply algorithm:
//
//  1. lock the drive row (ErrDriveNotOpen if there is none);
//  2. a different stream: *StreamMismatchError;
//  3. a batch whose range is already covered by an acked range is a
//     duplicate: answered with the current ranges, before the key lookup (a
//     restarted agent may send the same range with different contents);
//  4. a known idempotency key: the stored response, or
//     ErrIdempotencyKeyReused if the request differs;
//  5. validate each entry; invalid ones are rejected, and a rejected
//     file/dir_child whose key is known becomes a delete at its version, so
//     the server shows the key as missing rather than stale;
//  6. apply every entry not inside an acked range, where its version is
//     above the key's current version (row or tombstone), one bulk
//     statement per group under a savepoint, falling back to one savepoint
//     per entry on a data error (SQLSTATE class 22/23);
//  7. merge the batch's range into the acked ranges and update the
//     watermark;
//  8. store the idempotency record.
func (s *Store) ApplyChanges(ctx context.Context, req ChangesRequest) (wire.ChangesResponse, error) {
	var resp wire.ChangesResponse
	b := req.Batch
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var pk int64
		var stream string
		err := tx.QueryRow(ctx, `SELECT id, stream_id::text FROM agent_drives WHERE agent_id = $1 AND drive_id = $2 FOR UPDATE`,
			req.AgentID, req.DriveID).Scan(&pk, &stream)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDriveNotOpen
		}
		if err != nil {
			return err
		}
		if !equalFoldUUID(stream, b.StreamID) {
			return &StreamMismatchError{StreamID: stream}
		}

		ranges, err := loadRanges(ctx, tx, pk)
		if err != nil {
			return err
		}
		if covered(ranges, b.FromVersion, b.ToVersion) {
			resp = wire.ChangesResponse{AckedRanges: ranges, Skipped: len(b.Changes), Duplicate: true, Rejected: []wire.Rejected{}}
			return nil
		}

		var storedHash, storedBody []byte
		err = tx.QueryRow(ctx, `SELECT request_sha256, response_body FROM agent_idempotency_keys WHERE user_id = $1 AND key = $2`,
			req.UserID, req.IdempotencyKey).Scan(&storedHash, &storedBody)
		switch {
		case err == nil && bytes.Equal(storedHash, req.RequestSHA256):
			return json.Unmarshal(storedBody, &resp)
		case err == nil:
			return ErrIdempotencyKeyReused
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}

		if resp, err = applyEntries(ctx, tx, pk, ranges, b); err != nil {
			return err
		}

		merged := mergeRange(ranges, wire.Range{b.FromVersion, b.ToVersion})
		if _, err := tx.Exec(ctx, `DELETE FROM agent_sync_ranges WHERE drive_pk = $1`, pk); err != nil {
			return err
		}
		froms, tos := make([]int64, len(merged)), make([]int64, len(merged))
		for i, r := range merged {
			froms[i], tos[i] = r[0], r[1]
		}
		if _, err := tx.Exec(ctx, `INSERT INTO agent_sync_ranges (drive_pk, from_version, to_version)
			SELECT $1, f, t FROM unnest($2::int8[], $3::int8[]) AS u(f, t)`, pk, froms, tos); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE agent_drives SET acked_version = $2, last_synced_at = now() WHERE id = $1`,
			pk, watermark(merged)); err != nil {
			return err
		}
		resp.AckedRanges = merged

		body, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO agent_idempotency_keys (user_id, key, request_sha256, status_code, response_body)
			VALUES ($1, $2, $3, 200, $4)`, req.UserID, req.IdempotencyKey, req.RequestSHA256, body)
		return err
	})
	return resp, err
}

func equalFoldUUID(a, b string) bool { return bytes.EqualFold([]byte(a), []byte(b)) }

// covered reports whether (from, to] lies inside one acked range.
func covered(ranges []wire.Range, from, to int64) bool {
	for _, r := range ranges {
		if r[0] <= from && to <= r[1] {
			return true
		}
	}
	return false
}

func inAcked(ranges []wire.Range, v int64) bool {
	for _, r := range ranges {
		if r[0] < v && v <= r[1] {
			return true
		}
	}
	return false
}

// mergeRange adds add to sorted, non-overlapping ranges, merging every
// range it overlaps or touches.
func mergeRange(ranges []wire.Range, add wire.Range) []wire.Range {
	all := append(append([]wire.Range{}, ranges...), add)
	sort.Slice(all, func(i, j int) bool { return all[i][0] < all[j][0] })
	out := []wire.Range{}
	for _, r := range all {
		if n := len(out); n > 0 && r[0] <= out[n-1][1] {
			out[n-1][1] = max(out[n-1][1], r[1])
			continue
		}
		out = append(out, r)
	}
	return out
}

// watermark is the end of the range that starts at 0, if any.
func watermark(ranges []wire.Range) int64 {
	if len(ranges) > 0 && ranges[0][0] == 0 {
		return ranges[0][1]
	}
	return 0
}

// isDataError reports whether err is a problem with the data (SQLSTATE
// class 22, data exception, or 23, integrity constraint violation). Only
// those fall back to per-entry application; anything else (a lost
// connection, a serialization failure) fails the request.
func isDataError(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && len(pg.Code) == 5 && (pg.Code[:2] == "22" || pg.Code[:2] == "23")
}

// applyEntries does steps 5 and 6.
func applyEntries(ctx context.Context, tx pgx.Tx, pk int64, ranges []wire.Range, b wire.ChangeBatch) (wire.ChangesResponse, error) {
	resp := wire.ChangesResponse{Rejected: []wire.Rejected{}}
	type groupKey struct {
		kind string
		key  string // hex of the key, or the run id
	}
	latest := map[groupKey]*entry{}
	for _, c := range b.Changes {
		e := prepare(c)
		if e.reason != "" {
			resp.Rejected = append(resp.Rejected, wire.Rejected{V: e.v, Reason: e.reason})
			if e.key == nil || e.kind == wire.KindScanRun {
				continue
			}
			// Apply it as a delete, so the key's older row doesn't linger.
			e.op, e.fromReject = wire.OpDelete, true
		}
		if inAcked(ranges, e.v) {
			if !e.fromReject {
				resp.Skipped++
			}
			continue
		}
		k := groupKey{kind: e.kind, key: string(e.key)}
		if e.kind == wire.KindScanRun {
			k.key = fmt.Sprint(e.runID)
		}
		// A key appears once in a well-formed batch; if not, the highest
		// version wins and the rest are skipped.
		if prev, ok := latest[k]; ok {
			if prev.v >= e.v {
				if !e.fromReject {
					resp.Skipped++
				}
				continue
			}
			if !prev.fromReject {
				resp.Skipped++
			}
		}
		latest[k] = e
	}

	// Deletes that stand in for rejected entries (rejDel) aren't counted
	// as applied: the entry is already in rejected.
	var fileUp, fileDel, dirUp, dirDel, runUp, rejFileDel, rejDirDel []*entry
	for _, e := range latest {
		switch {
		case e.fromReject && e.kind == wire.KindFile:
			rejFileDel = append(rejFileDel, e)
		case e.fromReject:
			rejDirDel = append(rejDirDel, e)
		case e.kind == wire.KindFile && e.op == wire.OpUpsert:
			fileUp = append(fileUp, e)
		case e.kind == wire.KindFile:
			fileDel = append(fileDel, e)
		case e.kind == wire.KindDirChild && e.op == wire.OpUpsert:
			dirUp = append(dirUp, e)
		case e.kind == wire.KindDirChild:
			dirDel = append(dirDel, e)
		default:
			runUp = append(runUp, e)
		}
	}

	var applied int64
	var failedUpserts []*entry
	for _, g := range []struct {
		name    string
		entries []*entry
		exec    func(context.Context, pgx.Tx, int64, []*entry) (int64, error)
		upsert  bool
	}{
		{"file_upserts", fileUp, execFileUpserts, true},
		{"dir_upserts", dirUp, execDirUpserts, true},
		{"run_upserts", runUp, execRunUpserts, false},
	} {
		n, failed, err := applyGroup(ctx, tx, pk, g.name, g.entries, g.exec)
		if err != nil {
			return resp, err
		}
		applied += n
		for _, f := range failed {
			resp.Rejected = append(resp.Rejected, wire.Rejected{V: f.e.v, Reason: f.reason})
			if g.upsert {
				f.e.op, f.e.fromReject = wire.OpDelete, true
				failedUpserts = append(failedUpserts, f.e)
			}
		}
	}
	for _, e := range failedUpserts {
		if e.kind == wire.KindFile {
			rejFileDel = append(rejFileDel, e)
		} else {
			rejDirDel = append(rejDirDel, e)
		}
	}
	for _, g := range []struct {
		name    string
		entries []*entry
		exec    func(context.Context, pgx.Tx, int64, []*entry) (int64, error)
		counted bool
	}{
		{"file_deletes", fileDel, execFileDeletes, true},
		{"dir_deletes", dirDel, execDirDeletes, true},
		{"rejected_file_deletes", rejFileDel, execFileDeletes, false},
		{"rejected_dir_deletes", rejDirDel, execDirDeletes, false},
	} {
		n, failed, err := applyGroup(ctx, tx, pk, g.name, g.entries, g.exec)
		if err != nil {
			return resp, err
		}
		if g.counted {
			applied += n
			for _, f := range failed {
				resp.Rejected = append(resp.Rejected, wire.Rejected{V: f.e.v, Reason: f.reason})
			}
		}
	}

	// Every entry is applied, skipped or rejected. Rejected entries turned
	// into deletes aren't counted again; entries that lost to a newer
	// version on the server count as skipped.
	resp.Applied = int(applied)
	accounted := resp.Applied + resp.Skipped + len(resp.Rejected)
	if extra := len(b.Changes) - accounted; extra > 0 {
		resp.Skipped += extra
	}
	sort.Slice(resp.Rejected, func(i, j int) bool { return resp.Rejected[i].V < resp.Rejected[j].V })
	return resp, nil
}

type failure struct {
	e      *entry
	reason string
}

// applyGroup runs one group's bulk statement under a savepoint. On a data
// error it rolls back to the savepoint and applies the entries one by one,
// each under its own savepoint, returning those that still fail.
func applyGroup(ctx context.Context, tx pgx.Tx, pk int64, name string, entries []*entry,
	exec func(context.Context, pgx.Tx, int64, []*entry) (int64, error)) (int64, []failure, error) {
	if len(entries) == 0 {
		return 0, nil, nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].v < entries[j].v })
	run := func(es []*entry) (int64, error) {
		sp, err := tx.Begin(ctx) // a savepoint
		if err != nil {
			return 0, err
		}
		n, err := exec(ctx, sp, pk, es)
		if err != nil {
			_ = sp.Rollback(ctx)
			return 0, err
		}
		return n, sp.Commit(ctx)
	}
	if applyHook != nil {
		if err := applyHook(name); err != nil {
			return 0, nil, err
		}
	}
	n, err := run(entries)
	if err == nil {
		return n, nil, nil
	}
	if !isDataError(err) {
		return 0, nil, fmt.Errorf("%s: %w", name, err)
	}
	var total int64
	var failed []failure
	for _, e := range entries {
		n, err := run([]*entry{e})
		switch {
		case err == nil:
			total += n
		case isDataError(err):
			failed = append(failed, failure{e: e, reason: "rejected by the database: " + err.Error()})
		default:
			return 0, nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	return total, failed, nil
}

func execFileUpserts(ctx context.Context, tx pgx.Tx, pk int64, es []*entry) (int64, error) {
	n := len(es)
	keys, raws := make([][]byte, n), make([][]byte, n)
	paths, hashes, algos, statuses, msgs := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	sizes, mtimes, vs := make([]int64, n), make([]int64, n), make([]int64, n)
	modes := make([]int32, n)
	scanned := make([]time.Time, n)
	for i, e := range es {
		c := e.change
		keys[i], raws[i], paths[i] = e.key, e.rawPath, e.path
		sizes[i], mtimes[i], modes[i] = *c.Size, *c.MTimeUnix, int32(*c.Mode)
		hashes[i], algos[i], statuses[i], msgs[i] = c.ContentHash, c.HashAlgo, c.Status, c.ErrorMessage
		scanned[i], vs[i] = *c.ScannedAt, e.v
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO agent_files AS f (drive_pk, path_key, relative_path, raw_path, size, mtime, mode,
		                              content_hash, hash_algo, status, error_message, scanned_at, row_version)
		SELECT $1, u.k, u.p, u.raw, u.size, to_timestamp(u.mtime::float8), u.mode,
		       nullif(u.ch, ''), nullif(u.ha, ''), u.st, nullif(u.em, ''), u.sa, u.v
		  FROM unnest($2::bytea[], $3::text[], $4::bytea[], $5::int8[], $6::int8[], $7::int4[],
		              $8::text[], $9::text[], $10::text[], $11::text[], $12::timestamptz[], $13::int8[])
		       AS u(k, p, raw, size, mtime, mode, ch, ha, st, em, sa, v)
		 WHERE NOT EXISTS (SELECT 1 FROM agent_tombstones t
		                    WHERE t.drive_pk = $1 AND t.kind = 'file' AND t.key = u.k AND t.row_version >= u.v)
		ON CONFLICT (drive_pk, path_key) DO UPDATE SET
		       relative_path = EXCLUDED.relative_path, raw_path = EXCLUDED.raw_path, size = EXCLUDED.size,
		       mtime = EXCLUDED.mtime, mode = EXCLUDED.mode, content_hash = EXCLUDED.content_hash,
		       hash_algo = EXCLUDED.hash_algo, status = EXCLUDED.status, error_message = EXCLUDED.error_message,
		       scanned_at = EXCLUDED.scanned_at, row_version = EXCLUDED.row_version
		 WHERE f.row_version < EXCLUDED.row_version`,
		pk, keys, paths, raws, sizes, mtimes, modes, hashes, algos, statuses, msgs, scanned, vs)
	if err != nil {
		return 0, err
	}
	// A row that now supersedes a tombstone removes it.
	if _, err := tx.Exec(ctx, `
		DELETE FROM agent_tombstones t USING unnest($2::bytea[], $3::int8[]) AS u(k, v)
		 WHERE t.drive_pk = $1 AND t.kind = 'file' AND t.key = u.k AND t.row_version < u.v`, pk, keys, vs); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func execDirUpserts(ctx context.Context, tx pgx.Tx, pk int64, es []*entry) (int64, error) {
	n := len(es)
	keys, parents, raws, rawChildren := make([][]byte, n), make([][]byte, n), make([][]byte, n), make([][]byte, n)
	paths, children := make([]string, n), make([]string, n)
	dirs := make([]bool, n)
	seen := make([]time.Time, n)
	vs := make([]int64, n)
	for i, e := range es {
		keys[i], parents[i], raws[i], rawChildren[i] = e.key, e.parentKey, e.rawPath, e.rawChild
		paths[i], children[i], dirs[i], seen[i], vs[i] = e.path, e.child, *e.change.IsDir, *e.change.FirstSeenAt, e.v
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO agent_dir_listings AS d (drive_pk, entry_key, parent_key, relative_path, child_name,
		                                     raw_path, raw_child_name, is_dir, first_seen_at, row_version)
		SELECT $1, u.k, u.pk, u.p, u.c, u.rp, u.rc, u.isdir, u.fs, u.v
		  FROM unnest($2::bytea[], $3::bytea[], $4::text[], $5::text[], $6::bytea[], $7::bytea[], $8::bool[],
		              $9::timestamptz[], $10::int8[]) AS u(k, pk, p, c, rp, rc, isdir, fs, v)
		 WHERE NOT EXISTS (SELECT 1 FROM agent_tombstones t
		                    WHERE t.drive_pk = $1 AND t.kind = 'dir_child' AND t.key = u.k AND t.row_version >= u.v)
		ON CONFLICT (drive_pk, entry_key) DO UPDATE SET
		       relative_path = EXCLUDED.relative_path, child_name = EXCLUDED.child_name, raw_path = EXCLUDED.raw_path,
		       raw_child_name = EXCLUDED.raw_child_name, is_dir = EXCLUDED.is_dir,
		       first_seen_at = EXCLUDED.first_seen_at, row_version = EXCLUDED.row_version
		 WHERE d.row_version < EXCLUDED.row_version`,
		pk, keys, parents, paths, children, raws, rawChildren, dirs, seen, vs)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM agent_tombstones t USING unnest($2::bytea[], $3::int8[]) AS u(k, v)
		 WHERE t.drive_pk = $1 AND t.kind = 'dir_child' AND t.key = u.k AND t.row_version < u.v`, pk, keys, vs); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func execRunUpserts(ctx context.Context, tx pgx.Tx, pk int64, es []*entry) (int64, error) {
	n := len(es)
	ids, vs := make([]int64, n), make([]int64, n)
	started := make([]time.Time, n)
	finished := make([]*time.Time, n)
	seen, hashed := make([]*int64, n), make([]*int64, n)
	interrupted := make([]*bool, n)
	for i, e := range es {
		c := e.change
		ids[i], vs[i], started[i] = e.runID, e.v, *c.StartedAt
		finished[i], seen[i], hashed[i], interrupted[i] = c.FinishedAt, c.FilesSeen, c.BytesHashed, c.Interrupted
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO agent_scan_runs AS r (drive_pk, run_id, started_at, finished_at, files_seen, bytes_hashed, interrupted, row_version)
		SELECT $1, u.id, u.sa, u.fa, u.fs, u.bh, u.i, u.v
		  FROM unnest($2::int8[], $3::timestamptz[], $4::timestamptz[], $5::int8[], $6::int8[], $7::bool[], $8::int8[])
		       AS u(id, sa, fa, fs, bh, i, v)
		ON CONFLICT (drive_pk, run_id) DO UPDATE SET
		       started_at = EXCLUDED.started_at, finished_at = EXCLUDED.finished_at, files_seen = EXCLUDED.files_seen,
		       bytes_hashed = EXCLUDED.bytes_hashed, interrupted = EXCLUDED.interrupted, row_version = EXCLUDED.row_version
		 WHERE r.row_version < EXCLUDED.row_version`,
		pk, ids, started, finished, seen, hashed, interrupted, vs)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// execDeletes removes the rows of table whose key is older than the delete,
// and records a tombstone unless a newer row exists.
func execDeletes(ctx context.Context, tx pgx.Tx, pk int64, es []*entry, table, keyCol, kind string) (int64, error) {
	keys, vs := make([][]byte, len(es)), make([]int64, len(es))
	for i, e := range es {
		keys[i], vs[i] = e.key, e.v
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM `+table+` x USING unnest($2::bytea[], $3::int8[]) AS u(k, v)
		 WHERE x.drive_pk = $1 AND x.`+keyCol+` = u.k AND x.row_version < u.v`, pk, keys, vs); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO agent_tombstones AS t (drive_pk, kind, key, row_version)
		SELECT $1, $4, u.k, u.v FROM unnest($2::bytea[], $3::int8[]) AS u(k, v)
		 WHERE NOT EXISTS (SELECT 1 FROM `+table+` x WHERE x.drive_pk = $1 AND x.`+keyCol+` = u.k AND x.row_version >= u.v)
		ON CONFLICT (drive_pk, kind, key) DO UPDATE SET row_version = EXCLUDED.row_version
		 WHERE t.row_version < EXCLUDED.row_version`, pk, keys, vs, kind)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func execFileDeletes(ctx context.Context, tx pgx.Tx, pk int64, es []*entry) (int64, error) {
	return execDeletes(ctx, tx, pk, es, "agent_files", "path_key", wire.KindFile)
}

func execDirDeletes(ctx context.Context, tx pgx.Tx, pk int64, es []*entry) (int64, error) {
	return execDeletes(ctx, tx, pk, es, "agent_dir_listings", "entry_key", wire.KindDirChild)
}
