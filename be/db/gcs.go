package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Google Cloud Storage: each linked account's living record of its buckets
// and objects, updated by every scan, and what each scan found. See
// docs/archive/gcs-scans.md, "A living record per bucket".

// An object version's states.
const (
	GcsLive        = "live"
	GcsNoncurrent  = "noncurrent"
	GcsSoftDeleted = "soft_deleted"
)

// What a scan did with a bucket.
const (
	GcsBucketCompleted = "completed"
	GcsBucketFailed    = "failed"
	GcsBucketSkipped   = "skipped" // Requester Pays
)

func migrateGcs() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS gcs_buckets (
			client_key       VARCHAR(100) NOT NULL,
			bucket           TEXT NOT NULL,
			project_id       TEXT NOT NULL,
			location         TEXT NOT NULL DEFAULT '',
			storage_class    TEXT NOT NULL DEFAULT '',
			versioning       BOOLEAN NOT NULL DEFAULT false,
			soft_delete_days INT,
			last_scan_id     INT REFERENCES scans (id) ON DELETE SET NULL,
			updated_at       TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (client_key, bucket)
		)`,
		`CREATE TABLE IF NOT EXISTS gcs_objects (
			client_key       VARCHAR(100) NOT NULL,
			bucket           TEXT NOT NULL,
			name             TEXT NOT NULL,
			generation       BIGINT NOT NULL,
			state            VARCHAR(12) NOT NULL,
			parent           TEXT NOT NULL,
			size             BIGINT NOT NULL,
			storage_class    TEXT NOT NULL,
			md5hash          TEXT NOT NULL DEFAULT '',
			crc32c           TEXT NOT NULL DEFAULT '',
			updated          TIMESTAMPTZ,
			time_deleted     TIMESTAMPTZ,
			hard_delete_time TIMESTAMPTZ,
			seen_scan_id     INT NOT NULL,
			PRIMARY KEY (client_key, bucket, name, generation, state)
		)`,
		`CREATE INDEX IF NOT EXISTS gcs_objects_children ON gcs_objects (client_key, bucket, parent)`,
		`CREATE TABLE IF NOT EXISTS gcs_prefix_totals (
			client_key           VARCHAR(100) NOT NULL,
			bucket               TEXT NOT NULL,
			prefix               TEXT NOT NULL,
			parent               TEXT,
			live_objects         BIGINT NOT NULL,
			live_bytes           BIGINT NOT NULL,
			noncurrent_objects   BIGINT NOT NULL,
			noncurrent_bytes     BIGINT NOT NULL,
			soft_deleted_objects BIGINT NOT NULL,
			soft_deleted_bytes   BIGINT NOT NULL,
			bytes_by_class       JSONB NOT NULL,
			PRIMARY KEY (client_key, bucket, prefix)
		)`,
		`CREATE INDEX IF NOT EXISTS gcs_prefix_totals_children ON gcs_prefix_totals (client_key, bucket, parent)`,
		`CREATE TABLE IF NOT EXISTS gcs_scan_buckets (
			scan_id              INT NOT NULL REFERENCES scans (id),
			bucket               TEXT NOT NULL,
			project_id           TEXT NOT NULL,
			prefix               TEXT NOT NULL DEFAULT '',
			status               TEXT NOT NULL,
			error                TEXT NOT NULL DEFAULT '',
			live_objects         BIGINT NOT NULL DEFAULT 0,
			live_bytes           BIGINT NOT NULL DEFAULT 0,
			noncurrent_objects   BIGINT NOT NULL DEFAULT 0,
			noncurrent_bytes     BIGINT NOT NULL DEFAULT 0,
			soft_deleted_objects BIGINT NOT NULL DEFAULT 0,
			soft_deleted_bytes   BIGINT NOT NULL DEFAULT 0,
			bytes_by_class       JSONB NOT NULL DEFAULT '{}',
			PRIMARY KEY (scan_id, bucket)
		)`,
	}
	for _, s := range statements {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("failed to create the Cloud Storage tables: %w", err)
		}
	}
	return nil
}

// GcsObject is one version of an object, as the record keeps it.
type GcsObject struct {
	Name           string     `db:"name" json:"name"`
	Generation     int64      `db:"generation" json:"generation"`
	State          string     `db:"state" json:"state"`
	Size           int64      `db:"size" json:"size"`
	StorageClass   string     `db:"storage_class" json:"storage_class"`
	Md5Hash        string     `db:"md5hash" json:"md5"` // hex; '' for composite and CMEK objects
	Crc32c         string     `db:"crc32c" json:"crc32c"`
	Updated        *time.Time `db:"updated" json:"updated"`
	TimeDeleted    *time.Time `db:"time_deleted" json:"time_deleted"`
	HardDeleteTime *time.Time `db:"hard_delete_time" json:"hard_delete_time"`
}

// GcsParent is the prefix an object sits under: its name up to and
// including the last "/", or "" at the bucket's root.
func GcsParent(name string) string {
	return name[:strings.LastIndex(name, "/")+1]
}

// timeText is t for an array parameter, "" for none.
func timeText(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// UpsertGcsObjects adds or updates object versions in the account's record,
// marking them seen by scanId.
func UpsertGcsObjects(clientKey string, bucket string, scanId int, objects []GcsObject) error {
	if len(objects) == 0 {
		return nil
	}
	n := len(objects)
	names, parents, states, classes, md5s, crcs := make([]string, n), make([]string, n), make([]string, n),
		make([]string, n), make([]string, n), make([]string, n)
	updated, deleted, hardDelete := make([]string, n), make([]string, n), make([]string, n)
	generations, sizes := make([]int64, n), make([]int64, n)
	for i, o := range objects {
		names[i], parents[i], states[i], classes[i] = o.Name, GcsParent(o.Name), o.State, o.StorageClass
		md5s[i], crcs[i], generations[i], sizes[i] = o.Md5Hash, o.Crc32c, o.Generation, o.Size
		updated[i], deleted[i], hardDelete[i] = timeText(o.Updated), timeText(o.TimeDeleted), timeText(o.HardDeleteTime)
	}
	_, err := db.Exec(`INSERT INTO gcs_objects (client_key, bucket, name, generation, state, parent, size,
			storage_class, md5hash, crc32c, updated, time_deleted, hard_delete_time, seen_scan_id)
		SELECT $1, $2, u.name, u.generation, u.state, u.parent, u.size, u.class, u.md5, u.crc,
			NULLIF(u.updated, '')::timestamptz, NULLIF(u.deleted, '')::timestamptz,
			NULLIF(u.hard_delete, '')::timestamptz, $3
		FROM unnest($4::text[], $5::bigint[], $6::text[], $7::text[], $8::bigint[], $9::text[], $10::text[],
			$11::text[], $12::text[], $13::text[], $14::text[])
			AS u(name, generation, state, parent, size, class, md5, crc, updated, deleted, hard_delete)
		ON CONFLICT (client_key, bucket, name, generation, state) DO UPDATE SET
			size = EXCLUDED.size, storage_class = EXCLUDED.storage_class, md5hash = EXCLUDED.md5hash,
			crc32c = EXCLUDED.crc32c, updated = EXCLUDED.updated, time_deleted = EXCLUDED.time_deleted,
			hard_delete_time = EXCLUDED.hard_delete_time, seen_scan_id = EXCLUDED.seen_scan_id`,
		clientKey, bucket, scanId, pq.Array(names), pq.Array(generations), pq.Array(states), pq.Array(parents),
		pq.Array(sizes), pq.Array(classes), pq.Array(md5s), pq.Array(crcs), pq.Array(updated), pq.Array(deleted),
		pq.Array(hardDelete))
	if err != nil {
		return fmt.Errorf("failed to save objects of %s: %w", bucket, err)
	}
	return nil
}

// DeleteUnseenGcsObjects deletes from the record what scanId didn't see in
// its scope: the bucket's objects under prefix, in the states it listed.
func DeleteUnseenGcsObjects(clientKey string, bucket string, scanId int, prefix string, states []string) (int64, error) {
	res, err := db.Exec(`DELETE FROM gcs_objects
		WHERE client_key = $1 AND bucket = $2 AND left(name, length($3)) = $3
			AND state = ANY($4) AND seen_scan_id <> $5`,
		clientKey, bucket, prefix, pq.Array(states), scanId)
	if err != nil {
		return 0, fmt.Errorf("failed to delete what scan %d didn't see in %s: %w", scanId, bucket, err)
	}
	return res.RowsAffected()
}

// RebuildGcsPrefixTotals recomputes a bucket's folder totals from the
// record: one row per prefix, with what's under it by state, and the live
// bytes by storage class. Each object counts towards its parent prefix and
// every prefix above that.
func RebuildGcsPrefixTotals(clientKey string, bucket string) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM gcs_prefix_totals WHERE client_key = $1 AND bucket = $2`, clientKey, bucket); err != nil {
		return fmt.Errorf("failed to clear the totals of %s: %w", bucket, err)
	}
	// A parent "a/b/" has segments {a, b}, so the prefixes above an object
	// are "", "a/", "a/b/", each with the one before as its parent.
	_, err = tx.Exec(`INSERT INTO gcs_prefix_totals (client_key, bucket, prefix, parent,
			live_objects, live_bytes, noncurrent_objects, noncurrent_bytes,
			soft_deleted_objects, soft_deleted_bytes, bytes_by_class)
		SELECT $1, $2, prefix, parent,
			COALESCE(sum(n) FILTER (WHERE state = 'live'), 0), COALESCE(sum(bytes) FILTER (WHERE state = 'live'), 0),
			COALESCE(sum(n) FILTER (WHERE state = 'noncurrent'), 0), COALESCE(sum(bytes) FILTER (WHERE state = 'noncurrent'), 0),
			COALESCE(sum(n) FILTER (WHERE state = 'soft_deleted'), 0), COALESCE(sum(bytes) FILTER (WHERE state = 'soft_deleted'), 0),
			COALESCE(jsonb_object_agg(storage_class, bytes) FILTER (WHERE state = 'live'), '{}')
		FROM (
			SELECT a.prefix, a.parent, o.state, o.storage_class, count(*) AS n, sum(o.size) AS bytes
			FROM gcs_objects o
			CROSS JOIN LATERAL (
				SELECT CASE WHEN o.parent = '' THEN '{}'::text[]
					ELSE string_to_array(left(o.parent, -1), '/') END AS segs
			) s
			CROSS JOIN LATERAL (
				SELECT CASE WHEN k = 0 THEN '' ELSE array_to_string(s.segs[1:k], '/') || '/' END AS prefix,
					CASE WHEN k = 0 THEN NULL WHEN k = 1 THEN ''
						ELSE array_to_string(s.segs[1:k-1], '/') || '/' END AS parent
				FROM generate_series(0, cardinality(s.segs)) AS k
			) a
			WHERE o.client_key = $1 AND o.bucket = $2
			GROUP BY 1, 2, 3, 4
		) g
		GROUP BY prefix, parent`, clientKey, bucket)
	if err != nil {
		return fmt.Errorf("failed to add up the totals of %s: %w", bucket, err)
	}
	return tx.Commit()
}

