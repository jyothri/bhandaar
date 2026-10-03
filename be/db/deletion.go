package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/lib/pq"
)

// Deleting data from Manage data: a drive as uploaded from one box, one
// service's data of an account, or a whole Google account. Each runs as
// a job.
// See docs/archive/data-deletion.md.

// Kinds of deletion: a drive from one box, a whole account, or one
// service of an account (its name: "gmail", "drive", "gcs", "photos").
const (
	DeleteAgentDrive = "agent_drive"
	DeleteAccount    = "account"
)

// A service whose data can be deleted on its own: its scans' type, and
// its living records, each a statement on $1 (the client key) whose rows
// are counted under a name when it has one.
type deletableService struct {
	scanType string
	records  []struct{ count, stmt string }
}

// DeletableServices are the services Manage data can delete on their own.
var DeletableServices = map[string]deletableService{
	ServiceGmail: {scanType: "gmail"},
	ServiceDrive: {scanType: "google_drive", records: []struct{ count, stmt string }{
		{"drive_items", `DELETE FROM drive_items WHERE client_key = $1`},
		{"", `DELETE FROM drive_accounts WHERE client_key = $1`},
		{"", `DELETE FROM browse_folder_totals WHERE source = 'drive:' || $1`},
		{"", `DELETE FROM browse_totals_state WHERE source = 'drive:' || $1`},
	}},
	"gcs": {scanType: "gcs", records: []struct{ count, stmt string }{
		{"gcs_objects", `DELETE FROM gcs_objects WHERE client_key = $1`},
		{"", `DELETE FROM gcs_prefix_totals WHERE client_key = $1`},
		{"gcs_buckets", `DELETE FROM gcs_buckets WHERE client_key = $1`},
	}},
	"photos": {scanType: "google_photos", records: []struct{ count, stmt string }{
		{"", `DELETE FROM photos_picker_sessions WHERE client_key = $1`},
	}},
}

// Job statuses.
const (
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"
)

// ErrScanRunning is a deletion refused while one of the account's scans runs.
var ErrScanRunning = errors.New("a scan of this account is running")

// ErrAccountBeingDeleted is a scan refused while a deletion of its
// account's data is running.
var ErrAccountBeingDeleted = errors.New("this account's data is being deleted")

// How long finished jobs are kept.
const deletionJobsKeptFor = 30 * 24 * time.Hour

