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
		panic(err)
	}
	slog.Info(fmt.Sprintf("Received request: %v", doScanRequest))
	var body DoScanResponse
	switch doScanRequest.ScanType {
	case "Local":
		body = DoScanResponse{
			ScanId: collect.LocalDrive(doScanRequest.LocalScan),
		}
	case "GDrive":
		body = DoScanResponse{
			ScanId: collect.CloudDrive(doScanRequest.GDriveScan),
		}
	case "GMail":
		body = DoScanResponse{
			ScanId: collect.Gmail(doScanRequest.GMailScan),
		}
	case "GPhotos":
		body = DoScanResponse{
			ScanId: collect.Photos(doScanRequest.GPhotosScan),
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

func GetRequestAccountsHandler(w http.ResponseWriter, r *http.Request) {
	accounts := db.GetRequestAccountsFromDb()
	serializedBody, _ := json.Marshal(accounts)
	setJsonHeader(w)
	_, _ = w.Write(serializedBody)
}

func GetScanRequestsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	accountKey := vars["account_key"]
	accountRequests := db.GetScanRequestsFromDb(accountKey)
	serializedBody, _ := json.Marshal(accountRequests)
	setJsonHeader(w)
	_, _ = w.Write(serializedBody)
}

func GetAccountsHandler(w http.ResponseWriter, r *http.Request) {
	accounts := db.GetAccountsFromDb()
	serializedBody, _ := json.Marshal(accounts)
	setJsonHeader(w)
	_, _ = w.Write(serializedBody)
}

func DeleteScanHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	scanId, _ := getIntFromMap(vars, "scan_id")
	db.DeleteScan(scanId)
	w.WriteHeader(http.StatusOK)
}

func ListMessageMetaDataHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pageNo := getPageNumber(mux.Vars(r))
	scanId, _ := getIntFromMap(vars, "scan_id")
	messageMetadata, totResults := db.GetMessageMetadataFromDb(scanId, pageNo)
	pageInfo := PaginationInfo{Page: pageNo, Size: totResults}
	body := MessageMetadataResponse{
		PageInfo:        pageInfo,
		MessageMetadata: messageMetadata,
	}
	serializedBody, _ := json.Marshal(body)
	setJsonHeader(w)
	_, _ = w.Write(serializedBody)
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
	scanId, _ := getIntFromMap(vars, "scan_id")
	photosMediaItem, totResults := db.GetPhotosMediaItemFromDb(scanId, pageNo)
	pageInfo := PaginationInfo{Page: pageNo, Size: totResults}
	body := PhotosMediaItemResponse{
		PageInfo:        pageInfo,
		PhotosMediaItem: photosMediaItem,
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
