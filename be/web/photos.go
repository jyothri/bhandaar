package web

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/collect"
	"github.com/jyothri/hdd/db"
)

// Google Photos scans: the user picks items in Google Photos, and the
// backend scans what they picked. See docs/specs/photos-picker.md,
// "Backend".

func photosRoutes(api *mux.Router) {
	api.HandleFunc("/photos/sessions", StartPhotosPickHandler).Methods("POST")
	api.HandleFunc("/photos/sessions/{session_key}", PhotosPickHandler).Methods("GET")
	api.HandleFunc("/photos/sessions/{session_key}", CancelPhotosPickHandler).Methods("DELETE")
	api.HandleFunc("/photos/{scan_id}", PickedItemsHandler).Methods("GET")
}

// The Picker calls; tests replace them.
var (
	startPhotosPick  = collect.StartPhotosPick
	cancelPhotosPick = collect.CancelPhotosPick
	pickerSession    = db.GetPickerSession
	pickedItems      = db.PickedItems
)

type startPhotosPickRequest struct {
	ClientKey string `json:"clientKey"`
}

type startPhotosPickResponse struct {
	SessionKey string `json:"sessionKey"`
	// Where the user picks; the UI appends "/autoclose".
	PickerUri string    `json:"pickerUri"`
	PickBy    time.Time `json:"pickBy"`
}

type photosPickResponse struct {
	State  string    `json:"state"`
	ScanId *int64    `json:"scanId,omitempty"`
	PickBy time.Time `json:"pickBy"`
}

// StartPhotosPickHandler starts a picking session for one of the user's
// linked accounts that has granted Photos.
func StartPhotosPickHandler(w http.ResponseWriter, r *http.Request) {
	var req startPhotosPickRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if handleMaxBytesError(w, r, err, ScanRequestMaxBodySize) {
		return
	}
	if err != nil || req.ClientKey == "" {
		http.Error(w, "Pick an account", http.StatusBadRequest)
		return
	}
	userID := currentUser(r).ID
	if status, msg := checkAccountService(userID, req.ClientKey, db.ServicePhotos, "Google Photos"); status != http.StatusOK {
		http.Error(w, msg, status)
		return
	}
	session, err := startPhotosPick(userID, req.ClientKey)
	if errors.Is(err, db.ErrPickActive) {
		http.Error(w, "A Google Photos pick is already in progress. Finish or cancel it first.", http.StatusConflict)
		return
	}
	if err != nil {
		slog.Error("Failed to start a Photos pick", "client_key", req.ClientKey, "error", err)
		http.Error(w, "Failed to start picking in Google Photos", http.StatusBadGateway)
		return
	}
	writeJSONResponse(w, startPhotosPickResponse{SessionKey: session.SessionKey, PickerUri: session.PickerUri,
		PickBy: session.PickBy}, http.StatusOK)
}

// PhotosPickHandler answers a picking session's state, and its scan once
// there is one.
func PhotosPickHandler(w http.ResponseWriter, r *http.Request) {
	session, err := pickerSession(currentUser(r).ID, mux.Vars(r)["session_key"])
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "Pick not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("Failed to get a Photos pick", "error", err)
		http.Error(w, "Failed to look up the pick", http.StatusInternalServerError)
		return
	}
	body := photosPickResponse{State: session.State, PickBy: session.PickBy}
	if session.ScanId.Valid {
		body.ScanId = &session.ScanId.Int64
	}
	writeJSONResponse(w, body, http.StatusOK)
}

// CancelPhotosPickHandler cancels a picking session the user hasn't picked
// in yet.
func CancelPhotosPickHandler(w http.ResponseWriter, r *http.Request) {
	err := cancelPhotosPick(currentUser(r).ID, mux.Vars(r)["session_key"])
	switch {
	case errors.Is(err, db.ErrNotFound):
		http.Error(w, "Pick not found", http.StatusNotFound)
	case errors.Is(err, collect.ErrPickNotWaiting):
		http.Error(w, "The pick has already finished.", http.StatusConflict)
	case err != nil:
		slog.Error("Failed to cancel a Photos pick", "error", err)
		http.Error(w, "Failed to cancel the pick", http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// PickedItemsHandler answers a page of a Photos scan's picked items.
func PickedItemsHandler(w http.ResponseWriter, r *http.Request) {
	scanId, ok := getIntFromMap(mux.Vars(r), "scan_id")
	if !ok {
		http.Error(w, "Invalid scan ID", http.StatusBadRequest)
		return
	}
	if !checkScanOwner(w, r, scanId) {
		return
	}
	page, err := pickedItems(scanId, queryPage(r))
	if err != nil {
		slog.Error("Failed to get picked items", "scan_id", scanId, "error", err)
		http.Error(w, "Failed to retrieve the picked items", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, page, http.StatusOK)
}