func migrateDeletions() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS deletions (
			id          BIGSERIAL PRIMARY KEY,
			user_id     BIGINT NOT NULL REFERENCES agent_users(id),
			kind        TEXT NOT NULL,
			target      TEXT NOT NULL,
			label       TEXT NOT NULL,
			status      TEXT NOT NULL,
			counts      JSONB NOT NULL DEFAULT '{}',
			revoke      TEXT NOT NULL DEFAULT '',
			error       TEXT NOT NULL DEFAULT '',
			started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
			finished_at TIMESTAMPTZ
		)`,
		// One running job per drive, and one per account whatever its kind
		// (the account, or one of its services).
		`DROP INDEX IF EXISTS deletions_running`,
		`CREATE UNIQUE INDEX IF NOT EXISTS deletions_running_target
			ON deletions ((kind = 'agent_drive'), target) WHERE status = 'running'`,
		// Every deletion is one transaction, so a job cut off by a restart
		// deleted nothing.
		`UPDATE deletions SET status = 'failed', error = 'Interrupted: the server stopped before it finished',
			finished_at = now() WHERE status = 'running'`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to add deletions to the schema: %w", err)
		}
	}
	return PurgeDeletionJobs()
}

// PurgeDeletionJobs deletes finished jobs older than 30 days.
func PurgeDeletionJobs() error {
	_, err := db.Exec(`DELETE FROM deletions WHERE status <> 'running'
		AND finished_at < now() - make_interval(secs => $1)`, deletionJobsKeptFor.Seconds())
	if err != nil {
		return fmt.Errorf("failed to purge old deletion jobs: %w", err)
	}
	return nil
}

// DeletionJob is a deletion and how it went.
type DeletionJob struct {
	ID     int64  `db:"id" json:"id"`
	Kind   string `db:"kind" json:"kind"`
	Target string `db:"target" json:"target"`
	Label  string `db:"label" json:"label"`
	Status string `db:"status" json:"status"`
	// What was deleted, e.g. {"files": 888902}.
	Counts json.RawMessage `db:"counts" json:"counts"`
	// For an account: "revoked", "already revoked", or why it failed.
	Revoke     string     `db:"revoke" json:"revoke,omitempty"`
	Error      string     `db:"error" json:"error,omitempty"`
	StartedAt  time.Time  `db:"started_at" json:"started_at"`
	FinishedAt *time.Time `db:"finished_at" json:"finished_at"`
}

const jobColumns = `id, kind, target, label, status, counts, revoke, error, started_at, finished_at`

// StartDeletion records a running job for userID, or returns the job
// already running for the same drive or account, with started false; that
// job may be of another kind (the account, or another of its services).
func StartDeletion(userID int64, kind string, target string, label string) (job DeletionJob, started bool, err error) {
	err = db.Get(&job, `INSERT INTO deletions (user_id, kind, target, label, status)
		VALUES ($1, $2, $3, $4, 'running') RETURNING `+jobColumns, userID, kind, target, label)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		err = db.Get(&job, `SELECT `+jobColumns+` FROM deletions
			WHERE (kind = 'agent_drive') = ($1 = 'agent_drive') AND target = $2 AND status = 'running'`, kind, target)
		return job, false, err
	}
	if err != nil {
		return DeletionJob{}, false, fmt.Errorf("failed to start deletion: %w", err)
	}
	return job, true, nil
}

// FinishDeletion records how a job ended: its counts, or err.
func FinishDeletion(id int64, counts map[string]int64, revoke string, jobErr error) error {
	status, message := JobDone, ""
	if jobErr != nil {
		status, message = JobFailed, jobErr.Error()
	}
	if counts == nil {
		counts = map[string]int64{}
	}
	encoded, err := json.Marshal(counts)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE deletions SET status = $2, counts = $3, revoke = $4, error = $5, finished_at = now()
		WHERE id = $1`, id, status, encoded, revoke, message)
	if err != nil {
		return fmt.Errorf("failed to record the end of deletion %d: %w", id, err)
	}
	WakeDuplicates()
	return nil
}

// GetDeletion returns userID's job; another user's is ErrNotFound.
func GetDeletion(userID int64, id int64) (DeletionJob, error) {
	var job DeletionJob
	err := db.Get(&job, `SELECT `+jobColumns+` FROM deletions WHERE id = $1 AND user_id = $2`, id, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return DeletionJob{}, ErrNotFound
	}
	return job, err
}

// runningJob is the job running for a target, if any.
func runningJob(kind string, target string) (*DeletionJob, error) {
	var job DeletionJob
	err := db.Get(&job, `SELECT `+jobColumns+` FROM deletions
		WHERE kind = $1 AND target = $2 AND status = 'running'`, kind, target)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to look up deletion of %s %s: %w", kind, target, err)
	}
	return &job, nil
}

// ScanMeta is what a scan covers, as SaveScanMetadata records it. A
// Google scan's ClientKey is its linked account's; other scans leave it
// and Name empty.
type ScanMeta struct {
	Name, ClientKey, SearchPath, Filter string
}

