package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// Duplicates: identical files and folders within and across a user's
// Google Drive, Cloud Storage and agent drives, and Google Photos items
// that are likely copies of files elsewhere. Each user's duplicates are
// worked out ahead of time into an index (dup_groups, dup_members), rebuilt
// when its inputs change. See docs/specs/duplicates.md.

// Kinds of duplicate groups.
const (
	DupFile   = "file"
	DupFolder = "folder"
	DupPhoto  = "photo"
)

// dupRulesVersion is part of every user's fingerprint: raising it, when
// the rules below change, rebuilds every index.
const dupRulesVersion = "2"

// systemFiles matches paths whose last part is a file the index leaves
// out: .DS_Store, Thumbs.db, desktop.ini, macOS's Icon\r and resource
// forks (._*). A POSIX regex for Postgres.
const systemFiles = `(^|/)(\.DS_Store|Thumbs\.db|desktop\.ini|Icon` + "\r" + `|\._[^/]*)$`

func migrateDuplicates() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS dup_groups (
			id            BIGSERIAL PRIMARY KEY,
			user_id       BIGINT NOT NULL REFERENCES agent_users(id),
			kind          TEXT NOT NULL,
			key           TEXT NOT NULL,
			name          TEXT NOT NULL,
			size          BIGINT NOT NULL,
			files         BIGINT NOT NULL DEFAULT 1,
			copies        INT NOT NULL,
			reclaimable   BIGINT NOT NULL,
			same_physical BOOLEAN NOT NULL,
			sources       TEXT[] NOT NULL,
			match         TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS dup_groups_page ON dup_groups (user_id, kind, reclaimable DESC, size DESC, id)`,
		`CREATE INDEX IF NOT EXISTS dup_groups_key ON dup_groups (user_id, kind, key)`,
		`CREATE INDEX IF NOT EXISTS dup_groups_sources ON dup_groups USING gin (sources)`,
		`CREATE TABLE IF NOT EXISTS dup_members (
			group_id       BIGINT NOT NULL REFERENCES dup_groups(id) ON DELETE CASCADE,
			source         TEXT NOT NULL,
			label          TEXT NOT NULL,
			item           TEXT NOT NULL,
			path           TEXT NOT NULL,
			folder         TEXT NOT NULL,
			size           BIGINT NOT NULL,
			files          BIGINT NOT NULL DEFAULT 1,
			modified       TIMESTAMPTZ,
			physical_drive BIGINT,
			shared         BOOLEAN NOT NULL DEFAULT false
		)`,
		`CREATE INDEX IF NOT EXISTS dup_members_group ON dup_members (group_id)`,
		`CREATE TABLE IF NOT EXISTS dup_state (
			user_id            BIGINT PRIMARY KEY REFERENCES agent_users(id),
			fingerprint        TEXT NOT NULL DEFAULT '',
			built_at           TIMESTAMPTZ,
			building           BOOLEAN NOT NULL DEFAULT false,
			took_ms            BIGINT NOT NULL DEFAULT 0,
			uncomparable       JSONB NOT NULL DEFAULT '[]',
			by_source          JSONB NOT NULL DEFAULT '[]',
			counts             JSONB NOT NULL DEFAULT '{}',
			error              TEXT NOT NULL DEFAULT '',
			failed_at          TIMESTAMPTZ,
			failed_fingerprint TEXT NOT NULL DEFAULT ''
		)`,
		// Added after the first version, which dev stacks may have.
		`ALTER TABLE dup_members ADD COLUMN IF NOT EXISTS shared BOOLEAN NOT NULL DEFAULT false`,
		`ALTER TABLE dup_state ADD COLUMN IF NOT EXISTS counts JSONB NOT NULL DEFAULT '{}'`,
		`ALTER TABLE dup_state ADD COLUMN IF NOT EXISTS failed_at TIMESTAMPTZ`,
		`ALTER TABLE dup_state ADD COLUMN IF NOT EXISTS failed_fingerprint TEXT NOT NULL DEFAULT ''`,
		// A build cut off by a restart runs again at the first check.
		`UPDATE dup_state SET building = false WHERE building`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to add the duplicates index to the schema: %w", err)
		}
	}
	return nil
}

// wakeDuplicates tells the builder its inputs may have changed.
var wakeDuplicates = make(chan struct{}, 1)

// WakeDuplicates asks the builder to check every user's index now, as when
// a scan or a deletion ends. It never blocks.
func WakeDuplicates() {
	select {
	case wakeDuplicates <- struct{}{}:
	default:
	}
}

// DuplicatesBuilder rebuilds the index of each user whose inputs changed:
// at once, then every interval, and soon after WakeDuplicates. One build
// at a time, one user at a time.
func DuplicatesBuilder(ctx context.Context, interval time.Duration) {
	for {
		checkDuplicates(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		case <-wakeDuplicates:
			// Let a burst of scans or uploads settle first.
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

// While an agent drive syncs, its acked version moves with every batch, and
// the index would be rebuilt from a half-synced drive at every check. So a
// change to agent drives alone rebuilds only once it has held still for
// dupAgentQuiet, since the previous periodic check; a change to Google
// sources (a scan or deletion ending) rebuilds at once, with the agent
// drives as they are. A fingerprint whose build failed isn't tried again
// for dupRetryAfter. Tests change both.
var (
	dupAgentQuiet = 5 * time.Minute
	dupRetryAfter = time.Hour
)

// dupAgentSeen is, per user, the agent part of the fingerprint the builder
// last saw, and since when. Only the builder's goroutine uses it.
var dupAgentSeen = map[int64]agentSeen{}

type agentSeen struct {
	fingerprint string
	since       time.Time
}

func checkDuplicates(ctx context.Context) {
	var users []int64
	if err := db.Select(&users, `SELECT id FROM agent_users ORDER BY id`); err != nil {
		slog.Error("Failed to list users for the duplicates index", "error", err)
		return
	}
	for _, user := range users {
		if ctx.Err() != nil {
			return
		}
		google, agent, err := dupFingerprint(user)
		if err != nil {
			slog.Error("Failed to fingerprint the duplicates index", "user", user, "error", err)
			continue
		}
		fingerprint := google + ":" + agent
		var state struct {
			Fingerprint string     `db:"fingerprint"`
			Built       bool       `db:"built"`
			Failed      string     `db:"failed_fingerprint"`
			FailedAt    *time.Time `db:"failed_at"`
		}
		err = db.Get(&state, `SELECT fingerprint, built_at IS NOT NULL AS built, failed_fingerprint, failed_at
			FROM dup_state WHERE user_id = $1`, user)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			slog.Error("Failed to read the duplicates index's state", "user", user, "error", err)
			continue
		}
		now := time.Now()
		seen := dupAgentSeen[user]
		if seen.fingerprint != agent {
			seen = agentSeen{agent, now}
			dupAgentSeen[user] = seen
		}
		builtGoogle, builtAgent, _ := strings.Cut(state.Fingerprint, ":")
		switch {
		case !state.Built: // the first build doesn't wait
		case builtGoogle != google:
		case builtAgent != agent && now.Sub(seen.since) >= dupAgentQuiet:
		default:
			continue
		}
		if state.Failed == fingerprint && state.FailedAt != nil && now.Sub(*state.FailedAt) < dupRetryAfter {
			continue
		}
		if err := BuildDuplicates(user, fingerprint); err != nil {
			slog.Error("Failed to build the duplicates index", "user", user, "error", err)
		}
	}
}

// dupFingerprint is what userID's index is built from, in two parts. The
// Google part: every Drive record's and bucket's last update, the user's
// Photos scans, the accounts' names, and the rules' version. The agent
// part: every agent drive's acked version (it moves with every upload),
// physical drive, and name.
func dupFingerprint(userID int64) (google string, agent string, err error) {
	var parts []sql.NullString
	err = db.Select(&parts, `SELECT * FROM (VALUES
		((SELECT string_agg(d.id || ':' || COALESCE(d.physical_drive_id, 0) || ':' || d.acked_version || ':' ||
				d.drive_id || ':' || COALESCE(a.hostname, ''), ',' ORDER BY d.id)
			FROM agent_drives d JOIN agent_agents a ON a.id = d.agent_id WHERE a.user_id = $1)),
		((SELECT string_agg(a.client_key || ':' || a.updated_at, ',' ORDER BY a.client_key)
			FROM drive_accounts a JOIN privatetokens p ON p.client_key = a.client_key WHERE p.user_id = $1)),
		((SELECT string_agg(b.client_key || '/' || b.bucket || ':' || b.updated_at, ',' ORDER BY b.client_key, b.bucket)
			FROM gcs_buckets b JOIN privatetokens p ON p.client_key = b.client_key WHERE p.user_id = $1)),
		((SELECT max(s.id) || ':' || count(*) FROM scans s WHERE s.user_id = $1 AND s.scan_type = 'google_photos')),
		((SELECT string_agg(client_key || ':' || COALESCE(display_name, ''), ',' ORDER BY client_key)
			FROM privatetokens WHERE user_id = $1))
	) v`, userID)
	if err != nil {
		return "", "", fmt.Errorf("failed to fingerprint the duplicates index of user %d: %w", userID, err)
	}
	hash := func(parts ...sql.NullString) string {
		h := sha256.New()
		fmt.Fprint(h, dupRulesVersion)
		for _, p := range parts {
			fmt.Fprint(h, "|", p.String)
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	return hash(parts[1:]...), hash(parts[0]), nil
}

// dupBuildMu keeps builds one at a time.
var dupBuildMu sync.Mutex

// BuildDuplicates rebuilds userID's index from scratch, replacing the old
// one in one transaction, and records fingerprint as what it was built
// from. A failure is recorded in dup_state, for the page.
func BuildDuplicates(userID int64, fingerprint string) error {
	dupBuildMu.Lock()
	defer dupBuildMu.Unlock()
	if _, err := db.Exec(`INSERT INTO dup_state (user_id, building) VALUES ($1, true)
		ON CONFLICT (user_id) DO UPDATE SET building = true`, userID); err != nil {
		return fmt.Errorf("failed to mark the duplicates index as building: %w", err)
	}
	took, phases, err := buildDuplicates(userID, fingerprint)
	if err != nil {
		if _, dbErr := db.Exec(`UPDATE dup_state SET building = false, error = $2, failed_at = now(),
				failed_fingerprint = $3 WHERE user_id = $1`, userID, err.Error(), fingerprint); dbErr != nil {
			slog.Error("Failed to record a failed duplicates build", "user", userID, "error", dbErr)
		}
		return err
	}
	slog.Info("Built the duplicates index", "user", userID, "took", took, "phases", phases)
	return nil
}

// Uncomparable is a source's folders that couldn't be compared: they hold
// a file without a content hash.
type Uncomparable struct {
	Source  string `json:"source"`
	Label   string `json:"label"`
	Folders int    `json:"folders"`
	Reason  string `json:"reason"`
}

// buildDuplicates writes userID's index, and its state, in one transaction,
// and returns how long it took, overall and by phase.
func buildDuplicates(userID int64, fingerprint string) (time.Duration, string, error) {
	start := time.Now()
	// A connection of its own, closed after: the temp tables need more
	// local buffers than the default 8 MB, which a session can only set
	// before its first temp table, and keeps once allocated.
	ctx := context.Background()
	conn, err := db.Connx(ctx)
	if err != nil {
		return 0, "", fmt.Errorf("failed to get a connection: %w", err)
	}
	defer func() {
		conn.Raw(func(any) error { return driver.ErrBadConn })
		conn.Close()
	}()
	if _, err := conn.ExecContext(ctx, `SET temp_buffers = '512MB'`); err != nil {
		slog.Warn("Building the duplicates index with the default temp buffers", "error", err)
	}
	tx, err := conn.BeginTxx(ctx, nil)
	if err != nil {
		return 0, "", fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	// How long each phase took, for the log.
	var phases []string
	last := time.Now()
	phase := func(name string) {
		phases = append(phases, fmt.Sprintf("%s %v", name, time.Since(last).Round(time.Millisecond)))
		last = time.Now()
	}
	if _, err := tx.Exec(`SET LOCAL work_mem = '256MB'`); err != nil {
		return 0, "", err
	}
	if err := gatherDupFiles(tx, userID); err != nil {
		return 0, "", err
	}
	phase("gather")
	// Members first: their foreign key's cascade would delete them a row
	// at a time.
	for _, stmt := range []string{
		`DELETE FROM dup_members WHERE group_id IN (SELECT id FROM dup_groups WHERE user_id = $1)`,
		`DELETE FROM dup_groups WHERE user_id = $1`,
	} {
		if _, err := tx.Exec(stmt, userID); err != nil {
			return 0, "", fmt.Errorf("failed to clear the old index: %w", err)
		}
	}
	phase("clear")
	if err := writeFileGroups(tx, userID); err != nil {
		return 0, "", err
	}
	phase("files")
	uncomparable, err := buildFolderGroups(tx, userID)
	if err != nil {
		return 0, "", err
	}
	phase("folders")
	if err := buildPhotoGroups(tx, userID); err != nil {
		return 0, "", err
	}
	phase("photos")
	encoded, err := json.Marshal(uncomparable)
	if err != nil {
		return 0, "", err
	}
	// The state commits with the groups, so the page's totals always match
	// the index.
	if _, err := tx.Exec(`UPDATE dup_state SET fingerprint = $2, built_at = now(), building = false,
			took_ms = $3, uncomparable = $4, error = '', failed_at = NULL, failed_fingerprint = '',
			by_source = (SELECT COALESCE(jsonb_agg(s ORDER BY s.bytes DESC, s.source), '[]') FROM (
				SELECT m.source, min(m.label) AS label, sum(m.size) AS bytes, count(*) AS files
				FROM dup_members m JOIN dup_groups g ON g.id = m.group_id
				WHERE g.user_id = $1 AND g.kind = 'file' AND NOT g.same_physical
				GROUP BY m.source) s),
			counts = (SELECT COALESCE(jsonb_object_agg(kind, jsonb_build_object('all', n,
					'hide_same_physical', apart, 'across', across, 'reclaimable', reclaimable)), '{}') FROM (
				SELECT kind, count(*) AS n, count(*) FILTER (WHERE NOT same_physical) AS apart,
					count(*) FILTER (WHERE cardinality(sources) > 1 AND NOT same_physical) AS across,
					sum(reclaimable) AS reclaimable
				FROM dup_groups WHERE user_id = $1 GROUP BY kind) c)
		WHERE user_id = $1`, userID, fingerprint, time.Since(start).Milliseconds(), encoded); err != nil {
		return 0, "", fmt.Errorf("failed to record the duplicates index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, "", fmt.Errorf("failed to commit the duplicates index: %w", err)
	}
	phase("commit")
	return time.Since(start), strings.Join(phases, ", "), nil
}

// gatherDupFiles collects every live file of userID's sources, with its
// key (empty when it has no content hash, e.g. a Google Doc), into the temp
// table dup_files, and the sources' names into dup_sources. Paths are as
// Browse shows them: Drive's from "My Drive" or "Shared with me", Cloud
// Storage's the object's name in its bucket, an agent drive's relative to
// its root. dup_files is kept narrow, as it's read a few times over: a
// file's name, and for most sources its item and folder, come from its
// path (dupNameSQL, dupItemSQL, dupFolderSQL).
//
// Drive allows folders of the same name in one folder, so its paths don't
// identify folders. A Drive file's sign is its path for signing folders,
// with each folder as "<name>\x01<ID>" and any "/" in a name as "∕"; see
// signer. Others sign by their path.
func gatherDupFiles(tx *sqlx.Tx, userID int64) error {
	statements := []string{
		`CREATE TEMP TABLE dup_sources ON COMMIT DROP AS
		SELECT 'google:' || a.client_key || ':drive' AS source,
			COALESCE(NULLIF(p.display_name, ''), a.client_key) || ' · Google Drive' AS label, NULL::bigint AS physical_drive
		FROM drive_accounts a JOIN privatetokens p ON p.client_key = a.client_key AND p.user_id = $1
		UNION ALL
		SELECT 'google:' || b.client_key || ':gcs:' || b.bucket, b.bucket || ' · Cloud Storage', NULL
		FROM gcs_buckets b JOIN privatetokens p ON p.client_key = b.client_key AND p.user_id = $1
		UNION ALL
		SELECT 'agent:' || d.id, d.drive_id || ' (' || COALESCE(a.hostname, '') || ')', d.physical_drive_id
		FROM agent_drives d JOIN agent_agents a ON a.id = d.agent_id AND a.user_id = $1`,
		// A folder reachable two ways (an item with several parents) takes
		// its shallowest, then first, path, the same at every build.
		`CREATE TEMP TABLE dup_drive_folders ON COMMIT DROP AS
		WITH RECURSIVE acct AS (
			SELECT p.client_key, a.my_drive_id FROM privatetokens p
			JOIN drive_accounts a ON a.client_key = p.client_key WHERE p.user_id = $1
		), folders (client_key, file_id, name, path, sign, depth) AS (
			SELECT i.client_key, i.file_id, i.name, root || '/' || i.name,
				root || '/' || replace(i.name, '/', '∕') || chr(1) || i.file_id, 0
			FROM drive_items i JOIN acct a ON a.client_key = i.client_key,
			LATERAL (SELECT CASE WHEN i.parent_id = a.my_drive_id THEN 'My Drive' ELSE 'Shared with me' END AS root) r
			WHERE i.is_dir AND NOT i.trashed AND (i.parent_id IS NULL OR i.parent_id = a.my_drive_id
				OR NOT EXISTS (SELECT 1 FROM drive_items p WHERE p.client_key = i.client_key AND p.file_id = i.parent_id))
		UNION ALL
			SELECT c.client_key, c.file_id, c.name, f.path || '/' || c.name,
				f.sign || '/' || replace(c.name, '/', '∕') || chr(1) || c.file_id, f.depth + 1
			FROM folders f
			JOIN drive_items c ON c.client_key = f.client_key AND c.parent_id = f.file_id AND c.is_dir AND NOT c.trashed
			WHERE f.depth < 100
		)
		SELECT DISTINCT ON (client_key, file_id) client_key, file_id, name, path, sign FROM folders
		ORDER BY client_key, file_id, depth, path`,
		// item, folder and sign are NULL when they follow from the path.
		`CREATE TEMP TABLE dup_files ON COMMIT DROP AS
		SELECT CASE WHEN i.md5 <> '' THEN 'md5:' || i.md5 || ':' || i.size ELSE '' END AS key,
			'google:' || i.client_key || ':drive' AS source,
			i.file_id AS item,
			COALESCE(f.path, root) || '/' || i.name AS path,
			COALESCE(f.sign, root) || '/' || replace(i.name, '/', '∕') AS sign,
			CASE WHEN f.file_id IS NOT NULL OR i.parent_id = a.my_drive_id THEN i.parent_id
				ELSE '` + SharedWithMe + `' END AS folder,
			i.size, i.modified, NULL::bigint AS physical_drive, i.owned_by_me AS owned, i.capture_time, i.width, i.height
		FROM drive_items i
		JOIN privatetokens p ON p.client_key = i.client_key AND p.user_id = $1
		JOIN drive_accounts a ON a.client_key = i.client_key
		LEFT JOIN dup_drive_folders f ON f.client_key = i.client_key AND f.file_id = i.parent_id,
		LATERAL (SELECT CASE WHEN i.parent_id = a.my_drive_id THEN 'My Drive' ELSE 'Shared with me' END AS root) r
		WHERE NOT i.is_dir AND NOT i.trashed AND i.size > 0 AND i.name !~ $2
		UNION ALL
		SELECT CASE WHEN o.md5hash <> '' THEN 'md5:' || o.md5hash || ':' || o.size ELSE '' END,
			'google:' || o.client_key || ':gcs:' || o.bucket, NULL, o.name, NULL, NULL, o.size, o.updated, NULL, true,
			NULL, NULL, NULL
		FROM gcs_objects o JOIN privatetokens p ON p.client_key = o.client_key AND p.user_id = $1
		WHERE o.state = 'live' AND o.size > 0 AND o.name !~ $2
		UNION ALL
		SELECT CASE WHEN f.status <> 'hashed' THEN ''
				WHEN f.md5 IS NOT NULL THEN 'md5:' || f.md5 || ':' || f.size
				ELSE COALESCE('blake3:' || f.content_hash || ':' || f.size, '') END,
			'agent:' || d.id, NULL, f.relative_path, NULL, NULL, f.size, f.mtime, d.physical_drive_id, true,
			NULL, NULL, NULL
		FROM agent_files f JOIN agent_drives d ON d.id = f.drive_pk
		JOIN agent_agents a ON a.id = d.agent_id AND a.user_id = $1
		WHERE f.size > 0 AND f.relative_path !~ $2`,
		`ANALYZE dup_sources`,
		`ANALYZE dup_files`,
	}
	for _, stmt := range statements {
		var args []any
		if strings.Contains(stmt, "$2") {
			args = []any{userID, systemFiles}
		} else if strings.Contains(stmt, "$1") {
			args = []any{userID}
		}
		if _, err := tx.Exec(stmt, args...); err != nil {
			return fmt.Errorf("failed to gather the files to compare: %w", err)
		}
	}
	return nil
}

// A dup_files row f's name, item (Drive's file ID, else its path) and
// folder (what Browse opens: Drive's folder ID, "<bucket>/<prefix>", or
// an agent drive's folder path).
const (
	dupNameSQL   = `regexp_replace(f.path, '^.*/', '')`
	dupItemSQL   = `COALESCE(f.item, f.path)`
	dupFolderSQL = `COALESCE(f.folder, CASE WHEN f.source LIKE 'agent:%' THEN regexp_replace(f.path, '/?[^/]*$', '')
		ELSE substr(f.source, strpos(f.source, ':gcs:') + 5) || '/' || regexp_replace(f.path, '[^/]*$', '') END)`
)

// copyOf names a copy, counting each physical drive's copy of a path once:
// seagate1 uploaded from three machines is one copy. A group's reclaimable
// space counts only the copies the user owns: a file shared with them
// isn't theirs to delete, nor does it use their storage.
const (
	copyOf         = `COALESCE('pd' || f.physical_drive || ':' || f.path, f.source || ':' || ` + dupItemSQL + `)`
	distinctCopies = `count(DISTINCT ` + copyOf + `)`
	ownedCopies    = `count(DISTINCT CASE WHEN f.owned THEN ` + copyOf + ` END)`
)

// writeFileGroups groups dup_files by key into the index. The groups get
// their IDs in a temp table, so their members join that, not dup_groups.
func writeFileGroups(tx *sqlx.Tx, userID int64) error {
	statements := []string{
		`CREATE TEMP TABLE dup_file_groups ON COMMIT DROP AS
		SELECT nextval(pg_get_serial_sequence('dup_groups', 'id')) AS id, key,
			mode() WITHIN GROUP (ORDER BY ` + dupNameSQL + `) AS name,
			max(size) AS size, count(*) AS copies, max(size) * GREATEST(` + ownedCopies + ` - 1, 0) AS reclaimable,
			` + distinctCopies + ` = 1 AS same_physical, array_agg(DISTINCT source ORDER BY source) AS sources
		FROM dup_files f WHERE key <> '' GROUP BY key HAVING count(*) > 1`,
		`ANALYZE dup_file_groups`,
		`INSERT INTO dup_groups (id, user_id, kind, key, name, size, files, copies, reclaimable, same_physical, sources)
		SELECT id, $1, 'file', key, name, size, 1, copies, reclaimable, same_physical, sources FROM dup_file_groups`,
		`INSERT INTO dup_members (group_id, source, label, item, path, folder, size, modified, physical_drive, shared)
		SELECT g.id, f.source, s.label, ` + dupItemSQL + `, f.path, ` + dupFolderSQL + `, f.size, f.modified,
			f.physical_drive, NOT f.owned
		FROM dup_files f JOIN dup_file_groups g ON g.key = f.key JOIN dup_sources s ON s.source = f.source`,
	}
	for _, stmt := range statements {
		var args []any
		if strings.Contains(stmt, "$1") {
			args = []any{userID}
		}
		if _, err := tx.Exec(stmt, args...); err != nil {
			return fmt.Errorf("failed to write the duplicate files: %w", err)
		}
	}
	return nil
}

// dupFolder is a folder, its signature, and what's under it.
type dupFolder struct {
	source, label string
	key           string // its sign path: identifies it in its source
	path, name    string // for display
	item          string // what Browse opens: the Drive folder's ID, "<bucket>/<prefix>/", or the path
	physical      sql.NullInt64
	files, bytes  int64
	sig           string
	ok            bool // every file below has a key
	owned         bool // and is the user's
}

// parent is the key of the folder above f; false for a source's root.
func (f *dupFolder) parent() (string, bool) {
	if f.key == "" {
		return "", false
	}
	i := strings.LastIndex(f.key, "/")
	if i < 0 {
		return "", true
	}
	return f.key[:i], true
}

// signer computes folder signatures from a source's files, given in sign
// path order (bytewise, so a folder's contents are contiguous), bottom up:
// a folder's signature is a SHA-256 over its children, sorted by name, each
// "<name>\0<f|d>\0<key or signature>\n". See docs/specs/duplicates.md,
// "Folders". A folder's part of a sign path may be "<name>\x01<ID>", which
// keeps apart Drive folders of one name; its name is the part before
// \x01.
type signer struct {
	source, label string
	physical      sql.NullInt64
	stack         []*openFolder
	done          []*dupFolder
}

type openFolder struct {
	key, part    string
	entries      []string // "<name>\0<f|d>\0<value>"
	files, bytes int64
	ok, owned    bool
}

func newSigner(source, label string, physical sql.NullInt64) *signer {
	return &signer{source: source, label: label, physical: physical,
		stack: []*openFolder{{ok: true, owned: true}}}
}

// add takes the next file in sign path order.
func (s *signer) add(sign, key string, size int64, owned bool) {
	parts := strings.Split(sign, "/")
	dirs, name := parts[:len(parts)-1], parts[len(parts)-1]
	// Close the open folders that aren't above this file.
	depth := 0
	for depth < len(dirs) && depth+1 < len(s.stack) && s.stack[depth+1].part == dirs[depth] {
		depth++
	}
	for len(s.stack) > depth+1 {
		s.pop()
	}
	for _, d := range dirs[depth:] {
		parent := s.stack[len(s.stack)-1]
		key := d
		if len(s.stack) > 1 {
			key = parent.key + "/" + d
		}
		s.stack = append(s.stack, &openFolder{key: key, part: d, ok: true, owned: true})
	}
	f := s.stack[len(s.stack)-1]
	f.files++
	f.bytes += size
	f.owned = f.owned && owned
	if key == "" {
		f.ok = false
		return
	}
	f.entries = append(f.entries, name+"\x00f\x00"+key)
}

// pop closes the innermost open folder, and adds it to its parent.
func (s *signer) pop() {
	f := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]
	sort.Strings(f.entries)
	h := sha256.New()
	for _, e := range f.entries {
		h.Write([]byte(e))
		h.Write([]byte{'\n'})
	}
	sig := hex.EncodeToString(h.Sum(nil))
	name, _, _ := strings.Cut(f.part, "\x01")
	if f.key == "" {
		name = s.label
	}
	s.done = append(s.done, &dupFolder{source: s.source, label: s.label, key: f.key, path: f.key, name: name,
		item: f.key, physical: s.physical, files: f.files, bytes: f.bytes, sig: sig, ok: f.ok, owned: f.owned})
	if len(s.stack) > 0 {
		parent := s.stack[len(s.stack)-1]
		parent.files += f.files
		parent.bytes += f.bytes
		parent.ok = parent.ok && f.ok
		parent.owned = parent.owned && f.owned
		parent.entries = append(parent.entries, name+"\x00d\x00"+sig)
	}
}

// finish closes every open folder, the root last, and returns them all.
func (s *signer) finish() []*dupFolder {
	for len(s.stack) > 0 {
		s.pop()
	}
	return s.done
}

// uncomparableReason says why a source's folders can't all be compared.
func uncomparableReason(source string) string {
	switch {
	case strings.HasSuffix(source, ":drive"):
		return "They hold Google Docs, Sheets or Slides, which have no MD5."
	case strings.Contains(source, ":gcs:"):
		return "They hold composite or encrypted objects, which have no MD5."
	default:
		return "They hold files not hashed yet, or that driveagent couldn't read."
	}
}

// buildFolderGroups signs every folder of the user's sources, groups those
// with the same signature, and writes the groups not wholly inside
// another; it returns the folders that couldn't be compared, by source.
func buildFolderGroups(tx *sqlx.Tx, userID int64) ([]Uncomparable, error) {
	var sources []struct {
		Source   string        `db:"source"`
		Label    string        `db:"label"`
		Physical sql.NullInt64 `db:"physical_drive"`
	}
	if err := tx.Select(&sources, `SELECT source, label, physical_drive FROM dup_sources`); err != nil {
		return nil, fmt.Errorf("failed to list the sources: %w", err)
	}
	signers := map[string]*signer{}
	for _, src := range sources {
		signers[src.Source] = newSigner(src.Source, src.Label, src.Physical)
	}
	// Drive folders by their sign path: their ID, path and name.
	type driveFolder struct{ id, path, name string }
	driveFolders := map[string]driveFolder{}
	rows, err := tx.Query(`SELECT 'google:' || client_key || ':drive', sign, file_id, path,
			name FROM dup_drive_folders
		UNION ALL SELECT 'google:' || a.client_key || ':drive', 'My Drive', a.my_drive_id, 'My Drive', 'My Drive'
		FROM drive_accounts a
		JOIN privatetokens p ON p.client_key = a.client_key AND p.user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to read Drive's folders: %w", err)
	}
	for rows.Next() {
		var d driveFolder
		var source, sign string
		if err := rows.Scan(&source, &sign, &d.id, &d.path, &d.name); err != nil {
			rows.Close()
			return nil, err
		}
		driveFolders[source+"\x00"+sign] = d
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read Drive's folders: %w", err)
	}

	// One pass over every file, a source at a time, each in sign path
	// order: bytewise, so a folder's contents are contiguous.
	rows, err = tx.Query(`SELECT source, COALESCE(sign, path), key, size, owned FROM dup_files
		ORDER BY source, COALESCE(sign, path) COLLATE "C"`)
	if err != nil {
		return nil, fmt.Errorf("failed to read the files: %w", err)
	}
	for rows.Next() {
		var source, sign, key string
		var size int64
		var owned bool
		if err := rows.Scan(&source, &sign, &key, &size, &owned); err != nil {
			rows.Close()
			return nil, err
		}
		if s := signers[source]; s != nil {
			s.add(sign, key, size, owned)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read the files: %w", err)
	}

	bySig := map[string][]*dupFolder{}
	var uncomparable []Uncomparable
	for _, src := range sources {
		bad := 0
		isDrive := strings.HasSuffix(src.Source, ":drive")
		_, bucket, isGcs := strings.Cut(src.Source, ":gcs:")
		for _, f := range signers[src.Source].finish() {
			// Drive's pseudo-roots aren't folders, nor is a source with no
			// files (a signer's root without entries).
			if isDrive && (f.key == "" || f.key == "Shared with me") || f.files == 0 {
				continue
			}
			if !f.ok {
				bad++
				continue
			}
			switch {
			case isDrive:
				d, found := driveFolders[src.Source+"\x00"+f.key]
				if !found {
					continue
				}
				f.item, f.path, f.name = d.id, d.path, d.name
			case isGcs && f.key == "":
				f.item = bucket + "/"
			case isGcs:
				f.item = bucket + "/" + f.key + "/"
			}
			bySig[f.sig] = append(bySig[f.sig], f)
		}
		if bad > 0 {
			uncomparable = append(uncomparable, Uncomparable{Source: src.Source, Label: src.Label, Folders: bad,
				Reason: uncomparableReason(src.Source)})
		}
	}
	sort.Slice(uncomparable, func(i, j int) bool { return uncomparable[i].Source < uncomparable[j].Source })

	// Groups of two or more, and which group each member folder is in.
	type key struct{ source, key string }
	var groups [][]*dupFolder
	groupOf := map[key]int{}
	sigs := make([]string, 0, len(bySig))
	for sig, fs := range bySig {
		if len(fs) > 1 {
			sigs = append(sigs, sig)
		}
	}
	sort.Strings(sigs)
	for _, sig := range sigs {
		for _, f := range bySig[sig] {
			groupOf[key{f.source, f.key}] = len(groups)
		}
		groups = append(groups, bySig[sig])
	}
	// A group whose every member's parent is in one other group, of the
	// same size, is explained by that group: only the topmost is kept.
	keep := make([]bool, len(groups))
	for i, g := range groups {
		parentGroup := -1
		nested := true
		for _, f := range g {
			p, ok := f.parent()
			if !ok {
				nested = false
				break
			}
			pg, found := groupOf[key{f.source, p}]
			if !found || (parentGroup >= 0 && pg != parentGroup) {
				nested = false
				break
			}
			parentGroup = pg
		}
		keep[i] = !nested || len(groups[parentGroup]) != len(g)
	}

	var (
		gKeys, gNames, gSources []string
		gSizes, gFiles          []int64
		gCopies                 []int64
		gReclaim                []int64
		gSame                   []bool
	)
	for i, g := range groups {
		if !keep[i] {
			continue
		}
		distinct, owned := map[string]bool{}, map[string]bool{}
		sources := map[string]bool{}
		for _, f := range g {
			id := f.source + ":" + f.key
			if f.physical.Valid {
				id = fmt.Sprintf("pd%d:%s", f.physical.Int64, f.key)
			}
			distinct[id] = true
			if f.owned {
				owned[id] = true
			}
			sources[f.source] = true
		}
		list := make([]string, 0, len(sources))
		for s := range sources {
			list = append(list, s)
		}
		sort.Strings(list)
		gKeys = append(gKeys, g[0].sig)
		gNames = append(gNames, g[0].name)
		gSizes = append(gSizes, g[0].bytes)
		gFiles = append(gFiles, g[0].files)
		gCopies = append(gCopies, int64(len(g)))
		gReclaim = append(gReclaim, g[0].bytes*int64(max(len(owned)-1, 0)))
		gSame = append(gSame, len(distinct) == 1)
		gSources = append(gSources, "{"+strings.Join(quoteAll(list), ",")+"}")
	}
	if len(gKeys) == 0 {
		return uncomparable, nil
	}
	ids := map[string]int64{}
	rows, err = tx.Query(`INSERT INTO dup_groups (user_id, kind, key, name, size, files, copies, reclaimable, same_physical, sources)
		SELECT $1, 'folder', k, n, s, f, c, r, sp, src::text[]
		FROM unnest($2::text[], $3::text[], $4::int8[], $5::int8[], $6::int8[], $7::int8[], $8::bool[], $9::text[])
			AS u(k, n, s, f, c, r, sp, src)
		RETURNING id, key`, userID, pq.Array(gKeys), pq.Array(gNames), pq.Array(gSizes), pq.Array(gFiles),
		pq.Array(gCopies), pq.Array(gReclaim), pq.Array(gSame), pq.Array(gSources))
	if err != nil {
		return nil, fmt.Errorf("failed to write the duplicate folders: %w", err)
	}
	for rows.Next() {
		var id int64
		var sig string
		if err := rows.Scan(&id, &sig); err != nil {
			rows.Close()
			return nil, fmt.Errorf("failed to read the duplicate folders' IDs: %w", err)
		}
		ids[sig] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to write the duplicate folders: %w", err)
	}

	var (
		mGroups, mSizes, mFiles, mPhysical []int64
		mSources, mLabels, mItems, mPaths  []string
		mHasPhysical, mShared              []bool
	)
	for i, g := range groups {
		if !keep[i] {
			continue
		}
		for _, f := range g {
			path := f.path
			if path == "" {
				path = "/"
			}
			mGroups = append(mGroups, ids[f.sig])
			mSources, mLabels, mItems, mPaths = append(mSources, f.source), append(mLabels, f.label),
				append(mItems, f.item), append(mPaths, path)
			mSizes, mFiles = append(mSizes, f.bytes), append(mFiles, f.files)
			mPhysical, mHasPhysical = append(mPhysical, f.physical.Int64), append(mHasPhysical, f.physical.Valid)
			mShared = append(mShared, !f.owned)
		}
	}
	if _, err := tx.Exec(`INSERT INTO dup_members (group_id, source, label, item, path, folder, size, files, physical_drive, shared)
		SELECT g, s, l, i, p, i, sz, f, CASE WHEN hp THEN pd END, sh
		FROM unnest($1::int8[], $2::text[], $3::text[], $4::text[], $5::text[], $6::int8[], $7::int8[], $8::int8[],
			$9::bool[], $10::bool[]) AS u(g, s, l, i, p, sz, f, pd, hp, sh)`,
		pq.Array(mGroups), pq.Array(mSources), pq.Array(mLabels), pq.Array(mItems), pq.Array(mPaths),
		pq.Array(mSizes), pq.Array(mFiles), pq.Array(mPhysical), pq.Array(mHasPhysical), pq.Array(mShared)); err != nil {
		return nil, fmt.Errorf("failed to write the duplicate folders' copies: %w", err)
	}
	return uncomparable, nil
}

