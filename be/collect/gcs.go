package collect

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	"github.com/jyothri/hdd/constants"
	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/notification"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/cloudresourcemanager/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// Google Cloud Storage: a linked account's projects, buckets and objects.
// See docs/archive/gcs-scans.md.

// Built on first use, after main has parsed the OAuth flags. Only the
// token's own grant matters; Scopes is for the record.
var gcsConfig = sync.OnceValue(func() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     constants.OauthClientId,
		ClientSecret: constants.OauthClientSecret,
		Endpoint:     google.Endpoint,
		Scopes: []string{storage.ScopeReadOnly,
			"https://www.googleapis.com/auth/cloudplatformprojects.readonly"},
	}
})

// gcsOptions are the client options for an account: its token, and in
// tests a fake endpoint instead. api is "storage" or "resourcemanager".
var gcsOptions = func(refreshToken string, api string) []option.ClientOption {
	ts := gcsConfig().TokenSource(context.Background(), &oauth2.Token{RefreshToken: refreshToken})
	return []option.ClientOption{option.WithTokenSource(ts)}
}

// gcsTimeout is how long listing an account's projects or a project's
// buckets may take.
const gcsTimeout = 30 * time.Second

// GcsProject is one of an account's Cloud projects.
type GcsProject struct {
	ProjectId   string `json:"projectId"`
	DisplayName string `json:"displayName"`
}

// GcsBucket is a bucket and the settings that decide what it costs.
type GcsBucket struct {
	Name         string `json:"name"`
	Location     string `json:"location"`
	StorageClass string `json:"storageClass"` // the default for new objects
	Versioning   bool   `json:"versioning"`
	// Nil when soft delete is off.
	SoftDeleteDays *int `json:"softDeleteDays,omitempty"`
	// Listing it would bill our project, so scans skip it.
	RequesterPays bool `json:"requesterPays"`
}

// GcsError is Google refusing a Cloud Storage or Resource Manager call,
// with a message the Request page can show as is.
type GcsError struct {
	Status  int
	Message string
}

func (e *GcsError) Error() string { return e.Message }

// gcsError turns a Google API error into a GcsError, and leaves others be.
func gcsError(err error, what string) error {
	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("failed to list %s: %w", what, err)
	}
	for _, d := range apiErr.Details {
		if info, ok := d.(map[string]any); ok && info["reason"] == "SERVICE_DISABLED" {
			return &GcsError{Status: http.StatusBadGateway, Message: fmt.Sprintf(
				"Google refused to list %s: an API isn't enabled for this app's Google Cloud project.", what)}
		}
	}
	switch apiErr.Code {
	case http.StatusForbidden:
		return &GcsError{Status: http.StatusForbidden, Message: fmt.Sprintf(
			"This Google account isn't allowed to list %s.", what)}
	case http.StatusNotFound:
		return &GcsError{Status: http.StatusNotFound, Message: fmt.Sprintf("Google found no %s.", what)}
	}
	return &GcsError{Status: http.StatusBadGateway, Message: fmt.Sprintf(
		"Google couldn't list %s: %s", what, apiErr.Message)}
}

// GcsProjects lists the active projects userID's linked account clientKey
// can see, by display name (ignoring case).
func GcsProjects(userID int64, clientKey string) ([]GcsProject, error) {
	account, err := resolveAccount(userID, clientKey, "")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), gcsTimeout)
	defer cancel()
	svc, err := cloudresourcemanager.NewService(ctx, gcsOptions(account.RefreshToken, "resourcemanager")...)
	if err != nil {
		return nil, fmt.Errorf("failed to create Resource Manager client: %w", err)
	}
	projects := []GcsProject{}
	err = svc.Projects.Search().Query("state:ACTIVE").Pages(ctx, func(page *cloudresourcemanager.SearchProjectsResponse) error {
		for _, p := range page.Projects {
			projects = append(projects, GcsProject{ProjectId: p.ProjectId, DisplayName: p.DisplayName})
		}
		return nil
	})
	if err != nil {
		return nil, gcsError(err, "the account's projects")
	}
	sort.Slice(projects, func(i, j int) bool {
		a, b := strings.ToLower(projects[i].DisplayName), strings.ToLower(projects[j].DisplayName)
		if a != b {
			return a < b
		}
		return projects[i].ProjectId < projects[j].ProjectId
	})
	return projects, nil
}

