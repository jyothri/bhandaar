package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/db"
)

func ListScansHandler(w http.ResponseWriter, r *http.Request) {
	pageNo := getPageNumber(mux.Vars(r))
	scans, totResults := db.GetScansFromDb(pageNo)
	pageInfo := PaginationInfo{Page: pageNo, Size: totResults}
	body := ScansResponse{
		PageInfo: pageInfo,
		Scans:    scans,
	}
	serializedBody, _ := json.Marshal(body)
	setJsonHeader(w)
	_, _ = w.Write(serializedBody)
}

func ListScanDataHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pageNo := getPageNumber(mux.Vars(r))
	scanId, _ := getIntFromMap(vars, "scan_id")
	scanData, totResults := db.GetScanDataFromDb(scanId, pageNo)
	pageInfo := PaginationInfo{Page: pageNo, Size: totResults}
	body := ScanDataResponse{
		PageInfo: pageInfo,
		ScanData: scanData,
	}
	serializedBody, _ := json.Marshal(body)
	setJsonHeader(w)
	_, _ = w.Write(serializedBody)
}

func StartWebServer() {
	r := mux.NewRouter()
	// Handle API routes
	api := r.PathPrefix("/api/").Subrouter()
	api.HandleFunc("/scans", ListScansHandler).Methods("GET").Queries("page", "{page}")
	api.HandleFunc("/scans", ListScansHandler).Methods("GET")
	api.HandleFunc("/scans/{scan_id}", ListScanDataHandler).Methods("GET").Queries("page", "{page}")
	api.HandleFunc("/scans/{scan_id}", ListScanDataHandler).Methods("GET")
	r.PathPrefix("/").Handler(http.FileServer(http.Dir("web/svelte/public"))).Methods("GET")
	http.ListenAndServe(":8090", r)
}

func getIntFromMap(vars map[string]string, field string) (int, bool) {
	field, present := vars[field]
	if !present {
		return 0, false
	}
	fieldInt, err := strconv.Atoi(field)
	if err != nil {
		return 0, false
	}
	return fieldInt, true
}

func getPageNumber(vars map[string]string) int {
	page, present := getIntFromMap(vars, "page")
	if !present {
		return 1
	}
	return page
}

func setJsonHeader(w http.ResponseWriter) {
	w.Header().Set(
		"Content-Type",
		"application/json",
	)
	w.Header().Set(
		"Access-Control-Allow-Origin",
		"http://localhost:8080",
	)
}

type PaginationInfo struct {
	Size int `json:"size"`
	Page int `json:"page"`
}

type ScansResponse struct {
	PageInfo PaginationInfo `json:"pagination_info"`
	Scans    []db.Scan      `json:"scans"`
}

type ScanDataResponse struct {
	PageInfo PaginationInfo `json:"pagination_info"`
	ScanData []db.ScanData  `json:"scan_data"`
}