// quoteAll quotes strings for a Postgres array literal.
func quoteAll(list []string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	return out
}

// buildPhotoGroups matches the user's picked Google Photos items with files
// elsewhere of the same name, taken or modified at the same time, as likely
// duplicates. Drive images are compared by the capture time Drive read
// from them, which has no zone, so it may differ from the item's (UTC) by
// whole quarter-hours, up to 14 hours; other files by their modified time,
// within a minute. When both sides' dimensions are known, they must match.
func buildPhotoGroups(tx *sqlx.Tx, userID int64) error {
	_, err := tx.Exec(`
	CREATE TEMP TABLE dup_photo_matches ON COMMIT DROP AS
	WITH picks AS (
		SELECT DISTINCT ON (pi.media_item_id) pi.media_item_id, pi.filename, pi.create_time, pi.width, pi.height,
			COALESCE(pi.size, 0) AS size, sm.client_key, COALESCE(NULLIF(p.display_name, ''), sm.client_key) AS account
		FROM photos_picked_items pi JOIN scans s ON s.id = pi.scan_id
		JOIN scanmetadata sm ON sm.scan_id = s.id
		JOIN privatetokens p ON p.client_key = sm.client_key AND p.user_id = $1
		WHERE s.user_id = $1 AND pi.create_time IS NOT NULL
		ORDER BY pi.media_item_id, pi.scan_id DESC
	)
	SELECT pk.*, f.source, s.label, `+dupItemSQL+` AS item, f.path, `+dupFolderSQL+` AS folder, f.size AS file_size,
		f.modified, f.owned,
		CASE WHEN f.capture_time IS NOT NULL THEN 'name and capture time' ELSE 'name and modified time' END
		|| CASE WHEN f.width IS NOT NULL AND pk.width IS NOT NULL THEN ', dimensions' ELSE '' END AS how
	FROM picks pk
	JOIN dup_files f ON lower(`+dupNameSQL+`) = lower(pk.filename)
	JOIN dup_sources s ON s.source = f.source
	WHERE CASE
		WHEN f.capture_time IS NOT NULL THEN
			abs(extract(epoch FROM f.capture_time - (pk.create_time AT TIME ZONE 'UTC'))) <= 14 * 3600 + 60
			AND abs(abs(extract(epoch FROM f.capture_time - (pk.create_time AT TIME ZONE 'UTC')))
				- round(abs(extract(epoch FROM f.capture_time - (pk.create_time AT TIME ZONE 'UTC'))) / 900) * 900) <= 60
		ELSE f.modified IS NOT NULL AND abs(extract(epoch FROM f.modified - pk.create_time)) <= 60
	END
	AND (f.width IS NULL OR pk.width IS NULL OR (f.width = pk.width AND f.height = pk.height)
		OR (f.width = pk.height AND f.height = pk.width))`, userID)
	if err != nil {
		return fmt.Errorf("failed to match photos: %w", err)
	}
	statements := []string{
		`CREATE TEMP TABLE dup_photo_groups ON COMMIT DROP AS
		SELECT nextval(pg_get_serial_sequence('dup_groups', 'id')) AS id, media_item_id, min(client_key) AS client_key,
			min(account) AS account, min(filename) AS filename, max(size) AS size, max(create_time) AS create_time,
			COALESCE(NULLIF(max(size), 0), max(file_size)) AS group_size, count(*) + 1 AS copies,
			array_agg(DISTINCT source ORDER BY source) || ('google:' || min(client_key) || ':photos') AS sources,
			string_agg(DISTINCT how, '; ') AS match
		FROM dup_photo_matches GROUP BY media_item_id`,
		`INSERT INTO dup_groups (id, user_id, kind, key, name, size, files, copies, reclaimable, same_physical, sources, match)
		SELECT id, $1, 'photo', 'photo:' || media_item_id, filename, group_size, 1, copies, 0, false, sources, match
		FROM dup_photo_groups`,
		// The photo itself, then what it matched.
		`INSERT INTO dup_members (group_id, source, label, item, path, folder, size, modified)
		SELECT id, 'google:' || client_key || ':photos', account || ' · Google Photos',
			media_item_id, filename, '', size, create_time
		FROM dup_photo_groups`,
		`INSERT INTO dup_members (group_id, source, label, item, path, folder, size, modified, shared)
		SELECT g.id, m.source, m.label, m.item, m.path, m.folder, m.file_size, m.modified, NOT m.owned
		FROM dup_photo_matches m JOIN dup_photo_groups g ON g.media_item_id = m.media_item_id`,
	}
	for _, stmt := range statements {
		var args []any
		if strings.Contains(stmt, "$1") {
			args = []any{userID}
		}
		if _, err := tx.Exec(stmt, args...); err != nil {
			return fmt.Errorf("failed to write the likely duplicate photos: %w", err)
		}
	}
	return nil
}