// gcsClient is a Cloud Storage client for an account.
func gcsClient(ctx context.Context, refreshToken string) (*storage.Client, error) {
	client, err := storage.NewClient(ctx, gcsOptions(refreshToken, "storage")...)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cloud Storage client: %w", err)
	}
	return client, nil
}

// bucketOf is what a scan and the Request page need of a bucket.
func bucketOf(attrs *storage.BucketAttrs) GcsBucket {
	b := GcsBucket{Name: attrs.Name, Location: attrs.Location, StorageClass: attrs.StorageClass,
		Versioning: attrs.VersioningEnabled, RequesterPays: attrs.RequesterPays}
	if p := attrs.SoftDeletePolicy; p != nil && p.RetentionDuration > 0 {
		days := int(p.RetentionDuration / (24 * time.Hour))
		b.SoftDeleteDays = &days
	}
	return b
}

// GcsBuckets lists the buckets of project that userID's linked account
// clientKey can see, by name.
func GcsBuckets(userID int64, clientKey string, project string) ([]GcsBucket, error) {
	account, err := resolveAccount(userID, clientKey, "")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), gcsTimeout)
	defer cancel()
	client, err := gcsClient(ctx, account.RefreshToken)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return listBuckets(ctx, client, project)
}

func listBuckets(ctx context.Context, client *storage.Client, project string) ([]GcsBucket, error) {
	buckets := []GcsBucket{}
	it := client.Buckets(ctx, project)
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, gcsError(err, fmt.Sprintf("the buckets of %s", project))
		}
		buckets = append(buckets, bucketOf(attrs))
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].Name < buckets[j].Name })
	return buckets, nil
}

var (
	// A project ID, optionally in a domain ("example.com:my-project").
	projectIdPattern = regexp.MustCompile(`^([a-z0-9][a-z0-9.-]{0,60}:)?[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	// A bucket name: 3 to 222 characters (63 without dots).
	bucketNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,220}[a-z0-9]$`)
)

// ValidProjectId reports whether id is a well-formed Cloud project ID.
func ValidProjectId(id string) bool { return projectIdPattern.MatchString(id) }

// ValidBucketName reports whether name is a well-formed bucket name.
func ValidBucketName(name string) bool {
	return bucketNamePattern.MatchString(name) && (len(name) <= 63 || strings.Contains(name, "."))
}

// GStorageScan asks to scan a project's buckets, or one bucket, optionally
// under a prefix.
type GStorageScan struct {
	ClientKey string
	ProjectId string
	Bucket    string // "" for every bucket of the project
	Prefix    string // only with Bucket
	// Also list noncurrent versions, and soft-deleted objects.
	Versions    bool
	SoftDeleted bool
}

// The account's record; tests replace these.
var (
	upsertGcsObjects       = db.UpsertGcsObjects
	deleteUnseenGcsObjects = db.DeleteUnseenGcsObjects
	rebuildGcsPrefixTotals = db.RebuildGcsPrefixTotals
	saveGcsBucket          = db.SaveGcsBucket
	saveGcsScanBucket      = db.SaveGcsScanBucket
)

// gcsBatch is how many object versions go to the record at a time.
const gcsBatch = 1000

// gcsAttrs are the object fields a scan asks for.
var gcsAttrs = []string{"Name", "Generation", "Size", "StorageClass", "MD5", "CRC32C", "Updated", "Deleted", "HardDeleteTime"}

