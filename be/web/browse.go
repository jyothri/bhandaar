package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/db"
)

// Browse: a user's Google accounts and agent drives, one folder at a time.
// See docs/archive/browse.md, "Browse API".

func browseRoutes(api *mux.Router) {
	api.HandleFunc("/browse/sources", BrowseSourcesHandler).Methods("GET")
	api.HandleFunc("/browse/google/{client_key}/drive/children", DriveChildrenHandler).Methods("GET")
	api.HandleFunc("/browse/google/{client_key}/gmail/messages", AccountMessagesHandler).Methods("GET")
	api.HandleFunc("/browse/google/{client_key}/photos/items", AccountPhotosHandler).Methods("GET")
	api.HandleFunc("/browse/agent/{drive}/children", AgentChildrenHandler).Methods("GET")
	api.HandleFunc("/browse/agent/{drive}/status", AgentStatusHandler).Methods("GET")
}

// The owner checks; tests replace them.
var (
	googleAccountOwnedBy = db.GoogleAccountOwnedBy
	agentDriveOwnedBy    = db.AgentDriveOwnedBy
)

func BrowseSourcesHandler(w http.ResponseWriter, r *http.Request) {
	sources, err := db.BrowseSources(currentUser(r).ID)
	if err != nil {
		slog.Error("Failed to list browse sources", "error", err)
		http.Error(w, "Failed to list what you can browse", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, sources, http.StatusOK)
}

// queryPage is the request's page query parameter, from 1.
func queryPage(r *http.Request) int {
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		return 1
	}
	return page
}

// checkGoogleAccount answers 404 unless the logged-in user linked the
// request's account, and returns its client key.
func checkGoogleAccount(w http.ResponseWriter, r *http.Request) (string, bool) {
	clientKey := mux.Vars(r)["client_key"]
	owned, err := googleAccountOwnedBy(currentUser(r).ID, clientKey)
	if err != nil {
		slog.Error("Failed to check account owner", "client_key", clientKey, "error", err)
		http.Error(w, "Failed to look up the account", http.StatusInternalServerError)
		return "", false
	}
	if !owned {
		http.Error(w, "Account not found", http.StatusNotFound)
		return "", false
	}
	return clientKey, true
}

// checkAgentDrive answers 404 unless the request's drive is one of the
// logged-in user's agents', and returns its ID.
func checkAgentDrive(w http.ResponseWriter, r *http.Request) (int64, bool) {
	drivePk, err := strconv.ParseInt(mux.Vars(r)["drive"], 10, 64)
	if err != nil {
		http.Error(w, "Drive not found", http.StatusNotFound)
		return 0, false
	}
	owned, err := agentDriveOwnedBy(currentUser(r).ID, drivePk)
	if err != nil {
		slog.Error("Failed to check drive owner", "drive", drivePk, "error", err)
		http.Error(w, "Failed to look up the drive", http.StatusInternalServerError)
		return 0, false
	}
	if !owned {
		http.Error(w, "Drive not found", http.StatusNotFound)
		return 0, false
	}
	return drivePk, true
}

// writeFolderPage answers a folder's page, or 404 for a folder that isn't
// there.
func writeFolderPage(w http.ResponseWriter, page db.FolderPage, err error, source string, folder string) {
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "Folder not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("Failed to list folder", "source", source, "folder", folder, "error", err)
		http.Error(w, "Failed to list the folder", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, page, http.StatusOK)
}

func DriveChildrenHandler(w http.ResponseWriter, r *http.Request) {
	clientKey, ok := checkGoogleAccount(w, r)
	if !ok {
		return
	}
	folder := r.URL.Query().Get("folder")
	page, err := db.DriveChildren(clientKey, folder, queryPage(r))
	writeFolderPage(w, page, err, "google/"+clientKey, folder)
}

func AgentChildrenHandler(w http.ResponseWriter, r *http.Request) {
	drivePk, ok := checkAgentDrive(w, r)
	if !ok {
		return
	}
	folder := r.URL.Query().Get("folder")
	page, err := db.AgentChildren(drivePk, folder, queryPage(r))
	writeFolderPage(w, page, err, "agent/"+strconv.FormatInt(drivePk, 10), folder)
}

func AgentStatusHandler(w http.ResponseWriter, r *http.Request) {
	drivePk, ok := checkAgentDrive(w, r)
	if !ok {
		return
	}
	status, err := db.GetAgentDriveStatus(drivePk)
	if err != nil {
		slog.Error("Failed to get agent drive status", "drive", drivePk, "error", err)
		http.Error(w, "Failed to get the drive's status", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, status, http.StatusOK)
}

func AccountMessagesHandler(w http.ResponseWriter, r *http.Request) {
	clientKey, ok := checkGoogleAccount(w, r)
	if !ok {
		return
	}
	page, err := db.AccountMessages(currentUser(r).ID, clientKey, r.URL.Query().Get("sort"), queryPage(r))
	if err != nil {
		slog.Error("Failed to list account messages", "client_key", clientKey, "error", err)
		http.Error(w, "Failed to list the messages", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, page, http.StatusOK)
}

func AccountPhotosHandler(w http.ResponseWriter, r *http.Request) {
	clientKey, ok := checkGoogleAccount(w, r)
	if !ok {
		return
	}
	page, err := db.AccountPhotos(currentUser(r).ID, clientKey, r.URL.Query().Get("sort"), queryPage(r))
	if err != nil {
		slog.Error("Failed to list account photos", "client_key", clientKey, "error", err)
		http.Error(w, "Failed to list the photos", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, page, http.StatusOK)
}