// GcsBucketRecord is a bucket in an account's record.
type GcsBucketRecord struct {
	Bucket         string `db:"bucket"`
	ProjectId      string `db:"project_id"`
	Location       string `db:"location"`
	StorageClass   string `db:"storage_class"`
	Versioning     bool   `db:"versioning"`
	SoftDeleteDays *int   `db:"soft_delete_days"`
}

// SaveGcsBucket records a bucket a scan has just completed, with its
// settings.
func SaveGcsBucket(clientKey string, b GcsBucketRecord, scanId int) error {
	_, err := db.Exec(`INSERT INTO gcs_buckets (client_key, bucket, project_id, location, storage_class,
			versioning, soft_delete_days, last_scan_id, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		ON CONFLICT (client_key, bucket) DO UPDATE SET project_id = EXCLUDED.project_id,
			location = EXCLUDED.location, storage_class = EXCLUDED.storage_class,
			versioning = EXCLUDED.versioning, soft_delete_days = EXCLUDED.soft_delete_days,
			last_scan_id = EXCLUDED.last_scan_id, updated_at = EXCLUDED.updated_at`,
		clientKey, b.Bucket, b.ProjectId, b.Location, b.StorageClass, b.Versioning, b.SoftDeleteDays, scanId)
	if err != nil {
		return fmt.Errorf("failed to record bucket %s: %w", b.Bucket, err)
	}
	return nil
}

