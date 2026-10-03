package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/db"
)

// Settings: the user's linked accounts and uploaded drives, and deleting
// them. Deletions run as background jobs. See docs/specs/data-deletion.md.

func settingsRoutes(api *mux.Router) {
	api.HandleFunc("/settings/data", SettingsDataHandler).Methods("GET")
	api.HandleFunc("/agent-drives/{drive}", DeleteAgentDriveHandler).Methods("DELETE")
	api.HandleFunc("/accounts/{client_key}/{service:gmail|drive|gcs|photos}", DeleteServiceHandler).Methods("DELETE")
	api.HandleFunc("/accounts/{client_key}", DeleteAccountHandler).Methods("DELETE")
	api.HandleFunc("/deletions/{id}", DeletionHandler).Methods("GET")
}

// What the handlers use; tests replace them.
var (
	settingsData     = db.GetSettingsData
	requestAccounts  = db.GetRequestAccountsFromDb
	runningScanOf    = db.RunningScanOf
	agentDriveLabel  = db.AgentDriveLabel
	startDeletion    = db.StartDeletion
	finishDeletion   = db.FinishDeletion
	deleteAgentDrive = db.DeleteAgentDriveData
	deleteService    = db.DeleteServiceData
	deleteAccount    = db.DeleteAccountData
	refreshTokenOf   = db.RefreshTokenOf
	getDeletion      = db.GetDeletion
	// runJob runs a deletion in the background.
	runJob = func(job func()) { go job() }
)

// deletionMu runs one deletion at a time, so two large ones don't compete.
var deletionMu sync.Mutex

// revokeEndpoint is Google's token revocation endpoint; tests point it at
// a fake.
var revokeEndpoint = "https://oauth2.googleapis.com/revoke"