// CloudStorage checks what a scan covers, records it, and scans it in the
// background into the account's record. A project or bucket the account
// can't list is a GcsError, and no scan is recorded.
func CloudStorage(scan GStorageScan, userID int64) (int, error) {
	account, err := resolveAccount(userID, scan.ClientKey, "")
	if err != nil {
		return 0, err
	}
	client, err := gcsClient(context.Background(), account.RefreshToken)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), gcsTimeout)
	buckets, err := scanTargets(ctx, client, scan)
	cancel()
	if err != nil {
		client.Close()
		return 0, err
	}

	scanId, err := startScan("gcs", userID, db.ScanMeta{Name: account.Name, ClientKey: account.ClientKey,
		SearchPath: gcsSearchPath(scan), Filter: gcsIncludes(scan)})
	if err != nil {
		client.Close()
		var requestErr *RequestError
		if errors.As(err, &requestErr) {
			return 0, err
		}
		return 0, fmt.Errorf("failed to start Cloud Storage scan of %s: %w", scan.ProjectId, err)
	}
	go func() {
		defer client.Close()
		if err := scanGcs(client, scanId, account.ClientKey, scan, buckets); err != nil {
			slog.Error("Cloud Storage scan failed", "scan_id", scanId, "error", err)
			db.MarkScanFailed(scanId, err.Error())
			return
		}
		if err := db.MarkScanCompleted(scanId); err != nil {
			slog.Error("Failed to mark scan complete", "scan_id", scanId, "error", err)
		}
	}()
	return scanId, nil
}

// gcsSearchPath is what a scan covers, as Request History shows it.
func gcsSearchPath(scan GStorageScan) string {
	if scan.Bucket == "" {
		return scan.ProjectId + " (all buckets)"
	}
	return scan.ProjectId + "/" + scan.Bucket + "/" + scan.Prefix
}

// gcsIncludes says which object versions a scan lists.
func gcsIncludes(scan GStorageScan) string {
	parts := []string{"live"}
	if scan.Versions {
		parts = append(parts, "noncurrent versions")
	}
	if scan.SoftDeleted {
		parts = append(parts, "soft-deleted")
	}
	return strings.Join(parts, ", ")
}

// scanTargets returns the buckets a scan covers.
func scanTargets(ctx context.Context, client *storage.Client, scan GStorageScan) ([]GcsBucket, error) {
	if scan.Bucket == "" {
		return listBuckets(ctx, client, scan.ProjectId)
	}
	attrs, err := client.Bucket(scan.Bucket).Attrs(ctx)
	if errors.Is(err, storage.ErrBucketNotExist) {
		return nil, &GcsError{Status: http.StatusNotFound, Message: fmt.Sprintf("There's no bucket %s.", scan.Bucket)}
	}
	if err != nil {
		return nil, gcsError(err, "bucket "+scan.Bucket)
	}
	return []GcsBucket{bucketOf(attrs)}, nil
}

// scanGcs scans each bucket in turn, publishing progress under clientKey.
// A bucket that fails is recorded as failed, and the scan goes on; the
// scan fails only if every bucket it tried did, or the record can't be
// written.
func scanGcs(client *storage.Client, scanId int, clientKey string, scan GStorageScan, buckets []GcsBucket) error {
	lock.Lock()
	defer lock.Unlock()
	resetCounters()
	ticker := time.NewTicker(5 * time.Second)
	done := make(chan bool)
	go logProgress(scanId, clientKey, time.Now(), done, ticker, notification.GetPublisher(clientKey))
	defer func() {
		done <- true
		ticker.Stop()
	}()

	tried, failed := 0, 0
	var lastErr error
	for _, b := range buckets {
		row := db.GcsScanBucket{Bucket: b.Name, ProjectId: scan.ProjectId, Prefix: scan.Prefix}
		if b.RequesterPays {
			row.Status, row.Error = db.GcsBucketSkipped, "Requester Pays: listing it would bill this app's project"
			if err := saveGcsScanBucket(scanId, row); err != nil {
				return err
			}
			continue
		}
		tried++
		totals, states, softErr, err := scanBucket(client, scanId, clientKey, b.Name, scan)
		row.GcsTotals = totals
		if err != nil {
			failed++
			lastErr = err
			row.Status, row.Error = db.GcsBucketFailed, err.Error()
			slog.Warn("Cloud Storage bucket failed", "scan_id", scanId, "bucket", b.Name, "error", err)
			if err := saveGcsScanBucket(scanId, row); err != nil {
				return err
			}
			continue
		}
		row.Status = db.GcsBucketCompleted
		if softErr != nil {
			row.Error = "Soft-deleted objects couldn't be listed: " + softErr.Error()
		}
		if _, err := deleteUnseenGcsObjects(clientKey, b.Name, scanId, scan.Prefix, states); err != nil {
			return err
		}
		if err := rebuildGcsPrefixTotals(clientKey, b.Name); err != nil {
			return err
		}
		record := db.GcsBucketRecord{Bucket: b.Name, ProjectId: scan.ProjectId, Location: b.Location,
			StorageClass: b.StorageClass, Versioning: b.Versioning, SoftDeleteDays: b.SoftDeleteDays}
		if err := saveGcsBucket(clientKey, record, scanId); err != nil {
			return err
		}
		if err := saveGcsScanBucket(scanId, row); err != nil {
			return err
		}
	}
	if tried > 0 && failed == tried {
		return fmt.Errorf("no bucket could be scanned: %w", lastErr)
	}
	return nil
}