// GcsTotals are objects and bytes by state, and live bytes by class.
type GcsTotals struct {
	LiveObjects        int64            `db:"live_objects" json:"live_objects"`
	LiveBytes          int64            `db:"live_bytes" json:"live_bytes"`
	NoncurrentObjects  int64            `db:"noncurrent_objects" json:"noncurrent_objects"`
	NoncurrentBytes    int64            `db:"noncurrent_bytes" json:"noncurrent_bytes"`
	SoftDeletedObjects int64            `db:"soft_deleted_objects" json:"soft_deleted_objects"`
	SoftDeletedBytes   int64            `db:"soft_deleted_bytes" json:"soft_deleted_bytes"`
	BytesByClass       map[string]int64 `db:"-" json:"bytes_by_class"`
}

// Add counts one object version.
func (t *GcsTotals) Add(state string, class string, size int64) {
	switch state {
	case GcsLive:
		t.LiveObjects, t.LiveBytes = t.LiveObjects+1, t.LiveBytes+size
		if t.BytesByClass == nil {
			t.BytesByClass = map[string]int64{}
		}
		t.BytesByClass[class] += size
	case GcsNoncurrent:
		t.NoncurrentObjects, t.NoncurrentBytes = t.NoncurrentObjects+1, t.NoncurrentBytes+size
	case GcsSoftDeleted:
		t.SoftDeletedObjects, t.SoftDeletedBytes = t.SoftDeletedObjects+1, t.SoftDeletedBytes+size
	}
}

