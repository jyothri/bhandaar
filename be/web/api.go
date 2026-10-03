package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

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

	// Scan POST endpoint with larger body limit (1 MB)
	scanPostRouter := api.PathPrefix("/scans").Subrouter()
	scanPostRouter.Use(RequestSizeLimitMiddleware(ScanRequestMaxBodySize))
	scanPostRouter.HandleFunc("", DoScansHandler).Methods("POST")

	// Other scan endpoints use default limit
	api.HandleFunc("/scans/requests/{account_key}", GetScanRequestsHandler).Methods("GET")
	api.HandleFunc("/scans/accounts", GetAccountsHandler).Methods("GET")
	api.HandleFunc("/scans/{scan_id}", DeleteScanHandler).Methods("DELETE")
	api.HandleFunc("/scans/{scan_id}/summary", ScanSummaryHandler).Methods("GET")
	api.HandleFunc("/scans", ListScansHandler).Methods("GET").Queries("page", "{page}")
	api.HandleFunc("/scans", ListScansHandler).Methods("GET")
	api.HandleFunc("/accounts", GetRequestAccountsHandler).Methods("GET")
	api.HandleFunc("/scans/{scan_id}", ListScanDataHandler).Methods("GET").Queries("page", "{page}")
	api.HandleFunc("/scans/{scan_id}", ListScanDataHandler).Methods("GET")
	api.HandleFunc("/gmaildata/{scan_id}", ListMessageMetaDataHandler).Methods("GET").Queries("page", "{page}")
	api.HandleFunc("/gmaildata/{scan_id}", ListMessageMetaDataHandler).Methods("GET")
	photosRoutes(api)
	gcsRoutes(api)
	browseRoutes(api)
	manageDataRoutes(api)
	duplicatesRoutes(api)
	settingsRoutes(api)
}

func DoScansHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	var doScanRequest DoScanRequest
	err := decoder.Decode(&doScanRequest)

	// Check if error is due to size limit
	if handleMaxBytesError(w, r, err, ScanRequestMaxBodySize) {
		return
	}

	if err != nil {
		slog.Error("Failed to decode scan request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	slog.Info(fmt.Sprintf("Received request: %v", doScanRequest))

	var scanId int
	userID := currentUser(r).ID
	if status, msg := checkScanRequest(doScanRequest, userID); status != http.StatusOK {
		http.Error(w, msg, status)
		return
	}
	switch doScanRequest.ScanType {
	case "Local":
		scanId, err = collect.LocalDrive(doScanRequest.LocalScan, userID)
	case "GDrive":
		scanId, err = collect.CloudDrive(doScanRequest.GDriveScan, userID)
	case "GMail":
		scanId, err = collect.Gmail(doScanRequest.GMailScan, userID)
	case "GPhotos":
		// The Photos Library API no longer lists a library; Photos scans
		// start from a pick (docs/archive/photos-picker.md).
		http.Error(w, "Start a Google Photos scan by picking photos on the Request page.", http.StatusBadRequest)
		return
	case "GStorage":
		scanId, err = collect.CloudStorage(doScanRequest.GStorageScan, userID)
	default:
		slog.Error("Unknown scan type", "scan_type", doScanRequest.ScanType)
		http.Error(w, fmt.Sprintf("Unknown scan type: %s", doScanRequest.ScanType), http.StatusBadRequest)
		return
	}

	var requestErr *collect.RequestError
	if errors.As(err, &requestErr) {
		http.Error(w, requestErr.Message, http.StatusBadRequest)
		return
	}
	var gcsErr *collect.GcsError
	if errors.As(err, &gcsErr) {
		http.Error(w, gcsErr.Message, gcsErr.Status)
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

// maxQueryLength is the longest filter or query a scan can record
// (scanmetadata.search_filter).
const maxQueryLength = 2000

// accountFor looks up a user's linked account; tests replace it.
var accountFor = db.GetOAuthToken

// checkScanRequest checks a Gmail, Drive or Cloud Storage request before
// any scan is created: its query fits, and the account it names has
// granted access to that service. It returns http.StatusOK, or a status and a message the
// Request page can show as is.
func checkScanRequest(req DoScanRequest, userID int64) (int, string) {
	var query, clientKey, service, serviceName string
	switch req.ScanType {
	case "GMail":
		query, clientKey, service, serviceName = req.GMailScan.Filter, req.GMailScan.ClientKey, db.ServiceGmail, "Gmail"
	case "GDrive":
		query, clientKey, service, serviceName = req.GDriveScan.QueryString, req.GDriveScan.ClientKey, db.ServiceDrive, "Google Drive"
	case "GStorage":
		if status, msg := checkGcsScan(req.GStorageScan); status != http.StatusOK {
			return status, msg
		}
		clientKey, service, serviceName = req.GStorageScan.ClientKey, db.ServiceGcs, "Google Cloud Storage"
	default:
		return http.StatusOK, ""
	}
	if len(query) > maxQueryLength {
		return http.StatusBadRequest, fmt.Sprintf("The query is too long: %d characters, the most is %d.", len(query), maxQueryLength)
	}
	if folderId := req.GDriveScan.FolderId; req.ScanType == "GDrive" && folderId != "" && !collect.ValidFolderId(folderId) {
		return http.StatusBadRequest, "That isn't a Google Drive folder ID."
	}
	if clientKey == "" {
		// A raw RefreshToken instead; the collector checks it.
		return http.StatusOK, ""
	}
	return checkAccountService(userID, clientKey, service, serviceName)
}

// checkAccountService checks that userID linked the account clientKey and
// that it has granted access to service (serviceName as people read it).
// It returns http.StatusOK, or a status and a message the UI can show.
func checkAccountService(userID int64, clientKey string, service string, serviceName string) (int, string) {
	account, err := accountFor(userID, clientKey)
	if errors.Is(err, sql.ErrNoRows) {
		return http.StatusBadRequest, "This account isn't linked. Pick another, or link it again."
	}
	if err != nil {
		slog.Error("Failed to look up linked account", "client_key", clientKey, "error", err)
		return http.StatusInternalServerError, "Failed to look up the account"
	}
	if !db.HasService(account.Scope, service) {
		return http.StatusBadRequest, fmt.Sprintf("This account hasn't granted %s access. Use \"Grant %s access\" first.", serviceName, strings.TrimPrefix(serviceName, "Google "))
	}
	return http.StatusOK, ""
}

func ListScansHandler(w http.ResponseWriter, r *http.Request) {
	pageNo := getPageNumber(mux.Vars(r))
	scans, totResults, err := db.GetScansFromDb(currentUser(r).ID, pageNo)
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
	accounts, err := db.GetRequestAccountsFromDb(currentUser(r).ID)
	if err != nil {
		slog.Error("Failed to get request accounts from database", "error", err)
		http.Error(w, "Failed to retrieve accounts", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, accounts, http.StatusOK)
}

func GetScanRequestsHandler(w http.ResponseWriter, r *http.Request) {
	// A linked account's client_key.
	clientKey := mux.Vars(r)["account_key"]
	accountRequests, err := db.GetScanRequestsFromDb(currentUser(r).ID, clientKey)
	if err != nil {
		slog.Error("Failed to get scan requests from database",
			"client_key", clientKey,
			"error", err)
		http.Error(w, "Failed to retrieve scan requests", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, accountRequests, http.StatusOK)
}

func GetAccountsHandler(w http.ResponseWriter, r *http.Request) {
	accounts, err := db.GetAccountsFromDb(currentUser(r).ID)
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
	if !checkScanOwner(w, r, scanId) {
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
	if !checkScanOwner(w, r, scanId) {
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

// ScanSummaryHandler answers a scan's details and totals.
func ScanSummaryHandler(w http.ResponseWriter, r *http.Request) {
	scanId, ok := getIntFromMap(mux.Vars(r), "scan_id")
	if !ok {
		http.Error(w, "Invalid scan ID", http.StatusBadRequest)
		return
	}
	if !checkScanOwner(w, r, scanId) {
		return
	}
	summary, err := scanSummary(scanId)
	if err != nil {
		slog.Error("Failed to get scan summary", "scan_id", scanId, "error", err)
		http.Error(w, "Failed to retrieve scan", http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, summary, http.StatusOK)
}

// scanSummary reads a scan's summary; tests replace it.
var scanSummary = db.GetScanSummary

func ListScanDataHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pageNo := getPageNumber(mux.Vars(r))
	scanId, ok := getIntFromMap(vars, "scan_id")
	if !ok {
		http.Error(w, "Invalid scan ID", http.StatusBadRequest)
		return
	}
	if !checkScanOwner(w, r, scanId) {
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

// scanOwnedBy reports whether a scan belongs to a user; tests replace it.
var scanOwnedBy = db.ScanOwnedBy

// checkScanOwner answers 404 unless the logged-in user owns scanId, so other
// users' scans look the same as missing ones.
func checkScanOwner(w http.ResponseWriter, r *http.Request, scanId int) bool {
	owned, err := scanOwnedBy(scanId, currentUser(r).ID)
	if err != nil {
		slog.Error("Failed to check scan owner", "scan_id", scanId, "error", err)
		http.Error(w, "Failed to retrieve scan", http.StatusInternalServerError)
		return false
	}
	if !owned {
		http.Error(w, "Scan not found", http.StatusNotFound)
		return false
	}
	return true
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
	PageInfo PaginationInfo   `json:"pagination_info"`
	ScanData []db.ScanDataRow `json:"scan_data"`
}

type DoScanRequest struct {
	ScanType   string
	LocalScan  collect.LocalScan
	GDriveScan collect.GDriveScan
	GMailScan  collect.GMailScan
	// Google Cloud Storage.
	GStorageScan collect.GStorageScan
}

type DoScanResponse struct {
	ScanId int `json:"scan_id"`
}

type MessageMetadataResponse struct {
	PageInfo        PaginationInfo  `json:"pagination_info"`
	MessageMetadata []db.MessageRow `json:"message_metadata"`
}
