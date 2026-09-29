package collect

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	"github.com/jyothri/hdd/constants"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/cloudresourcemanager/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// Google Cloud Storage: a linked account's projects, buckets and objects.
// See docs/specs/gcs-scans.md.

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