// GcsScanBucket is what a scan did with one bucket.
type GcsScanBucket struct {
	Bucket    string `db:"bucket" json:"bucket"`
	ProjectId string `db:"project_id" json:"project_id"`
	Prefix    string `db:"prefix" json:"prefix"`
	Status    string `db:"status" json:"status"`
	Error     string `db:"error" json:"error"`
	GcsTotals
	ByClass []byte `db:"bytes_by_class" json:"-"`
}

// SaveGcsScanBucket records what scanId did with a bucket.
func SaveGcsScanBucket(scanId int, b GcsScanBucket) error {
	byClass, err := json.Marshal(b.BytesByClass)
	if err != nil || b.BytesByClass == nil {
		byClass = []byte("{}")
	}
	_, err = db.Exec(`INSERT INTO gcs_scan_buckets (scan_id, bucket, project_id, prefix, status, error,
			live_objects, live_bytes, noncurrent_objects, noncurrent_bytes, soft_deleted_objects,
			soft_deleted_bytes, bytes_by_class)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (scan_id, bucket) DO UPDATE SET status = EXCLUDED.status, error = EXCLUDED.error,
			live_objects = EXCLUDED.live_objects, live_bytes = EXCLUDED.live_bytes,
			noncurrent_objects = EXCLUDED.noncurrent_objects, noncurrent_bytes = EXCLUDED.noncurrent_bytes,
			soft_deleted_objects = EXCLUDED.soft_deleted_objects, soft_deleted_bytes = EXCLUDED.soft_deleted_bytes,
			bytes_by_class = EXCLUDED.bytes_by_class`,
		scanId, b.Bucket, b.ProjectId, b.Prefix, b.Status, b.Error, b.LiveObjects, b.LiveBytes,
		b.NoncurrentObjects, b.NoncurrentBytes, b.SoftDeletedObjects, b.SoftDeletedBytes, byClass)
	if err != nil {
		return fmt.Errorf("failed to record scan %d of bucket %s: %w", scanId, b.Bucket, err)
	}
	return nil
}

// GcsScanBuckets returns what a scan did with each bucket, by name. The
// caller checks the scan's owner.
func GcsScanBuckets(scanId int) ([]GcsScanBucket, error) {
	buckets := []GcsScanBucket{}
	if err := db.Select(&buckets, `SELECT bucket, project_id, prefix, status, error, live_objects, live_bytes,
			noncurrent_objects, noncurrent_bytes, soft_deleted_objects, soft_deleted_bytes, bytes_by_class
		FROM gcs_scan_buckets WHERE scan_id = $1 ORDER BY bucket`, scanId); err != nil {
		return nil, fmt.Errorf("failed to get the buckets of scan %d: %w", scanId, err)
	}
	for i := range buckets {
		if err := json.Unmarshal(buckets[i].ByClass, &buckets[i].BytesByClass); err != nil {
			return nil, fmt.Errorf("failed to read bytes by class of %s: %w", buckets[i].Bucket, err)
		}
	}
	return buckets, nil
}