// scanBucket lists a bucket's live (and, when asked, noncurrent) objects
// under the scan's prefix, then its soft-deleted ones when asked, into the
// record. It returns the totals, the states it listed in full, and the
// soft-deleted pass's error, which doesn't fail the bucket.
func scanBucket(client *storage.Client, scanId int, clientKey string, bucket string, scan GStorageScan) (db.GcsTotals, []string, error, error) {
	var totals db.GcsTotals
	ctx := context.Background()
	states := []string{db.GcsLive}
	if scan.Versions {
		states = append(states, db.GcsNoncurrent)
	}
	query := &storage.Query{Prefix: scan.Prefix, Versions: scan.Versions}
	if err := listObjects(ctx, client, scanId, clientKey, bucket, query, false, &totals); err != nil {
		return totals, nil, nil, err
	}
	var softErr error
	if scan.SoftDeleted {
		query := &storage.Query{Prefix: scan.Prefix, SoftDeleted: true}
		if softErr = listObjects(ctx, client, scanId, clientKey, bucket, query, true, &totals); softErr == nil {
			states = append(states, db.GcsSoftDeleted)
		}
	}
	return totals, states, softErr, nil
}

// listObjects lists one pass of a bucket into the record, a batch at a
// time. The client library retries 429s and 5xx on its own.
func listObjects(ctx context.Context, client *storage.Client, scanId int, clientKey string, bucket string,
	query *storage.Query, softDeleted bool, totals *db.GcsTotals) error {
	if err := query.SetAttrSelection(gcsAttrs); err != nil {
		return err
	}
	batch := make([]db.GcsObject, 0, gcsBatch)
	flush := func() error {
		if err := upsertGcsObjects(clientKey, bucket, scanId, batch); err != nil {
			return err
		}
		counter_processed.Add(int64(len(batch)))
		batch = batch[:0]
		return nil
	}
	it := client.Bucket(bucket).Objects(ctx, query)
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return gcsError(err, "the objects of "+bucket)
		}
		o := objectOf(attrs, softDeleted)
		totals.Add(o.State, o.StorageClass, o.Size)
		if batch = append(batch, o); len(batch) == gcsBatch {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

// objectOf is what the record keeps of an object version.
func objectOf(attrs *storage.ObjectAttrs, softDeleted bool) db.GcsObject {
	o := db.GcsObject{Name: attrs.Name, Generation: attrs.Generation, Size: attrs.Size,
		StorageClass: attrs.StorageClass, State: db.GcsLive}
	switch {
	case softDeleted:
		o.State = db.GcsSoftDeleted
	case !attrs.Deleted.IsZero():
		o.State = db.GcsNoncurrent
	}
	if len(attrs.MD5) > 0 {
		o.Md5Hash = hex.EncodeToString(attrs.MD5)
	}
	if attrs.CRC32C != 0 {
		o.Crc32c = fmt.Sprintf("%08x", attrs.CRC32C)
	}
	if !attrs.Updated.IsZero() {
		o.Updated = &attrs.Updated
	}
	if !attrs.Deleted.IsZero() {
		o.TimeDeleted = &attrs.Deleted
	}
	if !attrs.HardDeleteTime.IsZero() {
		o.HardDeleteTime = &attrs.HardDeleteTime
	}
	return o
}
