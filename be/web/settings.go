package web

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/db"
)

// The user's settings, from the Settings page.

func settingsRoutes(api *mux.Router) {
	api.HandleFunc("/settings", GetSettingsHandler).Methods("GET")
	api.HandleFunc("/settings", SaveSettingsHandler).Methods("PUT")
}

// The settings store; tests replace them.
var (
	getSettings  = db.GetSettings
	saveSettings = db.SaveSettings
)

func GetSettingsHandler(w http.ResponseWriter, r *http.Request) {
	s, err := getSettings(currentUser(r).ID)
	if err != nil {
		slog.Error("Failed to read settings", "error", err)
		http.Error(w, "Failed to read your settings", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, s, http.StatusOK)
}

// SaveSettingsHandler replaces the user's settings with the body's, and
// answers them.
func SaveSettingsHandler(w http.ResponseWriter, r *http.Request) {
	var s db.Settings
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&s); err != nil {
		http.Error(w, "Invalid settings", http.StatusBadRequest)
		return
	}
	if err := saveSettings(currentUser(r).ID, s); err != nil {
		slog.Error("Failed to save settings", "error", err)
		http.Error(w, "Failed to save your settings", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, s, http.StatusOK)
}