// DupSourceTotal is what a source holds in duplicate files: its copies'
// bytes and count, in groups not all on one physical drive.
type DupSourceTotal struct {
	Source string `json:"source"`
	Label  string `json:"label"`
	Bytes  int64  `json:"bytes"`
	Files  int64  `json:"files"`
}

// DupSummary is the top of the Duplicates page.
type DupSummary struct {
	Reclaimable  int64            `json:"reclaimable"`
	Groups       map[string]int64 `json:"groups"`
	BySource     []DupSourceTotal `json:"by_source"`
	Uncomparable []Uncomparable   `json:"uncomparable"`
	BuiltAt      *time.Time       `json:"built_at"`
	// A build is running, or about to: the first, before the builder has
	// reached the user.
	Updating bool  `json:"updating"`
	TookMs   int64 `json:"took_ms"`
	// The last build's failure, if it failed: the index shown, if any, is
	// older.
	Error    string     `json:"error,omitempty"`
	FailedAt *time.Time `json:"failed_at,omitempty"`
}

// dupCounts is dup_state.counts: per kind, its groups, those not all on one
// physical drive, those across sources, and the reclaimable bytes.
type dupCounts map[string]struct {
	All              int64 `json:"all"`
	HideSamePhysical int64 `json:"hide_same_physical"`
	Across           int64 `json:"across"`
	Reclaimable      int64 `json:"reclaimable"`
}