func SettingsDataHandler(w http.ResponseWriter, r *http.Request) {
	data, err := settingsData(currentUser(r).ID)
	if err != nil {
		slog.Error("Failed to get settings data", "error", err)
		http.Error(w, "Failed to list your accounts and drives", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, data, http.StatusOK)
}

// start records a deletion job and runs work in the background, unless a
// job for the same target is already running; it answers 202 with the job
// either way.
func start(w http.ResponseWriter, r *http.Request, kind string, target string, label string,
	work func() (counts map[string]int64, revoke string, err error)) {
	user := currentUser(r)
	job, started, err := startDeletion(user.ID, kind, target, label)
	if err != nil {
		slog.Error("Failed to start deletion", "kind", kind, "target", target, "error", err)
		http.Error(w, "Failed to start the deletion", http.StatusInternalServerError)
		return
	}
	if started {
		slog.Info("Deletion started", "job", job.ID, "user", user.Username, "kind", kind, "target", target, "label", label)
		runJob(func() {
			deletionMu.Lock()
			defer deletionMu.Unlock()
			counts, revoke, err := work()
			if err != nil {
				slog.Error("Deletion failed", "job", job.ID, "kind", kind, "target", target, "error", err)
			} else {
				slog.Info("Deletion done", "job", job.ID, "kind", kind, "target", target, "counts", counts, "revoke", revoke)
			}
			if err := finishDeletion(job.ID, counts, revoke, err); err != nil {
				slog.Error("Failed to record the end of a deletion", "job", job.ID, "error", err)
			}
		})
	}
	writeJSONResponse(w, job, http.StatusAccepted)
}

// jobError is what a failed job shows.
func jobError(err error) error {
	if errors.Is(err, db.ErrScanRunning) {
		return errors.New("a scan of this account started; wait for it to finish, then try again")
	}
	return err
}

func DeleteAgentDriveHandler(w http.ResponseWriter, r *http.Request) {
	drivePk, ok := checkAgentDrive(w, r)
	if !ok {
		return
	}
	label, err := agentDriveLabel(drivePk)
	if err != nil {
		slog.Error("Failed to look up drive", "drive", drivePk, "error", err)
		http.Error(w, "Failed to look up the drive", http.StatusInternalServerError)
		return
	}
	start(w, r, db.DeleteAgentDrive, strconv.FormatInt(drivePk, 10), label,
		func() (map[string]int64, string, error) {
			counts, err := deleteAgentDrive(drivePk)
			return counts, "", jobError(err)
		})
}

// accountLabel is the label of the request's account, which the user must
// have linked; it answers 404 otherwise.
func accountLabel(w http.ResponseWriter, r *http.Request) (clientKey string, label string, ok bool) {
	clientKey, ok = checkGoogleAccount(w, r)
	if !ok {
		return "", "", false
	}
	accounts, err := requestAccounts(currentUser(r).ID)
	if err != nil {
		slog.Error("Failed to list accounts", "error", err)
		http.Error(w, "Failed to look up the account", http.StatusInternalServerError)
		return "", "", false
	}
	label, ok = db.AccountLabels(accounts)[clientKey]
	if !ok {
		http.Error(w, "Account not found", http.StatusNotFound)
		return "", "", false
	}
	return clientKey, label, true
}

// checkNoRunningScan answers 409 while one of the account's scans runs.
func checkNoRunningScan(w http.ResponseWriter, r *http.Request, clientKey string) bool {
	scan, err := runningScanOf(currentUser(r).ID, clientKey)
	if err != nil {
		slog.Error("Failed to check for running scans", "client_key", clientKey, "error", err)
		http.Error(w, "Failed to check the account's scans", http.StatusInternalServerError)
		return false
	}
	if scan != 0 {
		http.Error(w, fmt.Sprintf("Scan %d of this account is running. Wait for it to finish, then try again.", scan),
			http.StatusConflict)
		return false
	}
	return true
}

// serviceNames name the services in job labels.
var serviceNames = map[string]string{
	"gmail":  "Gmail",
	"drive":  "Google Drive",
	"gcs":    "Cloud Storage",
	"photos": "Google Photos",
}

// DeleteServiceHandler deletes one service's data of an account: its scans
// and its records. The account stays linked.
func DeleteServiceHandler(w http.ResponseWriter, r *http.Request) {
	clientKey, label, ok := accountLabel(w, r)
	if !ok || !checkNoRunningScan(w, r, clientKey) {
		return
	}
	service := mux.Vars(r)["service"]
	userID := currentUser(r).ID
	start(w, r, service, clientKey, label+" · "+serviceNames[service],
		func() (map[string]int64, string, error) {
			counts, err := deleteService(userID, clientKey, service)
			return counts, "", jobError(err)
		})
}

// DisconnectRequest confirms disconnecting an account by its name.
type DisconnectRequest struct {
	Confirm string `json:"confirm"`
}

func DeleteAccountHandler(w http.ResponseWriter, r *http.Request) {
	clientKey, label, ok := accountLabel(w, r)
	if !ok {
		return
	}
	var req DisconnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	// The UI asks for the name too; this keeps a UI bug from skipping it.
	if strings.TrimSpace(req.Confirm) != label {
		http.Error(w, fmt.Sprintf("To disconnect this account, type its name exactly: %s", label), http.StatusBadRequest)
		return
	}
	if !checkNoRunningScan(w, r, clientKey) {
		return
	}
	userID := currentUser(r).ID
	start(w, r, db.DeleteAccount, clientKey, label,
		func() (map[string]int64, string, error) {
			token, err := refreshTokenOf(userID, clientKey)
			if err != nil {
				return nil, "", fmt.Errorf("failed to read the account's token: %w", err)
			}
			revoke := revokeToken(token)
			counts, err := deleteAccount(userID, clientKey)
			return counts, revoke, jobError(err)
		})
}

// revokeToken revokes a refresh token at Google, and says how it went:
// "revoked", "already revoked" (Google no longer knows it), or why it
// failed. It never stops a deletion.
func revokeToken(token string) string {
	if token == "" {
		return "already revoked"
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.PostForm(revokeEndpoint, url.Values{"token": {token}})
	if err != nil {
		return "failed: " + err.Error()
	}
	defer resp.Body.Close()
	var body struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	switch {
	case resp.StatusCode == http.StatusOK:
		return "revoked"
	case resp.StatusCode == http.StatusBadRequest && body.Error == "invalid_token":
		return "already revoked"
	default:
		return fmt.Sprintf("failed: Google answered %d %s", resp.StatusCode, body.Error)
	}
}

func DeletionHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil {
		http.Error(w, "Deletion not found", http.StatusNotFound)
		return
	}
	job, err := getDeletion(currentUser(r).ID, id)
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "Deletion not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("Failed to get deletion", "id", id, "error", err)
		http.Error(w, "Failed to get the deletion", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, job, http.StatusOK)
}
