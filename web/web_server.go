package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/collect"
	"github.com/jyothri/hdd/db"
)

func DoScansHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	var doScanRequest DoScanRequest
	err := decoder.Decode(&doScanRequest)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Received request: %v\n", doScanRequest)
	var body DoScanResponse
	switch doScanRequest.ScanType {
	case "Local":
		body = DoScanResponse{
			ScanId: collect.LocalDrive(doScanRequest.LocalPath),
		}
	case "Google Drive":
		body = DoScanResponse{
			ScanId: collect.CloudDrive(doScanRequest.Filter),
		}
	case "Google Storage":
		body = DoScanResponse{
			ScanId: collect.CloudStorage(doScanRequest.Bucket),
		}
	default:
		body = DoScanResponse{
			ScanId: -1,
		}
	}
	serializedBody, _ := json.Marshal(body)
	setJsonHeader(w)
	_, _ = w.Write(serializedBody)
}

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

func DeleteScanHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	scanId, _ := getIntFromMap(vars, "scan_id")
	db.DeleteScan(scanId)
	w.WriteHeader(http.StatusOK)
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
	api.HandleFunc("/scans", DoScansHandler).Methods("POST")
	api.HandleFunc("/scans/{scan_id}", DeleteScanHandler).Methods("DELETE")
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

type DoScanRequest struct {
	ScanType  string `json:"scan_type"`
	LocalPath string `json:"localPath"`
	Filter    string `json:"filter"`
	Bucket    string `json:"gs_bucket"`
}

type DoScanResponse struct {
	ScanId int `json:"scan_id"`
}