// GetDupSummary returns userID's summary; BuiltAt is nil until the first
// build.
func GetDupSummary(userID int64) (DupSummary, error) {
	summary := DupSummary{Groups: map[string]int64{DupFile: 0, DupFolder: 0, DupPhoto: 0},
		BySource: []DupSourceTotal{}, Uncomparable: []Uncomparable{}}
	var state struct {
		BuiltAt      *time.Time `db:"built_at"`
		Building     bool       `db:"building"`
		TookMs       int64      `db:"took_ms"`
		Uncomparable []byte     `db:"uncomparable"`
		BySource     []byte     `db:"by_source"`
		Counts       []byte     `db:"counts"`
		Error        string     `db:"error"`
		FailedAt     *time.Time `db:"failed_at"`
	}
	err := db.Get(&state, `SELECT built_at, building, took_ms, uncomparable, by_source, counts, error, failed_at
		FROM dup_state WHERE user_id = $1`, userID)
	if errors.Is(err, sql.ErrNoRows) {
		summary.Updating = true
		return summary, nil
	}
	if err != nil {
		return DupSummary{}, fmt.Errorf("failed to read the duplicates index's state: %w", err)
	}
	summary.BuiltAt, summary.TookMs, summary.Updating = state.BuiltAt, state.TookMs, state.Building
	summary.Error, summary.FailedAt = state.Error, state.FailedAt
	if err := json.Unmarshal(state.Uncomparable, &summary.Uncomparable); err != nil {
		return DupSummary{}, err
	}
	if err := json.Unmarshal(state.BySource, &summary.BySource); err != nil {
		return DupSummary{}, err
	}
	var counts dupCounts
	if err := json.Unmarshal(state.Counts, &counts); err != nil {
		return DupSummary{}, err
	}
	for kind, c := range counts {
		summary.Groups[kind] = c.All
	}
	// Folders' bytes are their files', already counted.
	summary.Reclaimable = counts[DupFile].Reclaimable
	return summary, nil
}