// Browse: an account's buckets, then prefixes as folders, from the record.
// A folder ID is "<bucket>/<prefix>": "jyo-pics/" is the bucket's root,
// "jyo-pics/2024/" a folder in it, and "" lists the buckets. See
// docs/archive/gcs-scans.md, "What Browse shows".

// gcsTotalsRow is a gcs_prefix_totals row.
type gcsTotalsRow struct {
	GcsTotals
	Prefix  string `db:"prefix"`
	ByClass []byte `db:"bytes_by_class"`
}

func (r *gcsTotalsRow) totals() *GcsTotals {
	t := r.GcsTotals
	json.Unmarshal(r.ByClass, &t.BytesByClass)
	return &t
}

// add sums another bucket's totals into t.
func (t *GcsTotals) add(o *GcsTotals) {
	t.LiveObjects += o.LiveObjects
	t.LiveBytes += o.LiveBytes
	t.NoncurrentObjects += o.NoncurrentObjects
	t.NoncurrentBytes += o.NoncurrentBytes
	t.SoftDeletedObjects += o.SoftDeletedObjects
	t.SoftDeletedBytes += o.SoftDeletedBytes
	for class, bytes := range o.BytesByClass {
		if t.BytesByClass == nil {
			t.BytesByClass = map[string]int64{}
		}
		t.BytesByClass[class] += bytes
	}
}

const gcsTotalsColumns = `live_objects, live_bytes, noncurrent_objects, noncurrent_bytes,
	soft_deleted_objects, soft_deleted_bytes, bytes_by_class`

func gcsServiceTotals(clientKey string) (ServiceTotals, error) {
	var row struct {
		Buckets   int64      `db:"buckets"`
		Files     int64      `db:"files"`
		Bytes     int64      `db:"bytes"`
		UpdatedAt *time.Time `db:"updated_at"`
	}
	err := db.Get(&row, `SELECT count(*) AS buckets, COALESCE(sum(t.live_objects), 0) AS files,
			COALESCE(sum(t.live_bytes), 0) AS bytes, max(b.updated_at) AS updated_at
		FROM gcs_buckets b LEFT JOIN gcs_prefix_totals t
			ON t.client_key = b.client_key AND t.bucket = b.bucket AND t.prefix = ''
		WHERE b.client_key = $1`, clientKey)
	if err != nil {
		return ServiceTotals{}, fmt.Errorf("failed to add up the Cloud Storage of %s: %w", clientKey, err)
	}
	if row.Buckets == 0 {
		return ServiceTotals{}, nil
	}
	return ServiceTotals{Files: &row.Files, Bytes: &row.Bytes, UpdatedAt: row.UpdatedAt}, nil
}

