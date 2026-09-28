package db

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// What Browse shows: a user's Google accounts and agent drives, one
// folder at a time. See docs/archive/browse.md.

// BrowsePageSize is how many entries a page of a folder holds, and
// MessagesPageSize how many messages a page of an account's Gmail does.
const (
	BrowsePageSize   = 200
	MessagesPageSize = 50
)

// ErrNotFound is a source, or a folder in it, that the user can't browse.
var ErrNotFound = errors.New("not found")

// The two roots of a Drive account, as the folder IDs Browse uses for
// them: My Drive's own ID stands for it too.
const SharedWithMe = "shared-with-me"

// ServiceTotals is what's recorded of one service of a Google account.
type ServiceTotals struct {
	Granted bool `json:"granted"`
	// Nil until something is recorded.
	Files     *int64     `json:"files,omitempty"`
	Bytes     *int64     `json:"bytes,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	// The folder totals are being rebuilt (Drive).
	Updating bool `json:"updating,omitempty"`
}

// BrowseSource is a Google account or an agent drive.
type BrowseSource struct {
	Kind string `json:"kind"` // "google" or "agent"
	Key  string `json:"key"`
	Name string `json:"name"`
	// A Google account's services, by name.
	Services map[string]ServiceTotals `json:"services,omitempty"`
	// An agent drive's totals, its last sync, and its physical drive when
	// copies are linked.
	Files         *int64     `json:"files,omitempty"`
	Bytes         *int64     `json:"bytes,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
	PhysicalDrive *int64     `json:"physical_drive,omitempty"`
	Updating      bool       `json:"updating,omitempty"`
}