// StartScan records a new running scan owned by userID, and what it
// covers, in one transaction. A scan of a linked account takes a share
// lock on the account's row first, so it can't start while a deletion's
// transaction holds the account (which locks it for update), and it's
// refused with ErrAccountBeingDeleted while a deletion of the account is
// queued or running, or ErrNotFound once the account is gone. See
// docs/archive/data-deletion.md, "Rules".
func StartScan(scanType string, userID int64, meta ScanMeta) (int, error) {
	tx, err := db.Beginx()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	if meta.ClientKey != "" {
		var linked bool
		err := tx.Get(&linked, `SELECT true FROM privatetokens WHERE client_key = $1 AND user_id = $2 FOR SHARE`,
			meta.ClientKey, userID)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		if err != nil {
			return 0, fmt.Errorf("failed to lock account %s: %w", meta.ClientKey, err)
		}
		var deleting bool
		if err := tx.Get(&deleting, `SELECT EXISTS (SELECT 1 FROM deletions
			WHERE target = $1 AND kind <> 'agent_drive' AND status = 'running')`, meta.ClientKey); err != nil {
			return 0, fmt.Errorf("failed to check for deletions of %s: %w", meta.ClientKey, err)
		}
		if deleting {
			return 0, ErrAccountBeingDeleted
		}
	}
	var scanId int
	if err := tx.Get(&scanId, `INSERT INTO scans (scan_type, created_on, scan_start_time, status, user_id)
		VALUES ($1, current_timestamp, current_timestamp, 'Running', $2) RETURNING id`, scanType, userID); err != nil {
		return 0, fmt.Errorf("failed to insert scan for type %s: %w", scanType, err)
	}
	if _, err := tx.Exec(`INSERT INTO scanmetadata (name, client_key, search_path, search_filter, scan_id)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5)`,
		meta.Name, meta.ClientKey, meta.SearchPath, meta.Filter, scanId); err != nil {
		return 0, fmt.Errorf("failed to save scan metadata for scan %d: %w", scanId, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit scan %d: %w", scanId, err)
	}
	return scanId, nil
}

// lockAccount locks userID's account clientKey for a deletion, waiting for
// any scan being started on it (StartScan), then refuses with
// ErrScanRunning if one of its scans runs. Inside tx, so no scan can
// start until tx ends.
func lockAccount(tx *sql.Tx, userID int64, clientKey string) error {
	var linked bool
	err := tx.QueryRow(`SELECT true FROM privatetokens WHERE client_key = $1 AND user_id = $2 FOR UPDATE`,
		clientKey, userID).Scan(&linked)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to lock account %s: %w", clientKey, err)
	}
	var running bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM scans s JOIN scanmetadata sm ON sm.scan_id = s.id
		WHERE s.user_id = $1 AND sm.client_key = $2 AND s.status = 'Running')`, userID, clientKey).Scan(&running); err != nil {
		return fmt.Errorf("failed to check for running scans of %s: %w", clientKey, err)
	}
	if running {
		return ErrScanRunning
	}
	return nil
}

// RunningScanOf is the ID of a running scan of userID's account clientKey,
// or 0.
func RunningScanOf(userID int64, clientKey string) (int, error) {
	var id int
	err := db.Get(&id, `SELECT s.id FROM scans s JOIN scanmetadata sm ON sm.scan_id = s.id
		WHERE s.user_id = $1 AND sm.client_key = $2 AND s.status = 'Running'
		ORDER BY s.id LIMIT 1`, userID, clientKey)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to check for running scans of %s: %w", clientKey, err)
	}
	return id, nil
}

// AccountLabels names each of userID's linked accounts as the UI does
// (accountLabels.ts): its display name, plus the start of its client key
// when two share a name. Disconnecting must be confirmed by typing it.
func AccountLabels(accounts []Account) map[string]string {
	counts := map[string]int{}
	for _, a := range accounts {
		counts[a.DisplayName]++
	}
	labels := map[string]string{}
	for _, a := range accounts {
		label := a.DisplayName
		if counts[a.DisplayName] > 1 {
			key := a.ClientKey
			if len(key) > 4 {
				key = key[:4]
			}
			label += " · " + key
		}
		labels[a.ClientKey] = label
	}
	return labels
}

// DeleteAgentDriveData deletes an agent drive as uploaded from one box:
// its agent_drives row, whose files, listings, scan runs, tombstones and
// acked ranges go with it (ON DELETE CASCADE), and Browse's totals for
// it. This is the only write be makes to agentserver's tables. The DELETE
// waits for any upload batch holding the row's lock.
func DeleteAgentDriveData(drivePk int64) (map[string]int64, error) {
	tx, err := db.Beginx()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	// The drive's row first: locking it waits for an upload batch or a
	// totals rebuild holding it (rebuildTotals), so the count is of what
	// goes, and the totals deleted below are the last written.
	var locked bool
	err = tx.Get(&locked, `SELECT true FROM agent_drives WHERE id = $1 FOR UPDATE`, drivePk)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to lock the drive: %w", err)
	}
	var files int64
	if err := tx.Get(&files, `SELECT count(*) FROM agent_files WHERE drive_pk = $1`, drivePk); err != nil {
		return nil, fmt.Errorf("failed to count the drive's files: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM agent_drives WHERE id = $1`, drivePk); err != nil {
		return nil, fmt.Errorf("failed to delete the drive: %w", err)
	}
	source := agentSource(drivePk)
	for _, stmt := range []string{
		`DELETE FROM browse_folder_totals WHERE source = $1`,
		`DELETE FROM browse_totals_state WHERE source = $1`,
	} {
		if _, err := tx.Exec(stmt, source); err != nil {
			return nil, fmt.Errorf("failed to delete the drive's folder totals: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit: %w", err)
	}
	return map[string]int64{"files": files}, nil
}