// DupFilter picks the groups of a page.
type DupFilter struct {
	Kind             string
	Source           string // only groups with a copy here; "" for all
	Across           bool   // only groups in two or more sources
	MinSize          int64
	HideSamePhysical bool
}

// DupGroupsPageSize and DupMembersPageSize are the page sizes; a page of
// groups carries up to DupMembersShown copies of each.
const (
	DupGroupsPageSize  = 50
	DupMembersShown    = 10
	DupMembersPageSize = 200
)

type DupMember struct {
	Source        string     `db:"source" json:"source"`
	Label         string     `db:"label" json:"label"`
	Item          string     `db:"item" json:"item"`
	Path          string     `db:"path" json:"path"`
	Folder        string     `db:"folder" json:"folder"`
	Size          int64      `db:"size" json:"size"`
	Files         int64      `db:"files" json:"files"`
	Modified      *time.Time `db:"modified" json:"modified"`
	PhysicalDrive *int64     `db:"physical_drive" json:"physical_drive"`
	// Shared with the user, not theirs: it doesn't count as reclaimable.
	Shared bool `db:"shared" json:"shared"`
}

// DupGroup is a group of copies. Its ID changes when the index is rebuilt;
// its kind and key don't, so groups are addressed by those.
type DupGroup struct {
	Id           int64          `db:"id" json:"-"`
	Kind         string         `db:"kind" json:"kind"`
	Key          string         `db:"key" json:"key"`
	Name         string         `db:"name" json:"name"`
	Size         int64          `db:"size" json:"size"`
	Files        int64          `db:"files" json:"files"`
	Copies       int64          `db:"copies" json:"copies"`
	Reclaimable  int64          `db:"reclaimable" json:"reclaimable"`
	SamePhysical bool           `db:"same_physical" json:"same_physical"`
	Sources      pq.StringArray `db:"sources" json:"sources"`
	Match        string         `db:"match" json:"match"`
	Members      []DupMember    `db:"-" json:"members"`
}

