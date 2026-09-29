package web

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/collect"
	"github.com/jyothri/hdd/db"
)

// Google Cloud Storage: what the Request page needs to start a scan. See
// docs/archive/gcs-scans.md, "Backend".

func gcsRoutes(api *mux.Router) {
	api.HandleFunc("/gcs/{client_key}/projects", GcsProjectsHandler).Methods("GET")
	api.HandleFunc("/gcs/{client_key}/projects/{project}/buckets", GcsBucketsHandler).Methods("GET")
	api.HandleFunc("/gcs/{scan_id}", GcsScanBucketsHandler).Methods("GET")
}

// The listings; tests replace them.
var (
	gcsProjects    = collect.GcsProjects
	gcsBuckets     = collect.GcsBuckets
	gcsScanBuckets = db.GcsScanBuckets
)

// checkGcsAccount answers an error unless the request's account is the
// user's and has granted Cloud Storage, and returns its granted scope.
func checkGcsAccount(w http.ResponseWriter, r *http.Request) (clientKey string, scope string, ok bool) {
	clientKey = mux.Vars(r)["client_key"]
	userID := currentUser(r).ID
	if status, msg := checkAccountService(userID, clientKey, db.ServiceGcs, "Google Cloud Storage"); status != http.StatusOK {
		http.Error(w, msg, status)
		return "", "", false
	}
	account, err := accountFor(userID, clientKey)
	if err != nil {
		slog.Error("Failed to look up linked account", "client_key", clientKey, "error", err)
		http.Error(w, "Failed to look up the account", http.StatusInternalServerError)
		return "", "", false
	}
	return clientKey, account.Scope, true
}

// writeGcsError answers Google's refusal as is, and anything else as a 500.
func writeGcsError(w http.ResponseWriter, err error, what string) {
	var gcsErr *collect.GcsError
	if errors.As(err, &gcsErr) {
		http.Error(w, gcsErr.Message, gcsErr.Status)
		return
	}
	slog.Error("Failed to list "+what, "error", err)
	http.Error(w, "Failed to list "+what, http.StatusInternalServerError)
}

// GcsProjectsHandler answers the account's active Cloud projects.
func GcsProjectsHandler(w http.ResponseWriter, r *http.Request) {
	clientKey, scope, ok := checkGcsAccount(w, r)
	if !ok {
		return
	}
	if !db.CanListProjects(scope) {
		http.Error(w, "This account didn't allow listing its projects. Type a project ID instead.", http.StatusConflict)
		return
	}
	projects, err := gcsProjects(currentUser(r).ID, clientKey)
	if err != nil {
		writeGcsError(w, err, "projects")
		return
	}
	writeJSONResponse(w, projects, http.StatusOK)
}

// GcsBucketsHandler answers a project's buckets, with their settings.
func GcsBucketsHandler(w http.ResponseWriter, r *http.Request) {
	clientKey, _, ok := checkGcsAccount(w, r)
	if !ok {
		return
	}
	project := mux.Vars(r)["project"]
	if !collect.ValidProjectId(project) {
		http.Error(w, "That isn't a Google Cloud project ID.", http.StatusBadRequest)
		return
	}
	buckets, err := gcsBuckets(currentUser(r).ID, clientKey, project)
	if err != nil {
		writeGcsError(w, err, "buckets")
		return
	}
	writeJSONResponse(w, buckets, http.StatusOK)
}

// maxPrefixLength is the longest prefix a scan takes: an object name's
// longest, in bytes.
const maxPrefixLength = 1024

// checkGcsScan checks a Cloud Storage scan request's own fields; the
// account is checked like any other scan's.
func checkGcsScan(scan collect.GStorageScan) (int, string) {
	switch {
	case scan.ClientKey == "":
		return http.StatusBadRequest, "Pick an account."
	case !collect.ValidProjectId(scan.ProjectId):
		return http.StatusBadRequest, "That isn't a Google Cloud project ID."
	case scan.Bucket != "" && !collect.ValidBucketName(scan.Bucket):
		return http.StatusBadRequest, "That isn't a bucket name."
	case scan.Prefix != "" && scan.Bucket == "":
		return http.StatusBadRequest, "A prefix needs a bucket."
	case len(scan.Prefix) > maxPrefixLength:
		return http.StatusBadRequest, fmt.Sprintf("The prefix is too long: %d bytes, the most is %d.", len(scan.Prefix), maxPrefixLength)
	}
	return http.StatusOK, ""
}

// GcsScanBucketsHandler answers what a Cloud Storage scan did with each
// bucket, with its totals.
func GcsScanBucketsHandler(w http.ResponseWriter, r *http.Request) {
	scanId, ok := getIntFromMap(mux.Vars(r), "scan_id")
	if !ok {
		http.Error(w, "Invalid scan ID", http.StatusBadRequest)
		return
	}
	if !checkScanOwner(w, r, scanId) {
		return
	}
	buckets, err := gcsScanBuckets(scanId)
	if err != nil {
		slog.Error("Failed to get a scan's buckets", "scan_id", scanId, "error", err)
		http.Error(w, "Failed to retrieve the scan's buckets", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, buckets, http.StatusOK)
}