// deleteScans deletes the scans of ids (a query for their IDs, with its
// arguments) and their rows, as DeleteScan does for one, in tx; the
// living records stay. It returns how many scans, messages and picked
// photos went.
func deleteScans(tx *sql.Tx, ids string, args ...any) (map[string]int64, error) {
	if _, err := tx.Exec(`CREATE TEMP TABLE doomed_scans ON COMMIT DROP AS `+ids, args...); err != nil {
		return nil, fmt.Errorf("failed to find the scans: %w", err)
	}
	steps := []struct{ table, count, stmt string }{
		{"scandata", "", `DELETE FROM scandata WHERE scan_id IN (SELECT id FROM doomed_scans)`},
		{"messagemetadata", "messages", `DELETE FROM messagemetadata WHERE scan_id IN (SELECT id FROM doomed_scans)`},
		{"photos_picked_items", "photos_items", `DELETE FROM photos_picked_items WHERE scan_id IN (SELECT id FROM doomed_scans)`},
		{"gcs_scan_buckets", "", `DELETE FROM gcs_scan_buckets WHERE scan_id IN (SELECT id FROM doomed_scans)`},
		{"scanmetadata", "", `DELETE FROM scanmetadata WHERE scan_id IN (SELECT id FROM doomed_scans)`},
		// gcs_buckets.last_scan_id and photos_picker_sessions.scan_id are
		// set to NULL by their foreign keys.
		{"scans", "scans", `DELETE FROM scans WHERE id IN (SELECT id FROM doomed_scans)`},
	}
	counts := map[string]int64{}
	for _, s := range steps {
		result, err := tx.Exec(s.stmt)
		if err != nil {
			return nil, fmt.Errorf("failed to delete from %s: %w", s.table, err)
		}
		if s.count != "" {
			counts[s.count], _ = result.RowsAffected()
		}
	}
	return counts, nil
}

// deleteRecords runs statements on clientKey in tx, adding the rows of
// the counted ones to counts.
func deleteRecords(tx *sql.Tx, clientKey string, counts map[string]int64, records []struct{ count, stmt string }) error {
	for _, r := range records {
		result, err := tx.Exec(r.stmt, clientKey)
		if err != nil {
			return fmt.Errorf("failed to delete the account's records: %w", err)
		}
		if r.count != "" {
			counts[r.count], _ = result.RowsAffected()
		}
	}
	return nil
}

// accountScans selects the IDs of userID's ($1) scans of the account ($2).
const accountScans = `SELECT s.id FROM scans s JOIN scanmetadata sm ON sm.scan_id = s.id
	WHERE s.user_id = $1 AND sm.client_key = $2`