// GcsChildren returns a page of a Cloud Storage folder of clientKey: the
// buckets for "", else a prefix's subfolders, then its object versions,
// each largest first. ErrNotFound for a bucket or prefix not in the record.
func GcsChildren(clientKey string, folder string, pageNo int) (FolderPage, error) {
	page := FolderPage{Path: []PathPart{}, Page: max(pageNo, 1)}
	if folder == "" {
		return gcsBuckets(clientKey, page)
	}
	bucket, prefix, ok := strings.Cut(folder, "/")
	if !ok || (prefix != "" && !strings.HasSuffix(prefix, "/")) {
		return FolderPage{}, ErrNotFound
	}
	var own gcsTotalsRow
	err := db.Get(&own, `SELECT prefix, `+gcsTotalsColumns+` FROM gcs_prefix_totals
		WHERE client_key = $1 AND bucket = $2 AND prefix = $3`, clientKey, bucket, prefix)
	if errors.Is(err, sql.ErrNoRows) {
		return FolderPage{}, ErrNotFound
	}
	if err != nil {
		return FolderPage{}, fmt.Errorf("failed to get the totals of %s: %w", folder, err)
	}
	page.Totals = FolderTotals{Files: own.LiveObjects, Bytes: own.LiveBytes}
	page.Gcs = own.totals()

	page.Path = append(page.Path, PathPart{Id: bucket + "/", Name: bucket})
	if prefix != "" {
		at := ""
		for _, segment := range strings.Split(strings.TrimSuffix(prefix, "/"), "/") {
			at += segment + "/"
			page.Path = append(page.Path, PathPart{Id: bucket + "/" + at, Name: gcsFolderName(segment)})
		}
	}

	var subs []gcsTotalsRow
	if err := db.Select(&subs, `SELECT prefix, `+gcsTotalsColumns+` FROM gcs_prefix_totals
		WHERE client_key = $1 AND bucket = $2 AND parent = $3`, clientKey, bucket, prefix); err != nil {
		return FolderPage{}, fmt.Errorf("failed to list folders in %s: %w", folder, err)
	}
	folders := make([]BrowseFolder, len(subs))
	for i, s := range subs {
		folders[i] = BrowseFolder{Id: bucket + "/" + s.Prefix,
			Name:         gcsFolderName(strings.TrimSuffix(strings.TrimPrefix(s.Prefix, prefix), "/")),
			FolderTotals: FolderTotals{Files: s.LiveObjects, Bytes: s.LiveBytes}}
	}
	var files int
	if err := db.Get(&files, `SELECT count(*) FROM gcs_objects WHERE client_key = $1 AND bucket = $2 AND parent = $3`,
		clientKey, bucket, prefix); err != nil {
		return FolderPage{}, fmt.Errorf("failed to count objects in %s: %w", folder, err)
	}
	offset, limit := pageFolders(&page, folders, files)
	if limit > 0 && offset < files {
		if err := db.Select(&page.Files, `SELECT name || '#' || generation || '/' || state AS id,
				substr(name, length($3) + 1) AS name, size, updated AS modified, '' AS mime_type, '' AS error,
				storage_class, state
			FROM gcs_objects WHERE client_key = $1 AND bucket = $2 AND parent = $3
			ORDER BY size DESC, name, generation DESC, state LIMIT $4 OFFSET $5`,
			clientKey, bucket, prefix, limit, offset); err != nil {
			return FolderPage{}, fmt.Errorf("failed to list objects in %s: %w", folder, err)
		}
	}
	return page, nil
}

// gcsFolderName is how a prefix's last segment reads; "a//b" has an empty one.
func gcsFolderName(segment string) string {
	if segment == "" {
		return "(empty name)"
	}
	return segment
}

// gcsBuckets is the buckets page: each bucket as a folder, with its
// location and class, and when a scan last completed it.
func gcsBuckets(clientKey string, page FolderPage) (FolderPage, error) {
	var rows []struct {
		gcsTotalsRow
		Bucket       string    `db:"bucket"`
		Location     string    `db:"location"`
		StorageClass string    `db:"storage_class"`
		UpdatedAt    time.Time `db:"updated_at"`
	}
	if err := db.Select(&rows, `SELECT b.bucket, b.location, b.storage_class, b.updated_at,
			COALESCE(t.prefix, '') AS prefix, COALESCE(t.live_objects, 0) AS live_objects,
			COALESCE(t.live_bytes, 0) AS live_bytes, COALESCE(t.noncurrent_objects, 0) AS noncurrent_objects,
			COALESCE(t.noncurrent_bytes, 0) AS noncurrent_bytes, COALESCE(t.soft_deleted_objects, 0) AS soft_deleted_objects,
			COALESCE(t.soft_deleted_bytes, 0) AS soft_deleted_bytes, COALESCE(t.bytes_by_class, '{}') AS bytes_by_class
		FROM gcs_buckets b LEFT JOIN gcs_prefix_totals t
			ON t.client_key = b.client_key AND t.bucket = b.bucket AND t.prefix = ''
		WHERE b.client_key = $1`, clientKey); err != nil {
		return FolderPage{}, fmt.Errorf("failed to list the buckets of %s: %w", clientKey, err)
	}
	all := &GcsTotals{}
	folders := make([]BrowseFolder, len(rows))
	for i, r := range rows {
		all.add(r.totals())
		detail := strings.Join(nonEmpty(r.Location, r.StorageClass), " · ")
		folders[i] = BrowseFolder{Id: r.Bucket + "/", Name: r.Bucket, Detail: detail,
			FolderTotals: FolderTotals{Files: r.LiveObjects, Bytes: r.LiveBytes}}
	}
	page.Totals = FolderTotals{Files: all.LiveObjects, Bytes: all.LiveBytes}
	page.Gcs = all
	pageFolders(&page, folders, 0)
	return page, nil
}

func nonEmpty(s ...string) []string {
	out := []string{}
	for _, x := range s {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}