// BrowseSources lists what userID can browse: their linked accounts, then
// their agents' drives.
func BrowseSources(userID int64) ([]BrowseSource, error) {
	accounts, err := GetRequestAccountsFromDb(userID)
	if err != nil {
		return nil, err
	}
	sources := []BrowseSource{}
	for _, a := range accounts {
		drive, err := driveServiceTotals(a.ClientKey)
		if err != nil {
			return nil, err
		}
		gmail, err := gmailServiceTotals(userID, a.ClientKey)
		if err != nil {
			return nil, err
		}
		drive.Granted = contains(a.Services, ServiceDrive)
		gmail.Granted = contains(a.Services, ServiceGmail)
		sources = append(sources, BrowseSource{Kind: "google", Key: a.ClientKey, Name: a.DisplayName,
			Services: map[string]ServiceTotals{ServiceDrive: drive, ServiceGmail: gmail}})
	}

	drives := []struct {
		Id            int64      `db:"id"`
		DriveId       string     `db:"drive_id"`
		Hostname      string     `db:"hostname"`
		PhysicalDrive *int64     `db:"physical_drive_id"`
		LastSyncedAt  *time.Time `db:"last_synced_at"`
	}{}
	if err := db.Select(&drives, `SELECT d.id, d.drive_id, COALESCE(a.hostname, '') AS hostname,
			d.physical_drive_id, d.last_synced_at
		FROM agent_drives d JOIN agent_agents a ON a.id = d.agent_id
		WHERE a.user_id = $1 ORDER BY d.drive_id, a.hostname, d.id`, userID); err != nil {
		return nil, fmt.Errorf("failed to list agent drives: %w", err)
	}
	for _, d := range drives {
		totals, updating, err := agentDriveTotals(d.Id)
		if err != nil {
			return nil, err
		}
		name := d.DriveId
		if d.Hostname != "" {
			name += " (" + d.Hostname + ")"
		}
		sources = append(sources, BrowseSource{Kind: "agent", Key: fmt.Sprint(d.Id), Name: name,
			Files: &totals.Files, Bytes: &totals.Bytes, UpdatedAt: d.LastSyncedAt,
			PhysicalDrive: d.PhysicalDrive, Updating: updating})
	}
	return sources, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// driveServiceTotals is what an account's Drive record holds.
func driveServiceTotals(clientKey string) (ServiceTotals, error) {
	var row struct {
		Files     int64      `db:"files"`
		Bytes     int64      `db:"bytes"`
		UpdatedAt *time.Time `db:"updated_at"`
	}
	err := db.Get(&row, `SELECT
			(SELECT count(*) FROM drive_items WHERE client_key = $1 AND NOT is_dir AND NOT trashed) AS files,
			(SELECT COALESCE(sum(size), 0) FROM drive_items WHERE client_key = $1 AND NOT is_dir AND NOT trashed) AS bytes,
			(SELECT updated_at FROM drive_accounts WHERE client_key = $1) AS updated_at`, clientKey)
	if err != nil {
		return ServiceTotals{}, fmt.Errorf("failed to add up the Drive record of %s: %w", clientKey, err)
	}
	if row.UpdatedAt == nil {
		return ServiceTotals{}, nil
	}
	state, err := getTotalsState(driveSource(clientKey))
	if err != nil {
		return ServiceTotals{}, err
	}
	return ServiceTotals{Files: &row.Files, Bytes: &row.Bytes, UpdatedAt: row.UpdatedAt,
		Updating: state.Building || !state.Built}, nil
}

// accountMessages is the FROM and WHERE of an account's messages: those
// of all userID's ($1) Gmail scans of the account ($2).
const accountMessages = `FROM messagemetadata m JOIN scanmetadata sm ON sm.scan_id = m.scan_id
	JOIN scans s ON s.id = m.scan_id
	WHERE s.user_id = $1 AND sm.client_key = $2 AND s.scan_type = 'gmail'`

func gmailServiceTotals(userID int64, clientKey string) (ServiceTotals, error) {
	var row struct {
		Files     int64      `db:"files"`
		Bytes     int64      `db:"bytes"`
		UpdatedAt *time.Time `db:"updated_at"`
	}
	err := db.Get(&row, `SELECT count(*) AS files, COALESCE(sum(m.size_estimate), 0) AS bytes,
			max(COALESCE(s.scan_end_time, s.scan_start_time)) AS updated_at `+accountMessages, userID, clientKey)
	if err != nil {
		return ServiceTotals{}, fmt.Errorf("failed to add up the messages of %s: %w", clientKey, err)
	}
	if row.Files == 0 {
		return ServiceTotals{}, nil
	}
	return ServiceTotals{Files: &row.Files, Bytes: &row.Bytes, UpdatedAt: row.UpdatedAt}, nil
}

// agentDriveTotals is an agent drive's totals, cached or else added up,
// and whether its cache is out of date.
func agentDriveTotals(drivePk int64) (FolderTotals, bool, error) {
	source := agentSource(drivePk)
	state, err := getTotalsState(source)
	if err != nil {
		return FolderTotals{}, false, err
	}
	if !state.Built {
		var t FolderTotals
		err := db.Get(&t, `SELECT count(*) AS files, COALESCE(sum(size), 0) AS bytes
			FROM agent_files WHERE drive_pk = $1`, drivePk)
		return t, true, err
	}
	totals, err := cachedTotals(source, []string{""})
	if err != nil {
		return FolderTotals{}, false, err
	}
	updating := state.Building
	if !updating {
		version, err := agentVersion(drivePk)
		if err != nil {
			return FolderTotals{}, false, err
		}
		updating = version != state.Version
	}
	return totals[""], updating, nil
}

// PathPart is one folder on the way to the one browsed.
type PathPart struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

// BrowseFolder is a subfolder, with everything under it.
type BrowseFolder struct {
	Id   string `json:"id"`
	Name string `json:"name"`
	FolderTotals
}

// BrowseFile is a file in the folder browsed.
type BrowseFile struct {
	Id       string     `db:"id" json:"id"`
	Name     string     `db:"name" json:"name"`
	Size     *int64     `db:"size" json:"size"` // nil when not scanned yet (agent)
	Modified *time.Time `db:"modified" json:"modified"`
	MimeType string     `db:"mime_type" json:"mime_type,omitempty"`
	// Why an agent couldn't read it, instead of a size.
	Error string `db:"error" json:"error,omitempty"`
}

// FolderPage is a page of a folder's contents: subfolders, largest first,
// then files, largest first.
type FolderPage struct {
	// From the source's root down to the folder; empty at the root.
	Path    []PathPart     `json:"path"`
	Folders []BrowseFolder `json:"folders"`
	Files   []BrowseFile   `json:"files"`
	// Subfolders and files in all, across pages.
	Entries  int  `json:"entries"`
	Page     int  `json:"page"`
	PageSize int  `json:"page_size"`
	Updating bool `json:"updating"`
	// Everything under the folder itself, for each entry's share of it.
	Totals FolderTotals `json:"totals"`
}

// GoogleAccountOwnedBy reports whether userID linked clientKey.
func GoogleAccountOwnedBy(userID int64, clientKey string) (bool, error) {
	var owned bool
	err := db.Get(&owned, `SELECT EXISTS (SELECT 1 FROM privatetokens WHERE client_key = $1 AND user_id = $2)`,
		clientKey, userID)
	return owned, err
}

// AgentDriveOwnedBy reports whether drivePk is a drive of userID's agents.
func AgentDriveOwnedBy(userID int64, drivePk int64) (bool, error) {
	var owned bool
	err := db.Get(&owned, `SELECT EXISTS (SELECT 1 FROM agent_drives d JOIN agent_agents a ON a.id = d.agent_id
		WHERE d.id = $1 AND a.user_id = $2)`, drivePk, userID)
	return owned, err
}

// pageFolders sorts folders largest first and fills in page with its
// part of them, and the offset and limit of the files after them.
func pageFolders(page *FolderPage, folders []BrowseFolder, files int) (offset, limit int) {
	sort.SliceStable(folders, func(i, j int) bool {
		if folders[i].Bytes != folders[j].Bytes {
			return folders[i].Bytes > folders[j].Bytes
		}
		return strings.ToLower(folders[i].Name) < strings.ToLower(folders[j].Name)
	})
	start := (page.Page - 1) * BrowsePageSize
	end := start + BrowsePageSize
	page.PageSize = BrowsePageSize
	page.Entries = len(folders) + files
	page.Folders = folders[min(start, len(folders)):min(end, len(folders))]
	page.Files = []BrowseFile{}
	offset = max(0, start-len(folders))
	limit = BrowsePageSize - len(page.Folders)
	return offset, limit
}

// DriveChildren returns a page of a Drive folder of clientKey: "" for the
// roots, My Drive's ID, SharedWithMe, or a folder in the record.
func DriveChildren(clientKey string, folder string, pageNo int) (FolderPage, error) {
	page := FolderPage{Path: []PathPart{}, Page: max(pageNo, 1)}
	var myDrive string
	err := db.Get(&myDrive, `SELECT my_drive_id FROM drive_accounts WHERE client_key = $1`, clientKey)
	if errors.Is(err, sql.ErrNoRows) {
		// Nothing recorded yet.
		pageFolders(&page, []BrowseFolder{}, 0)
		return page, nil
	}
	if err != nil {
		return FolderPage{}, fmt.Errorf("failed to look up My Drive of %s: %w", clientKey, err)
	}
	source := driveSource(clientKey)
	state, err := getTotalsState(source)
	if err != nil {
		return FolderPage{}, err
	}
	page.Updating = state.Building || !state.Built
	totalsOf := func(ids []string) (map[string]FolderTotals, error) {
		if state.Built {
			return cachedTotals(source, ids)
		}
		return liveDriveTotals(clientKey)
	}

	if folder == "" {
		totals, err := totalsOf([]string{"", myDrive})
		if err != nil {
			return FolderPage{}, err
		}
		all, mine := totals[""], totals[myDrive]
		shared := FolderTotals{Files: all.Files - mine.Files, Bytes: all.Bytes - mine.Bytes}
		// The roots stay in this order.
		page.Folders = []BrowseFolder{
			{Id: myDrive, Name: "My Drive", FolderTotals: mine},
			{Id: SharedWithMe, Name: "Shared with me", FolderTotals: shared},
		}
		page.Files = []BrowseFile{}
		page.Entries, page.PageSize = 2, BrowsePageSize
		page.Totals = all
		return page, nil
	}

	// The folder's children ($1 is the account, $2 the folder), and its
	// path.
	where, arg := `i.parent_id = $2`, folder
	switch folder {
	case myDrive:
		page.Path = []PathPart{{Id: myDrive, Name: "My Drive"}}
	case SharedWithMe:
		page.Path = []PathPart{{Id: SharedWithMe, Name: "Shared with me"}}
		// Items whose parent isn't in the record, nor My Drive ($2).
		where, arg = `i.parent_id IS NULL OR (i.parent_id <> $2 AND NOT EXISTS (
			SELECT 1 FROM drive_items p WHERE p.client_key = $1 AND p.file_id = i.parent_id))`, myDrive
	default:
		path, err := drivePath(clientKey, folder, myDrive)
		if err != nil {
			return FolderPage{}, err
		}
		page.Path = path
	}
	base := `FROM drive_items i WHERE i.client_key = $1 AND NOT i.trashed AND (` + where + `)`
	folders := []BrowseFolder{}
	if err := db.Select(&folders, `SELECT i.file_id AS id, i.name, 0 AS files, 0 AS bytes `+base+` AND i.is_dir`,
		clientKey, arg); err != nil {
		return FolderPage{}, fmt.Errorf("failed to list folders in %s: %w", folder, err)
	}
	// The subfolders' totals, and the folder's own: "Shared with me" is
	// the account's ("") less My Drive's.
	ids := []string{folder}
	if folder == SharedWithMe {
		ids = []string{"", myDrive}
	}
	for _, f := range folders {
		ids = append(ids, f.Id)
	}
	totals, err := totalsOf(ids)
	if err != nil {
		return FolderPage{}, err
	}
	for i := range folders {
		folders[i].FolderTotals = totals[folders[i].Id]
	}
	page.Totals = totals[folder]
	if folder == SharedWithMe {
		all, mine := totals[""], totals[myDrive]
		page.Totals = FolderTotals{Files: all.Files - mine.Files, Bytes: all.Bytes - mine.Bytes}
	}
	var files int
	if err := db.Get(&files, `SELECT count(*) `+base+` AND NOT i.is_dir`, clientKey, arg); err != nil {
		return FolderPage{}, fmt.Errorf("failed to count files in %s: %w", folder, err)
	}
	offset, limit := pageFolders(&page, folders, files)
	if limit > 0 && offset < files {
		if err := db.Select(&page.Files, `SELECT i.file_id AS id, i.name, i.size, i.modified, i.mime_type, '' AS error `+
			base+` AND NOT i.is_dir ORDER BY i.size DESC, lower(i.name), i.file_id LIMIT $3 OFFSET $4`,
			clientKey, arg, limit, offset); err != nil {
			return FolderPage{}, fmt.Errorf("failed to list files in %s: %w", folder, err)
		}
	}
	return page, nil
}

// drivePath is the path from a root down to folder, built from the parents
// in the record. ErrNotFound when folder isn't a folder in it.
func drivePath(clientKey string, folder string, myDrive string) ([]PathPart, error) {
	rows := []struct {
		Id     string `db:"file_id"`
		Name   string `db:"name"`
		Parent string `db:"parent_id"`
		IsDir  bool   `db:"is_dir"`
	}{}
	err := db.Select(&rows, `WITH RECURSIVE up (file_id, name, parent_id, is_dir, depth) AS (
			SELECT file_id, name, COALESCE(parent_id, ''), is_dir, 0 FROM drive_items
			WHERE client_key = $1 AND file_id = $2
		UNION ALL
			SELECT p.file_id, p.name, COALESCE(p.parent_id, ''), p.is_dir, u.depth + 1 FROM up u
			JOIN drive_items p ON p.client_key = $1 AND p.file_id = u.parent_id
			WHERE u.depth < 100
		)
		SELECT file_id, name, parent_id, is_dir FROM up ORDER BY depth DESC`, clientKey, folder)
	if err != nil {
		return nil, fmt.Errorf("failed to look up the path of %s: %w", folder, err)
	}
	if len(rows) == 0 || !rows[len(rows)-1].IsDir {
		return nil, ErrNotFound
	}
	root := PathPart{Id: SharedWithMe, Name: "Shared with me"}
	if rows[0].Parent == myDrive {
		root = PathPart{Id: myDrive, Name: "My Drive"}
	}
	path := []PathPart{root}
	for _, r := range rows {
		path = append(path, PathPart{Id: r.Id, Name: r.Name})
	}
	return path, nil
}

// liveDriveTotals adds up every folder of clientKey, for before its
// totals are first cached.
func liveDriveTotals(clientKey string) (map[string]FolderTotals, error) {
	rows := []struct {
		Folder string `db:"folder"`
		FolderTotals
	}{}
	if err := db.Select(&rows, strings.ReplaceAll(driveTotalsQuery, "$2", "$1"), clientKey); err != nil {
		return nil, fmt.Errorf("failed to add up the folders of %s: %w", clientKey, err)
	}
	totals := map[string]FolderTotals{}
	for _, r := range rows {
		totals[r.Folder] = r.FolderTotals
	}
	return totals, nil
}

// pathKey is how agentserver keys a path: sha256 of its bytes. The folder
// path browsed is readable text; see "AgentChildren".
const pathKey = `sha256(convert_to($2, 'UTF8'))`

// AgentChildren returns a page of a folder of an agent drive, by its path
// relative to the drive's root ("" for the root). Subfolders come from the
// drive's directory listings, so folders with nothing scanned under them
// show too; files are the listings' files, with what agent_files has of
// them. Paths that aren't valid UTF-8 are shown in agentserver's readable
// form, which can't be browsed into.
func AgentChildren(drivePk int64, folder string, pageNo int) (FolderPage, error) {
	folder = strings.Trim(folder, "/")
	page := FolderPage{Path: []PathPart{}, Page: max(pageNo, 1)}
	if folder != "" {
		var exists bool
		// A folder is listed in its parent, as a directory.
		parent, name := "", folder
		if i := strings.LastIndex(folder, "/"); i >= 0 {
			parent, name = folder[:i], folder[i+1:]
		}
		if err := db.Get(&exists, `SELECT EXISTS (SELECT 1 FROM agent_dir_listings
			WHERE drive_pk = $1 AND parent_key = `+pathKey+` AND child_name = $3 AND is_dir)`,
			drivePk, parent, name); err != nil {
			return FolderPage{}, fmt.Errorf("failed to look up folder %q: %w", folder, err)
		}
		if !exists {
			return FolderPage{}, ErrNotFound
		}
		parts := strings.Split(folder, "/")
		for i := range parts {
			page.Path = append(page.Path, PathPart{Id: strings.Join(parts[:i+1], "/"), Name: parts[i]})
		}
	}

	folders := []BrowseFolder{}
	if err := db.Select(&folders, `SELECT child_name AS name, '' AS id, 0 AS files, 0 AS bytes
		FROM agent_dir_listings WHERE drive_pk = $1 AND parent_key = `+pathKey+` AND is_dir`,
		drivePk, folder); err != nil {
		return FolderPage{}, fmt.Errorf("failed to list folders in %q: %w", folder, err)
	}
	prefix := ""
	if folder != "" {
		prefix = folder + "/"
	}
	ids := make([]string, len(folders))
	for i := range folders {
		folders[i].Id = prefix + folders[i].Name
		ids[i] = folders[i].Id
	}
	source := agentSource(drivePk)
	state, err := getTotalsState(source)
	if err != nil {
		return FolderPage{}, err
	}
	var totals map[string]FolderTotals
	if state.Built {
		// The folder's own totals too.
		totals, err = cachedTotals(source, append(ids, folder))
		page.Updating = state.Building
		if err == nil && !page.Updating {
			var version int64
			version, err = agentVersion(drivePk)
			page.Updating = version != state.Version
		}
	} else {
		totals, err = liveAgentTotals(drivePk, prefix)
		if err == nil {
			totals[folder], err = liveAgentFolderTotals(drivePk, prefix)
		}
		page.Updating = true
	}
	if err != nil {
		return FolderPage{}, err
	}
	for i := range folders {
		folders[i].FolderTotals = totals[folders[i].Id]
	}
	page.Totals = totals[folder]

	// A listed file's path key: its parent's raw path, "/", its raw name.
	const files = `FROM agent_dir_listings l
		LEFT JOIN agent_files f ON f.drive_pk = l.drive_pk AND f.path_key = sha256(
			CASE WHEN l.relative_path = '' AND l.raw_path IS NULL THEN ''::bytea
				ELSE COALESCE(l.raw_path, convert_to(l.relative_path, 'UTF8')) || '/'::bytea END
			|| COALESCE(l.raw_child_name, convert_to(l.child_name, 'UTF8')))
		WHERE l.drive_pk = $1 AND l.parent_key = ` + pathKey + ` AND NOT l.is_dir`
	var count int
	if err := db.Get(&count, `SELECT count(*) `+files, drivePk, folder); err != nil {
		return FolderPage{}, fmt.Errorf("failed to count files in %q: %w", folder, err)
	}
	offset, limit := pageFolders(&page, folders, count)
	if limit > 0 && offset < count {
		if err := db.Select(&page.Files, `SELECT $5 || l.child_name AS id, l.child_name AS name,
				CASE WHEN f.status = 'error' THEN NULL ELSE f.size END AS size, f.mtime AS modified,
				'' AS mime_type, CASE WHEN f.status = 'error' THEN COALESCE(f.error_message, 'unreadable') ELSE '' END AS error
			`+files+` ORDER BY f.size DESC NULLS LAST, lower(l.child_name), l.child_name LIMIT $3 OFFSET $4`,
			drivePk, folder, limit, offset, prefix); err != nil {
			return FolderPage{}, fmt.Errorf("failed to list files in %q: %w", folder, err)
		}
	}
	return page, nil
}

// liveAgentTotals adds up, in one pass over the drive's files, the
// subfolders of the folder whose paths start with prefix, for before the
// drive's totals are first cached. Keyed by the subfolder's path.
func liveAgentTotals(drivePk int64, prefix string) (map[string]FolderTotals, error) {
	rows := []struct {
		Folder string `db:"folder"`
		FolderTotals
	}{}
	if err := db.Select(&rows, `SELECT $2 || split_part(substr(relative_path, length($2) + 1), '/', 1) AS folder,
			count(*) AS files, COALESCE(sum(size), 0) AS bytes
		FROM agent_files
		WHERE drive_pk = $1 AND left(relative_path, length($2)) = $2
			AND strpos(substr(relative_path, length($2) + 1), '/') > 0
		GROUP BY 1`, drivePk, prefix); err != nil {
		return nil, fmt.Errorf("failed to add up the folders under %q: %w", prefix, err)
	}
	totals := map[string]FolderTotals{}
	for _, r := range rows {
		totals[r.Folder] = r.FolderTotals
	}
	return totals, nil
}

// liveAgentFolderTotals adds up everything under the folder whose paths
// start with prefix ("" for the drive's root).
func liveAgentFolderTotals(drivePk int64, prefix string) (FolderTotals, error) {
	var t FolderTotals
	err := db.Get(&t, `SELECT count(*) AS files, COALESCE(sum(size), 0) AS bytes FROM agent_files
		WHERE drive_pk = $1 AND left(relative_path, length($2)) = $2`, drivePk, prefix)
	if err != nil {
		return FolderTotals{}, fmt.Errorf("failed to add up the folder %q: %w", prefix, err)
	}
	return t, nil
}

// AgentScanRun is an agent drive's last scan.
type AgentScanRun struct {
	StartedAt   time.Time  `db:"started_at" json:"started_at"`
	FinishedAt  *time.Time `db:"finished_at" json:"finished_at"`
	FilesSeen   *int64     `db:"files_seen" json:"files_seen"`
	Interrupted *bool      `db:"interrupted" json:"interrupted"`
}

// AgentDriveStatus is how current an agent drive's upload is.
type AgentDriveStatus struct {
	LastScan      *AgentScanRun `json:"last_scan"`
	LastSyncedAt  *time.Time    `json:"last_synced_at"`
	PhysicalDrive *int64        `json:"physical_drive"`
	// The drive's folder totals are being rebuilt.
	Updating bool `json:"updating"`
}

// GetAgentDriveStatus returns an agent drive's status. The caller checks
// its owner.
func GetAgentDriveStatus(drivePk int64) (AgentDriveStatus, error) {
	var status AgentDriveStatus
	if err := db.QueryRow(`SELECT last_synced_at, physical_drive_id FROM agent_drives WHERE id = $1`, drivePk).
		Scan(&status.LastSyncedAt, &status.PhysicalDrive); err != nil {
		return AgentDriveStatus{}, fmt.Errorf("failed to look up agent drive %d: %w", drivePk, err)
	}
	var run AgentScanRun
	err := db.Get(&run, `SELECT started_at, finished_at, files_seen, interrupted FROM agent_scan_runs
		WHERE drive_pk = $1 ORDER BY started_at DESC, run_id DESC LIMIT 1`, drivePk)
	switch {
	case err == nil:
		status.LastScan = &run
	case !errors.Is(err, sql.ErrNoRows):
		return AgentDriveStatus{}, fmt.Errorf("failed to look up the last scan of drive %d: %w", drivePk, err)
	}
	_, updating, err := agentDriveTotals(drivePk)
	if err != nil {
		return AgentDriveStatus{}, err
	}
	status.Updating = updating
	return status, nil
}

// AccountMessage is a message in an account's Gmail, with the scan that
// found it.
type AccountMessage struct {
	MessageRow
	ScanId int `db:"scan_id" json:"scan_id"`
}

// MessagePage is a page of an account's messages.
type AccountMessagePage struct {
	Messages []AccountMessage `json:"messages"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

// AccountMessages returns a page of clientKey's messages, across all
// userID's Gmail scans of it: by "size", largest first, or else newest
// first.
func AccountMessages(userID int64, clientKey string, sortBy string, pageNo int) (AccountMessagePage, error) {
	page := AccountMessagePage{Messages: []AccountMessage{}, Page: max(pageNo, 1), PageSize: MessagesPageSize}
	if err := db.Get(&page.Total, `SELECT count(*) `+accountMessages, userID, clientKey); err != nil {
		return AccountMessagePage{}, fmt.Errorf("failed to count the messages of %s: %w", clientKey, err)
	}
	order := `m.date DESC NULLS LAST, m.id`
	if sortBy == "size" {
		order = `m.size_estimate DESC NULLS LAST, m.id`
	}
	if err := db.Select(&page.Messages, `SELECT m.id, COALESCE(m.message_id, '') AS message_id,
			COALESCE(m.thread_id, '') AS thread_id, COALESCE(m.labels, '') AS labels,
			COALESCE(m.mail_from, '') AS mail_from, COALESCE(m.mail_to, '') AS mail_to,
			COALESCE(m.subject, '') AS subject, m.date, COALESCE(m.size_estimate, 0) AS size_estimate, m.scan_id
		`+accountMessages+` ORDER BY `+order+` LIMIT $3 OFFSET $4`,
		userID, clientKey, MessagesPageSize, MessagesPageSize*(page.Page-1)); err != nil {
		return AccountMessagePage{}, fmt.Errorf("failed to list the messages of %s: %w", clientKey, err)
	}
	return page, nil
}
