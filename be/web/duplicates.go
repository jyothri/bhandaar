package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/db"
)

// Duplicates: identical files and folders across a user's sources, and
// likely copies of their Google Photos. See docs/specs/duplicates.md, "API".

func duplicatesRoutes(api *mux.Router) {
	api.HandleFunc("/duplicates/summary", DupSummaryHandler).Methods("GET")
	api.HandleFunc("/duplicates/groups", DupGroupsHandler).Methods("GET")
	api.HandleFunc("/duplicates/members", DupMembersHandler).Methods("GET")
}

func DupSummaryHandler(w http.ResponseWriter, r *http.Request) {
	summary, err := db.GetDupSummary(currentUser(r).ID)
	if err != nil {
		slog.Error("Failed to read the duplicates summary", "error", err)
		http.Error(w, "Failed to read your duplicates", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, summary, http.StatusOK)
}

// dupFilter reads a page's filter from the query: kind (file, the
// default, folder or photo), source, across, min_size, hide_same_physical.
func dupFilter(r *http.Request) (db.DupFilter, bool) {
	q := r.URL.Query()
	filter := db.DupFilter{Kind: q.Get("kind"), Source: q.Get("source"),
		Across: q.Get("across") == "1", HideSamePhysical: q.Get("hide_same_physical") == "1"}
	switch filter.Kind {
	case "":
		filter.Kind = db.DupFile
	case db.DupFile, db.DupFolder, db.DupPhoto:
	default:
		return filter, false
	}
	if s := q.Get("min_size"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			return filter, false
		}
		filter.MinSize = n
	}
	return filter, true
}

func DupGroupsHandler(w http.ResponseWriter, r *http.Request) {
	filter, ok := dupFilter(r)
	if !ok {
		http.Error(w, "Invalid filter", http.StatusBadRequest)
		return
	}
	page, err := db.GetDupGroups(currentUser(r).ID, filter, queryPage(r))
	if err != nil {
		slog.Error("Failed to read duplicate groups", "error", err)
		http.Error(w, "Failed to read your duplicates", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, page, http.StatusOK)
}

// DupMembersHandler answers a page of a group's copies, the group named by
// its kind and key (?kind=&key=), which stay the same when the index is
// rebuilt.
func DupMembersHandler(w http.ResponseWriter, r *http.Request) {
	kind, key := r.URL.Query().Get("kind"), r.URL.Query().Get("key")
	if kind != db.DupFile && kind != db.DupFolder && kind != db.DupPhoto || key == "" {
		http.Error(w, "Invalid group", http.StatusBadRequest)
		return
	}
	page, err := db.GetDupMembers(currentUser(r).ID, kind, key, queryPage(r))
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "Group not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("Failed to read a duplicate group's copies", "kind", kind, "key", key, "error", err)
		http.Error(w, "Failed to read the copies", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, page, http.StatusOK)
}
