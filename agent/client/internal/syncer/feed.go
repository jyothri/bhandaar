// Package syncer uploads the change feed in state.db to agentserver: upload
// sessions, batches, the synced marker's bookkeeping and the uploader's
// state machine (docs/specs/remote-sync-agent.md, "Synced marker",
// "Reading the feed" and "Uploader").
package syncer

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/jyothri/bhandaar/agent/client/internal/store"
	"github.com/jyothri/bhandaar/agent/wire"
)

// Feed reads the change feed through its own read-only connection, so a
// page read never blocks the store's writer (WAL lets readers run beside
// it). Each page is one read transaction: a consistent snapshot.
type Feed struct {
	db *sql.DB
}

// OpenFeed opens stateDir's state.db read-only. The store must have opened
// (and migrated) it already.
func OpenFeed(stateDir string) (*Feed, error) {
	path, err := filepath.Abs(filepath.Join(stateDir, "state.db"))
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("opening the feed: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("opening the feed: %w", err)
	}
	return &Feed{db: db}, nil
}

// Close closes the reader.
func (f *Feed) Close() error { return f.db.Close() }

// Entry is one feed entry: what's sent, and its local key.
type Entry struct {
	Change wire.Change
	Key    store.FeedKey
}

// The feed, one query per table (keeping each column's declared type, so
// timestamps scan as times). ?1 drive, (?2, ?3] versions, ?4 limit.
var feedQueries = []struct {
	scan  func(*sql.Rows) (Entry, error)
	query string
}{
	{scanFile, `SELECT row_version, relative_path, size, mtime_unix, mode, content_hash, hash_algo, status, error_message, scanned_at
		FROM files WHERE drive_id = ?1 AND row_version > ?2 AND row_version <= ?3 ORDER BY row_version LIMIT ?4`},
	{scanDirChild, `SELECT row_version, relative_path, child_name, is_dir, first_seen_at
		FROM dir_listings WHERE drive_id = ?1 AND row_version > ?2 AND row_version <= ?3 ORDER BY row_version LIMIT ?4`},
	{scanRun, `SELECT row_version, id, started_at, finished_at, files_seen, bytes_hashed, interrupted
		FROM scan_runs WHERE drive_id = ?1 AND row_version > ?2 AND row_version <= ?3 ORDER BY row_version LIMIT ?4`},
	{scanTombstone, `SELECT row_version, kind, relative_path, child_name
		FROM sync_tombstones WHERE drive_id = ?1 AND row_version > ?2 AND row_version <= ?3 ORDER BY row_version LIMIT ?4`},
}

