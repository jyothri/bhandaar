package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/collect"
	"github.com/jyothri/hdd/db"
)

func api(r *mux.Router) {
	// Handle API routes
	api := r.PathPrefix("/api/").Subrouter()
	api.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	api.HandleFunc("/scans", DoScansHandler).Methods("POST")
	api.HandleFunc("/scans/requests/{account_key}", GetScanRequestsHandler).Methods("GET")
	api.HandleFunc("/scans/accounts", GetAccountsHandler).Methods("GET")
	api.HandleFunc("/scans/{scan_id}", DeleteScanHandler).Methods("DELETE")
	api.HandleFunc("/scans", ListScansHandler).Methods("GET").Queries("page", "{page}")
	api.HandleFunc("/scans", ListScansHandler).Methods("GET")
	api.HandleFunc("/accounts", GetRequestAccountsHandler).Methods("GET")
	api.HandleFunc("/scans/{scan_id}", ListScanDataHandler).Methods("GET").Queries("page", "{page}")
	api.HandleFunc("/scans/{scan_id}", ListScanDataHandler).Methods("GET")
	api.HandleFunc("/gmaildata/{scan_id}", ListMessageMetaDataHandler).Methods("GET").Queries("page", "{page}")
	api.HandleFunc("/gmaildata/{scan_id}", ListMessageMetaDataHandler).Methods("GET")
	api.HandleFunc("/photos/albums", ListAlbumsHandler).Methods("GET").Queries("refresh_token", "{refresh_token}")
	api.HandleFunc("/photos/{scan_id}", ListPhotosHandler).Methods("GET").Queries("page", "{page}")
	api.HandleFunc("/photos/{scan_id}", ListPhotosHandler).Methods("GET")
}

func DoScansHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	var doScanRequest DoScanRequest
	err := decoder.Decode(&doScanRequest)
	if err != nil {
		slog.Error("Failed to decode scan request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	slog.Info(fmt.Sprintf("Received request: %v", doScanRequest))

	var scanId int
	switch doScanRequest.ScanType {
	case "Local":
		scanId, err = collect.LocalDrive(doScanRequest.LocalScan)
	case "GDrive":
		scanId, err = collect.CloudDrive(doScanRequest.GDriveScan)
	case "GMail":
		scanId, err = collect.Gmail(doScanRequest.GMailScan)
	case "GPhotos":
		scanId, err = collect.Photos(doScanRequest.GPhotosScan)
	default:
		slog.Error("Unknown scan type", "scan_type", doScanRequest.ScanType)
		http.Error(w, fmt.Sprintf("Unknown scan type: %s", doScanRequest.ScanType), http.StatusBadRequest)
		return
	}

	if err != nil {
		slog.Error("Failed to start scan",
			"scan_type", doScanRequest.ScanType,
			"error", err)
		http.Error(w, fmt.Sprintf("Failed to start scan: %v", err), http.StatusInternalServerError)
		return
	}

	body := DoScanResponse{ScanId: scanId}
	writeJSONResponse(w, body, http.StatusOK)
}

func ListScansHandler(w http.ResponseWriter, r *http.Request) {
	pageNo := getPageNumber(mux.Vars(r))
	scans, totResults, err := db.GetScansFromDb(pageNo)
	if err != nil {
		slog.Error("Failed to get scans from database",
			"page", pageNo,
			"error", err)
		http.Error(w, "Failed to retrieve scans", http.StatusInternalServerError)
		return
	}

	pageInfo := PaginationInfo{Page: pageNo, Size: totResults}
	body := ScansResponse{
		PageInfo: pageInfo,
		Scans:    scans,
	}
	writeJSONResponse(w, body, http.StatusOK)
}

func GetRequestAccountsHandler(w http.ResponseWriter, r *http.Request) {
	accounts, err := db.GetRequestAccountsFromDb()
	if err != nil {
		slog.Error("Failed to get request accounts from database", "error", err)
		http.Error(w, "Failed to retrieve accounts", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, accounts, http.StatusOK)
}

func GetScanRequestsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	accountKey := vars["account_key"]
	accountRequests, err := db.GetScanRequestsFromDb(accountKey)
	if err != nil {
		slog.Error("Failed to get scan requests from database",
			"account_key", accountKey,
			"error", err)
		http.Error(w, "Failed to retrieve scan requests", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, accountRequests, http.StatusOK)
}

func GetAccountsHandler(w http.ResponseWriter, r *http.Request) {
	accounts, err := db.GetAccountsFromDb()
	if err != nil {
		slog.Error("Failed to get accounts from database", "error", err)
		http.Error(w, "Failed to retrieve accounts", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, accounts, http.StatusOK)
}

func DeleteScanHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	scanId, ok := getIntFromMap(vars, "scan_id")
	if !ok {
		http.Error(w, "Invalid scan ID", http.StatusBadRequest)
		return
	}

	if err := db.DeleteScan(scanId); err != nil {
		slog.Error("Failed to delete scan", "error", err, "scan_id", scanId)
		http.Error(w, "Failed to delete scan", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func ListMessageMetaDataHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pageNo := getPageNumber(mux.Vars(r))
	scanId, ok := getIntFromMap(vars, "scan_id")
	if !ok {
		http.Error(w, "Invalid scan ID", http.StatusBadRequest)
		return
	}

	messageMetadata, totResults, err := db.GetMessageMetadataFromDb(scanId, pageNo)
	if err != nil {
		slog.Error("Failed to get message metadata from database",
			"scan_id", scanId,
			"page", pageNo,
			"error", err)
		http.Error(w, "Failed to retrieve message metadata", http.StatusInternalServerError)
		return
	}

	pageInfo := PaginationInfo{Page: pageNo, Size: totResults}
	body := MessageMetadataResponse{
		PageInfo:        pageInfo,
		MessageMetadata: messageMetadata,
	}
	writeJSONResponse(w, body, http.StatusOK)
}

func ListAlbumsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	refresh_token, present := vars["refresh_token"]
	if !present {
		slog.Warn("No refresh token to execute ListAlbumsHandler.")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	albums := collect.ListAlbums(refresh_token)
	pageInfo := PaginationInfo{Page: 1, Size: len(albums)}
	body := ListAlbumsResponse{
		PageInfo: pageInfo,
		Albums:   albums,
	}
	serializedBody, _ := json.Marshal(body)
	setJsonHeader(w)
	_, _ = w.Write(serializedBody)
}

func ListPhotosHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pageNo := getPageNumber(mux.Vars(r))
	scanId, ok := getIntFromMap(vars, "scan_id")
	if !ok {
		http.Error(w, "Invalid scan ID", http.StatusBadRequest)
		return
	}

	photosMediaItem, totResults, err := db.GetPhotosMediaItemFromDb(scanId, pageNo)
	if err != nil {
		slog.Error("Failed to get photos from database",
			"scan_id", scanId,
			"page", pageNo,
			"error", err)
		http.Error(w, "Failed to retrieve photos", http.StatusInternalServerError)
		return
	}

	pageInfo := PaginationInfo{Page: pageNo, Size: totResults}
	body := PhotosMediaItemResponse{
		PageInfo:        pageInfo,
		PhotosMediaItem: photosMediaItem,
	}
	writeJSONResponse(w, body, http.StatusOK)
}

func ListScanDataHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pageNo := getPageNumber(mux.Vars(r))
	scanId, ok := getIntFromMap(vars, "scan_id")
	if !ok {
		http.Error(w, "Invalid scan ID", http.StatusBadRequest)
		return
	}

	scanData, totResults, err := db.GetScanDataFromDb(scanId, pageNo)
	if err != nil {
		slog.Error("Failed to get scan data from database",
			"scan_id", scanId,
			"page", pageNo,
			"error", err)
		http.Error(w, "Failed to retrieve scan data", http.StatusInternalServerError)
		return
	}

	pageInfo := PaginationInfo{Page: pageNo, Size: totResults}
	body := ScanDataResponse{
		PageInfo: pageInfo,
		ScanData: scanData,
	}
	writeJSONResponse(w, body, http.StatusOK)
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
}

// writeJSONResponse writes a JSON response with the given status code
func writeJSONResponse(w http.ResponseWriter, data interface{}, statusCode int) {
	w.Header().Set("Content-Type", "application/json")

	serializedBody, err := json.Marshal(data)
	if err != nil {
		slog.Error("Failed to marshal JSON", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(statusCode)

	if _, err := w.Write(serializedBody); err != nil {
		slog.Error("Failed to write response", "error", err)
	}
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
	ScanType    string
	LocalScan   collect.LocalScan
	GDriveScan  collect.GDriveScan
	GMailScan   collect.GMailScan
	GPhotosScan collect.GPhotosScan
}

type DoScanResponse struct {
	ScanId int `json:"scan_id"`
}

type MessageMetadataResponse struct {
	PageInfo        PaginationInfo           `json:"pagination_info"`
	MessageMetadata []db.MessageMetadataRead `json:"message_metadata"`
}

type PhotosMediaItemResponse struct {
	PageInfo        PaginationInfo           `json:"pagination_info"`
	PhotosMediaItem []db.PhotosMediaItemRead `json:"photos_media_item"`
}

type ListAlbumsResponse struct {
	PageInfo PaginationInfo  `json:"pagination_info"`
	Albums   []collect.Album `json:"albums"`
}