// DeleteServiceData deletes one service of userID's account clientKey:
// the service's scans and their rows, and its living records. The account
// stays linked. ErrScanRunning while one of the account's scans runs,
// checked with the account locked (see lockAccount).
func DeleteServiceData(userID int64, clientKey string, service string) (map[string]int64, error) {
	svc, ok := DeletableServices[service]
	if !ok {
		return nil, fmt.Errorf("no such service %q", service)
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := lockAccount(tx, userID, clientKey); err != nil {
		return nil, err
	}
	counts, err := deleteScans(tx, accountScans+` AND s.scan_type = $3`, userID, clientKey, svc.scanType)
	if err != nil {
		return nil, err
	}
	if err := deleteRecords(tx, clientKey, counts, svc.records); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit: %w", err)
	}
	return counts, nil
}

// DeleteAccountData deletes userID's linked account clientKey and
// everything recorded for it: every scan of it, its Drive, Cloud Storage
// and Photos records, Browse's totals for it, and its tokens, in one
// transaction. ErrScanRunning while one of its scans runs. Revoking at
// Google is the caller's.
func DeleteAccountData(userID int64, clientKey string) (map[string]int64, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := lockAccount(tx, userID, clientKey); err != nil {
		return nil, err
	}
	counts, err := deleteScans(tx, accountScans, userID, clientKey)
	if err != nil {
		return nil, err
	}
	// Every service's living records.
	for _, svc := range DeletableServices {
		if err := deleteRecords(tx, clientKey, counts, svc.records); err != nil {
			return nil, err
		}
	}
	result, err := tx.Exec(`DELETE FROM privatetokens WHERE client_key = $1 AND user_id = $2`, clientKey, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to delete the linked account: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit: %w", err)
	}
	slog.Info("Deleted a linked account", "user_id", userID, "client_key", clientKey, "counts", counts)
	return counts, nil
}

// RefreshTokenOf is the refresh token of userID's account, for revoking.
func RefreshTokenOf(userID int64, clientKey string) (string, error) {
	token, err := GetOAuthToken(userID, clientKey)
	if err != nil {
		return "", err
	}
	return token.RefreshToken, nil
}

// ManageAccount is a linked account on the Manage data page.
type ManageAccount struct {
	ClientKey string   `json:"client_key"`
	Label     string   `json:"label"`
	Services  []string `json:"services"`
	// What's recorded per service: "gmail", "drive", "gcs", "photos".
	Recorded map[string]ServiceTotals `json:"recorded"`
	// Scans per service, and in all.
	ServiceScans map[string]int64 `json:"service_scans"`
	Scans        int64            `json:"scans"`
	// A running scan of the account, which blocks its deletions.
	RunningScan int          `json:"running_scan,omitempty"`
	Job         *DeletionJob `json:"job"`
}

// ManageDrive is an agent drive, as uploaded from one box.
type ManageDrive struct {
	ID            int64      `db:"id" json:"id"`
	DriveID       string     `db:"drive_id" json:"drive_id"`
	Hostname      string     `db:"hostname" json:"hostname"`
	Files         int64      `json:"files"`
	Bytes         int64      `json:"bytes"`
	LastSyncedAt  *time.Time `db:"last_synced_at" json:"last_synced_at"`
	PhysicalDrive *int64     `db:"physical_drive_id" json:"physical_drive,omitempty"`
	// The other boxes this physical drive was uploaded from.
	OtherCopies pq.StringArray `db:"other_copies" json:"other_copies"`
	Job         *DeletionJob   `json:"job"`
}

// ManageData is what the Manage data page lists.
type ManageData struct {
	Accounts []ManageAccount `json:"accounts"`
	Drives   []ManageDrive   `json:"drives"`
}