// Page reads up to limit entries of the drive with versions in
// (after, upper], sorted by version. more reports whether the interval has
// entries beyond them.
func (f *Feed) Page(ctx context.Context, driveID string, after, upper int64, limit int) (entries []Entry, more bool, err error) {
	tx, err := f.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	for _, q := range feedQueries {
		rows, err := tx.QueryContext(ctx, q.query, driveID, after, upper, limit+1)
		if err != nil {
			return nil, false, err
		}
		for rows.Next() {
			e, err := q.scan(rows)
			if err != nil {
				rows.Close()
				return nil, false, err
			}
			entries = append(entries, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Change.V < entries[j].Change.V })
	if len(entries) > limit {
		return entries[:limit], true, nil
	}
	return entries, false, nil
}

// Count counts the drive's entries in each of the intervals.
func (f *Feed) Count(ctx context.Context, driveID string, intervals []wire.Range) (int64, error) {
	tx, err := f.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var total int64
	for _, iv := range intervals {
		for _, table := range []string{"files", "dir_listings", "scan_runs", "sync_tombstones"} {
			var n int64
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE drive_id = ? AND row_version > ? AND row_version <= ?`,
				driveID, iv[0], iv[1]).Scan(&n); err != nil {
				return 0, err
			}
			total += n
		}
	}
	return total, nil
}

// setName sets a path or child name: plain if it's valid UTF-8, otherwise
// as base64 of its bytes (Go's JSON encoder would replace invalid bytes
// with U+FFFD).
func setName(plain **string, b64 *string, name string) {
	if utf8.ValidString(name) {
		*plain = &name
		return
	}
	*b64 = base64.StdEncoding.EncodeToString([]byte(name))
}

func utc(t time.Time) *time.Time {
	t = t.UTC()
	return &t
}

func scanFile(rows *sql.Rows) (Entry, error) {
	var e Entry
	var hash, algo, errMsg sql.NullString
	var size, mtime, mode int64
	var scannedAt time.Time
	c := &e.Change
	if err := rows.Scan(&c.V, &e.Key.Path, &size, &mtime, &mode, &hash, &algo, &c.Status, &errMsg, &scannedAt); err != nil {
		return e, fmt.Errorf("reading a files row: %w", err)
	}
	c.Kind, c.Op = wire.KindFile, wire.OpUpsert
	setName(&c.Path, &c.PathB64, e.Key.Path)
	c.Size, c.MTimeUnix, c.Mode = &size, &mtime, &mode
	c.ContentHash, c.HashAlgo, c.ErrorMessage = hash.String, algo.String, errMsg.String
	c.ScannedAt = utc(scannedAt)
	e.Key.Kind, e.Key.V = c.Kind, c.V
	return e, nil
}

func scanDirChild(rows *sql.Rows) (Entry, error) {
	var e Entry
	var isDir bool
	var firstSeen time.Time
	c := &e.Change
	if err := rows.Scan(&c.V, &e.Key.Path, &e.Key.Child, &isDir, &firstSeen); err != nil {
		return e, fmt.Errorf("reading a dir_listings row: %w", err)
	}
	c.Kind, c.Op = wire.KindDirChild, wire.OpUpsert
	setName(&c.Path, &c.PathB64, e.Key.Path)
	setName(&c.Child, &c.ChildB64, e.Key.Child)
	c.IsDir, c.FirstSeenAt = &isDir, utc(firstSeen)
	e.Key.Kind, e.Key.V = c.Kind, c.V
	return e, nil
}

func scanRun(rows *sql.Rows) (Entry, error) {
	var e Entry
	var id int64
	var started time.Time
	var finished sql.NullTime
	var filesSeen, bytesHashed sql.NullInt64
	var interrupted sql.NullBool
	c := &e.Change
	if err := rows.Scan(&c.V, &id, &started, &finished, &filesSeen, &bytesHashed, &interrupted); err != nil {
		return e, fmt.Errorf("reading a scan_runs row: %w", err)
	}
	c.Kind, c.Op = wire.KindScanRun, wire.OpUpsert
	c.RunID, c.StartedAt = &id, utc(started)
	if finished.Valid {
		c.FinishedAt = utc(finished.Time)
	}
	if filesSeen.Valid {
		c.FilesSeen = &filesSeen.Int64
	}
	if bytesHashed.Valid {
		c.BytesHashed = &bytesHashed.Int64
	}
	if interrupted.Valid {
		c.Interrupted = &interrupted.Bool
	}
	e.Key = store.FeedKey{Kind: c.Kind, Path: store.ScanRunKey(id), V: c.V}
	return e, nil
}

func scanTombstone(rows *sql.Rows) (Entry, error) {
	var e Entry
	c := &e.Change
	if err := rows.Scan(&c.V, &e.Key.Kind, &e.Key.Path, &e.Key.Child); err != nil {
		return e, fmt.Errorf("reading a sync_tombstones row: %w", err)
	}
	c.Kind, c.Op = e.Key.Kind, wire.OpDelete
	setName(&c.Path, &c.PathB64, e.Key.Path)
	if c.Kind == wire.KindDirChild {
		setName(&c.Child, &c.ChildB64, e.Key.Child)
	}
	e.Key.V = c.V
	return e, nil
}