type DupGroupsPage struct {
	Groups   []DupGroup        `json:"groups"`
	Labels   map[string]string `json:"labels"` // the sources' names
	Total    int64             `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
}

const memberColumns = `m.source, m.label, m.item, m.path, m.folder, m.size, m.files, m.modified, m.physical_drive, m.shared`

// GetDupGroups returns a page of userID's groups, largest reclaimable
// first (photos, which reclaim nothing, largest first).
func GetDupGroups(userID int64, filter DupFilter, pageNo int) (DupGroupsPage, error) {
	page := DupGroupsPage{Groups: []DupGroup{}, Labels: map[string]string{}, Page: max(pageNo, 1),
		PageSize: DupGroupsPageSize}
	where := `user_id = $1 AND kind = $2 AND size >= $3`
	args := []any{userID, filter.Kind, filter.MinSize}
	if filter.Source != "" {
		args = append(args, pq.Array([]string{filter.Source}))
		where += fmt.Sprintf(` AND sources @> $%d`, len(args))
	}
	if filter.Across {
		where += ` AND cardinality(sources) > 1 AND NOT same_physical`
	}
	if filter.HideSamePhysical {
		where += ` AND NOT same_physical`
	}
	if err := countDupGroups(userID, filter, where, args, &page.Total); err != nil {
		return DupGroupsPage{}, err
	}
	args = append(args, DupGroupsPageSize, (page.Page-1)*DupGroupsPageSize)
	if err := db.Select(&page.Groups, fmt.Sprintf(`SELECT id, kind, key, name, size, files, copies, reclaimable,
			same_physical, sources, match
		FROM dup_groups WHERE %s ORDER BY reclaimable DESC, size DESC, id LIMIT $%d OFFSET $%d`,
		where, len(args)-1, len(args)), args...); err != nil {
		return DupGroupsPage{}, fmt.Errorf("failed to read duplicate groups: %w", err)
	}
	if len(page.Groups) == 0 {
		return page, nil
	}
	ids := make([]int64, len(page.Groups))
	index := map[int64]int{}
	for i, g := range page.Groups {
		ids[i], index[g.Id] = g.Id, i
		page.Groups[i].Members = []DupMember{}
	}
	var members []struct {
		GroupId int64 `db:"group_id"`
		DupMember
	}
	if err := db.Select(&members, `SELECT m.group_id, `+memberColumns+`
		FROM unnest($1::int8[]) AS g(id)
		CROSS JOIN LATERAL (SELECT * FROM dup_members m WHERE m.group_id = g.id
			ORDER BY `+memberOrder+` LIMIT $2) m`, pq.Array(ids), DupMembersShown); err != nil {
		return DupGroupsPage{}, fmt.Errorf("failed to read duplicate copies: %w", err)
	}
	for _, m := range members {
		g := &page.Groups[index[m.GroupId]]
		g.Members = append(g.Members, m.DupMember)
	}
	var labels []struct {
		Source string `db:"source"`
		Label  string `db:"label"`
	}
	if err := db.Select(&labels, `SELECT DISTINCT ON (source) source, label FROM dup_members
		WHERE group_id = ANY($1) ORDER BY source`, pq.Array(ids)); err != nil {
		return DupGroupsPage{}, fmt.Errorf("failed to read duplicate sources: %w", err)
	}
	for _, l := range labels {
		page.Labels[l.Source] = l.Label
	}
	return page, nil
}

// countDupGroups counts the groups a filter picks: from the counts the
// build kept, unless it limits the source or size.
func countDupGroups(userID int64, filter DupFilter, where string, args []any, total *int64) error {
	if filter.Source == "" && filter.MinSize == 0 {
		var encoded []byte
		err := db.Get(&encoded, `SELECT counts FROM dup_state WHERE user_id = $1`, userID)
		if errors.Is(err, sql.ErrNoRows) {
			*total = 0
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to read the duplicates' counts: %w", err)
		}
		var counts dupCounts
		if err := json.Unmarshal(encoded, &counts); err != nil {
			return err
		}
		c := counts[filter.Kind]
		switch {
		case filter.Across:
			*total = c.Across
		case filter.HideSamePhysical:
			*total = c.HideSamePhysical
		default:
			*total = c.All
		}
		return nil
	}
	if err := db.Get(total, `SELECT count(*) FROM dup_groups WHERE `+where, args...); err != nil {
		return fmt.Errorf("failed to count duplicate groups: %w", err)
	}
	return nil
}

// memberOrder lists a group's copies: Google Photos' item first, then by
// source and path.
const memberOrder = `(m.source LIKE '%:photos') DESC, m.source, m.path, m.item`

type DupMembersPage struct {
	Members  []DupMember `json:"members"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

// GetDupMembers returns a page of the copies of userID's group of kind and
// key; a group that isn't in their index (any longer) is ErrNotFound.
func GetDupMembers(userID int64, kind string, key string, pageNo int) (DupMembersPage, error) {
	page := DupMembersPage{Members: []DupMember{}, Page: max(pageNo, 1), PageSize: DupMembersPageSize}
	var group struct {
		Id     int64 `db:"id"`
		Copies int64 `db:"copies"`
	}
	err := db.Get(&group, `SELECT id, copies FROM dup_groups WHERE user_id = $1 AND kind = $2 AND key = $3`,
		userID, kind, key)
	if errors.Is(err, sql.ErrNoRows) {
		return DupMembersPage{}, ErrNotFound
	}
	if err != nil {
		return DupMembersPage{}, fmt.Errorf("failed to read duplicate group %s: %w", key, err)
	}
	page.Total = group.Copies
	if err := db.Select(&page.Members, `SELECT `+memberColumns+`
		FROM dup_members m WHERE m.group_id = $1 ORDER BY `+memberOrder+` LIMIT $2 OFFSET $3`,
		group.Id, DupMembersPageSize, (page.Page-1)*DupMembersPageSize); err != nil {
		return DupMembersPage{}, fmt.Errorf("failed to read duplicate group %s's copies: %w", key, err)
	}
	return page, nil
}