// GetManageData lists userID's linked accounts and uploaded drives, with
// what deleting each would remove.
func GetManageData(userID int64) (ManageData, error) {
	data := ManageData{Accounts: []ManageAccount{}, Drives: []ManageDrive{}}
	accounts, err := GetRequestAccountsFromDb(userID)
	if err != nil {
		return ManageData{}, err
	}
	labels := AccountLabels(accounts)
	for _, a := range accounts {
		account := ManageAccount{ClientKey: a.ClientKey, Label: labels[a.ClientKey], Services: a.Services,
			Recorded: map[string]ServiceTotals{}}
		for name, totals := range map[string]func() (ServiceTotals, error){
			"gmail":  func() (ServiceTotals, error) { return gmailServiceTotals(userID, a.ClientKey) },
			"drive":  func() (ServiceTotals, error) { return driveServiceTotals(a.ClientKey) },
			"gcs":    func() (ServiceTotals, error) { return gcsServiceTotals(a.ClientKey) },
			"photos": func() (ServiceTotals, error) { return photosServiceTotals(userID, a.ClientKey) },
		} {
			t, err := totals()
			if err != nil {
				return ManageData{}, err
			}
			account.Recorded[name] = t
		}
		var byType []struct {
			Type  string `db:"scan_type"`
			Count int64  `db:"n"`
		}
		if err := db.Select(&byType, `SELECT s.scan_type, count(*) AS n
			FROM scans s JOIN scanmetadata sm ON sm.scan_id = s.id
			WHERE s.user_id = $1 AND sm.client_key = $2 GROUP BY 1`, userID, a.ClientKey); err != nil {
			return ManageData{}, fmt.Errorf("failed to count the scans of %s: %w", a.ClientKey, err)
		}
		account.ServiceScans = map[string]int64{}
		for _, t := range byType {
			account.Scans += t.Count
			for name, svc := range DeletableServices {
				if svc.scanType == t.Type {
					account.ServiceScans[name] = t.Count
				}
			}
		}
		if account.RunningScan, err = RunningScanOf(userID, a.ClientKey); err != nil {
			return ManageData{}, err
		}
		// A job for the account, or for one of its services.
		for _, kind := range []string{DeleteAccount, ServiceGmail, ServiceDrive, "gcs", "photos"} {
			if account.Job != nil {
				break
			}
			if account.Job, err = runningJob(kind, a.ClientKey); err != nil {
				return ManageData{}, err
			}
		}
		data.Accounts = append(data.Accounts, account)
	}

	if err := db.Select(&data.Drives, `SELECT d.id, d.drive_id, COALESCE(a.hostname, '') AS hostname,
			d.last_synced_at, d.physical_drive_id,
			ARRAY(SELECT COALESCE(oa.hostname, '') FROM agent_drives o JOIN agent_agents oa ON oa.id = o.agent_id
				WHERE o.physical_drive_id = d.physical_drive_id AND o.id <> d.id AND oa.user_id = a.user_id
				ORDER BY 1) AS other_copies
		FROM agent_drives d JOIN agent_agents a ON a.id = d.agent_id
		WHERE a.user_id = $1 ORDER BY a.hostname, d.drive_id, d.id`, userID); err != nil {
		return ManageData{}, fmt.Errorf("failed to list agent drives: %w", err)
	}
	for i := range data.Drives {
		d := &data.Drives[i]
		totals, _, err := agentDriveTotals(d.ID)
		if err != nil {
			return ManageData{}, err
		}
		d.Files, d.Bytes = totals.Files, totals.Bytes
		if d.OtherCopies == nil {
			d.OtherCopies = pq.StringArray{}
		}
		if d.Job, err = runningJob(DeleteAgentDrive, fmt.Sprint(d.ID)); err != nil {
			return ManageData{}, err
		}
	}
	return data, nil
}

// AgentDriveLabel names an agent drive as Manage data does:
// "seagate1 (JyothriingasMBP.attlocal.net)".
func AgentDriveLabel(drivePk int64) (string, error) {
	var label string
	err := db.Get(&label, `SELECT d.drive_id || ' (' || COALESCE(a.hostname, '') || ')'
		FROM agent_drives d JOIN agent_agents a ON a.id = d.agent_id WHERE d.id = $1`, drivePk)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return label, err
}
