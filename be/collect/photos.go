package collect

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jyothri/hdd/constants"
	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/notification"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/time/rate"
)

var photosApiBaseUrl = "https://photoslibrary.googleapis.com/"
var throttler = rate.NewLimiter(150, 10)
var photosConfig *oauth2.Config

func init() {
	photosConfig = &oauth2.Config{
		ClientID:     constants.OauthClientId,
		ClientSecret: constants.OauthClientSecret,
		Endpoint:     google.Endpoint,
		Scopes: []string{
			"https://www.googleapis.com/auth/photoslibrary.readonly",
			"https://www.googleapis.com/auth/photoslibrary.sharing"},
	}
}

func getPhotosService(refreshToken string) (*http.Client, error) {
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh token is empty")
	}
	tokenSrc := oauth2.Token{
		RefreshToken: refreshToken,
	}
	client := photosConfig.Client(context.Background(), &tokenSrc)
	if client == nil {
		return nil, fmt.Errorf("failed to create photos client")
	}
	client.Timeout = 10 * time.Second
	return client, nil
}

func Photos(photosScan GPhotosScan) (int, error) {
	// Phase 1: Create scan record (synchronous)
	scanId, err := db.LogStartScan("photos")
	if err != nil {
		return 0, fmt.Errorf("failed to start photos scan (album=%s): %w", photosScan.AlbumId, err)
	}

	// Validate photos client
	_, err = getPhotosService(photosScan.RefreshToken)
	if err != nil {
		return 0, fmt.Errorf("failed to get photos service for scan %d: %w", scanId, err)
	}

	// Save metadata in background
	go func() {
		if err := db.SaveScanMetadata("", "", "", scanId); err != nil {
			slog.Error("Failed to save scan metadata",
				"scan_id", scanId,
				"album_id", photosScan.AlbumId,
				"error", err)
		}
	}()

	// Phase 2: Start collection in background (asynchronous)
	photosMediaItem := make(chan db.PhotosMediaItem, 10)
	go func() {
		defer close(photosMediaItem)

		err := startPhotosScan(scanId, photosScan, photosMediaItem)
		if err != nil {
			slog.Error("Photos scan collection failed",
				"scan_id", scanId,
				"album_id", photosScan.AlbumId,
				"error", err)
			db.MarkScanFailed(scanId, err.Error())
			return
		}
	}()

	// Start processing photo data in background
	go db.SavePhotosMediaItemToDb(scanId, photosMediaItem)

	return scanId, nil
}

func startPhotosScan(scanId int, photosScan GPhotosScan, photosMediaItem chan<- db.PhotosMediaItem) error {
	lock.Lock()
	defer lock.Unlock()
	resetCounters()
	ticker := time.NewTicker(5 * time.Second)
	done := make(chan bool)
	notificationChannel := notification.GetPublisher(photosScan.AlbumId)
	go logProgress(scanId, photosScan.AlbumId, done, ticker, notificationChannel)
	var wg sync.WaitGroup
	var err error
	if photosScan.AlbumId != "" {
		err = listMediaItemsForAlbum(photosScan, photosMediaItem, &wg)
	} else {
		err = listMediaItems(photosScan, photosMediaItem, &wg)
	}
	wg.Wait()
	done <- true
	ticker.Stop()
	if err != nil {
		return fmt.Errorf("failed to list media items: %w", err)
	}
	return nil
}

func processMediaItem(photosScan GPhotosScan, mediaItem MediaItem, photosMediaItem chan<- db.PhotosMediaItem, wg *sync.WaitGroup) {
	defer wg.Done()
	var size int64 = -1
	var md5Hash string
	if photosScan.FetchMd5Hash {
		size, md5Hash = getContentSizeAndHash(mediaItem.BaseUrl, mediaItem.MimeType)
	} else if photosScan.FetchSize {
		size = getContentSize(mediaItem.BaseUrl, mediaItem.MimeType)
	}
	var cameraMake string
	var cameraModel string
	var fNumber float32
	var exposureTime string
	var focalLength float32
	var iso int
	var fps float32
	if mediaItem.MimeType[:5] == "image" {
		cameraMake = mediaItem.MediaMetadata.Photo.CameraMake
		cameraModel = mediaItem.MediaMetadata.Photo.CameraModel
		fNumber = mediaItem.MediaMetadata.Photo.ApertureFNumber
		exposureTime = mediaItem.MediaMetadata.Photo.ExposureTime
		focalLength = mediaItem.MediaMetadata.Photo.FocalLength
		iso = mediaItem.MediaMetadata.Photo.IsoEquivalent
	} else {
		cameraMake = mediaItem.MediaMetadata.Video.CameraMake
		cameraModel = mediaItem.MediaMetadata.Video.CameraModel
		fps = mediaItem.MediaMetadata.Video.Fps
	}
	pmi := db.PhotosMediaItem{
		MediaItemId:            mediaItem.Id,
		ProductUrl:             mediaItem.ProductUrl,
		MimeType:               mediaItem.MimeType,
		Filename:               mediaItem.Filename,
		Size:                   size,
		ContributorDisplayName: mediaItem.ContributorInfo.DisplayName,
		CameraMake:             cameraMake,
		CameraModel:            cameraModel,
		FocalLength:            focalLength,
		FNumber:                fNumber,
		Iso:                    iso,
		ExposureTime:           exposureTime,
		Fps:                    fps,
		Md5hash:                md5Hash,
	}
	layout := "2006-01-02T15:04:05Z"
	str := mediaItem.MediaMetadata.CreationTime
	t, err := time.Parse(layout, str)

	if err == nil {
		pmi.FileModTime = t
	} else {
		slog.Error(fmt.Sprintf("err parsing time. err=%v", err))
	}

	photosMediaItem <- pmi
	counter_processed.Add(1)
	counter_pending.Add(-1)
}

func ListAlbums(refreshToken string) []Album {
	albums := make([]Album, 0)
	url := photosApiBaseUrl + "v1/albums"
	nextPageToken := ""
	hasNextPage := true
	client, err := getPhotosService(refreshToken)
	if err != nil {
		slog.Error("Failed to get photos service for ListAlbums", "error", err)
		return albums
	}
	for hasNextPage {
		err := throttler.Wait(context.Background())
		if err != nil {
			slog.Error("Throttler wait error in ListAlbums", "error", err)
			return albums
		}
		nextPageUrl := url + "?pageToken=" + nextPageToken
		req, err := http.NewRequest("GET", nextPageUrl, nil)
		if err != nil {
			slog.Error("Failed to create album list request", "error", err)
			return albums
		}
		resp, err := client.Do(req)
		if err != nil {
			slog.Error("Failed to fetch albums", "error", err)
			return albums
		}
		if resp.StatusCode != 200 {
			slog.Warn(fmt.Sprintf("Unexpected response status code %v", resp.StatusCode))
			rb, _ := io.ReadAll(resp.Body)
			slog.Warn(fmt.Sprintf("Response %v", string(rb)))
			return albums
		}
		albumResponse := new(ListAlbumsResponse)
		err = getJson(resp, albumResponse)
		if err != nil {
			slog.Error("Failed to decode album response JSON", "error", err)
			return albums
		}
		nextPageToken = albumResponse.NextPageToken
		albums = append(albums, albumResponse.Albums...)
		if len(nextPageToken) == 0 {
			hasNextPage = false
		}
	}
	return albums
}

func listMediaItemsForAlbum(photosScan GPhotosScan, photosMediaItem chan<- db.PhotosMediaItem, wg *sync.WaitGroup) error {
	var retries int = 25
	url := photosApiBaseUrl + "v1/mediaItems:search"
	nextPageToken := ""
	hasNextPage := true
	client, err := getPhotosService(photosScan.RefreshToken)
	if err != nil {
		return fmt.Errorf("failed to get photos service: %w", err)
	}
	for hasNextPage {
		err := throttler.Wait(context.Background())
		if err != nil {
			return fmt.Errorf("throttler wait error: %w", err)
		}
		nextPageUrl := url + "?pageToken=" + nextPageToken
		request := &SearchMediaItemRequest{AlbumId: photosScan.AlbumId}
		reqJson, err := json.Marshal(request)
		if err != nil {
			return fmt.Errorf("failed to marshal search request: %w", err)
		}
		reqBody := strings.NewReader(string(reqJson))
		req, err := http.NewRequest("POST", nextPageUrl, reqBody)
		if err != nil {
			return fmt.Errorf("failed to create search request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to execute search request: %w", err)
		}
		if resp.StatusCode != 200 {
			slog.Warn(fmt.Sprintf("Unexpected response status code %v", resp.StatusCode))
			rb, _ := io.ReadAll(resp.Body)
			slog.Warn(fmt.Sprintf("Response %v", string(rb)))
			if retries == 0 {
				return fmt.Errorf("exceeded retry limit for album media items")
			}
			retries -= 1
			continue
		}
		listMediaItemResponse := new(ListMediaItemResponse)
		err = getJson(resp, listMediaItemResponse)
		if err != nil {
			return fmt.Errorf("failed to decode media items response: %w", err)
		}
		nextPageToken = listMediaItemResponse.NextPageToken
		wg.Add(len(listMediaItemResponse.MediaItems))
		counter_pending.Add(int64(len(listMediaItemResponse.MediaItems)))
		for _, mediaItem := range listMediaItemResponse.MediaItems {
			err := throttler.Wait(context.Background())
			if err != nil {
				slog.Warn("Throttler wait error while processing media item, skipping",
					"error", err,
					"media_item_id", mediaItem.Id)
				wg.Done()
				counter_pending.Add(-1)
				continue
			}
			processMediaItem(photosScan, mediaItem, photosMediaItem, wg)
		}
		if len(nextPageToken) == 0 {
			hasNextPage = false
		}
	}
	return nil
}

func listMediaItems(photosScan GPhotosScan, photosMediaItem chan<- db.PhotosMediaItem, wg *sync.WaitGroup) error {
	var retries int = 25
	url := photosApiBaseUrl + "v1/mediaItems"
	nextPageToken := ""
	hasNextPage := true
	client, err := getPhotosService(photosScan.RefreshToken)
	if err != nil {
		return fmt.Errorf("failed to get photos service: %w", err)
	}
	for hasNextPage {
		err := throttler.Wait(context.Background())
		if err != nil {
			return fmt.Errorf("throttler wait error: %w", err)
		}
		nextPageUrl := url + "?pageToken=" + nextPageToken
		req, err := http.NewRequest("GET", nextPageUrl, nil)
		if err != nil {
			return fmt.Errorf("failed to create media items request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to execute media items request: %w", err)
		}
		if resp.StatusCode != 200 {
			slog.Warn(fmt.Sprintf("Unexpected response status code %v", resp.StatusCode))
			rb, _ := io.ReadAll(resp.Body)
			slog.Warn(fmt.Sprintf("Response %v", string(rb)))
			if retries == 0 {
				return fmt.Errorf("exceeded retry limit for media items")
			}
			retries -= 1
			continue
		}
		listMediaItemResponse := new(ListMediaItemResponse)
		err = getJson(resp, listMediaItemResponse)
		if err != nil {
			return fmt.Errorf("failed to decode media items response: %w", err)
		}
		nextPageToken = listMediaItemResponse.NextPageToken
		wg.Add(len(listMediaItemResponse.MediaItems))
		counter_pending.Add(int64(len(listMediaItemResponse.MediaItems)))
		for _, mediaItem := range listMediaItemResponse.MediaItems {
			err := throttler.Wait(context.Background())
			if err != nil {
				slog.Warn("Throttler wait error while processing media item, skipping",
					"error", err,
					"media_item_id", mediaItem.Id)
				wg.Done()
				counter_pending.Add(-1)
				continue
			}
			processMediaItem(photosScan, mediaItem, photosMediaItem, wg)
		}
		if len(nextPageToken) == 0 {
			hasNextPage = false
		}
	}
	return nil
}

func getContentSizeAndHash(url string, mimeType string) (int64, string) {
	var retries int = 5
	var resp *http.Response
	var err error
	switch mimeType[:5] {
	case "image":
		//e.g. image/jpeg image/png image/gif
		url = url + "=d"
	case "video":
		//e.g. video/mp4
		url = url + "=dv"
	default:
		slog.Warn(fmt.Sprintf("Unhandled mime type: %v", mimeType))
	}
	for retries > 0 {
		resp, err = http.Get(url)
		if err != nil {
			slog.Warn(fmt.Sprintf("Got error:%v. Will retry %v times", err, retries))
			retries -= 1
			continue
		}
		if resp.StatusCode != 200 {
			slog.Warn(fmt.Sprintf("Unexpected response status code %v", resp.StatusCode))
			rb, _ := io.ReadAll(resp.Body)
			slog.Warn(fmt.Sprintf("Response %v", string(rb)))
			slog.Warn(fmt.Sprintf("Will retry %v times", retries))
			retries -= 1
		}
		break
	}
	if resp == nil || resp.StatusCode != 200 {
		return 0, ""
	}
	defer resp.Body.Close()
	contentLength, err := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	if err != nil {
		slog.Warn("Failed to parse Content-Length header, skipping size/hash",
			"error", err,
			"url", url)
		return 0, ""
	}

	hash := md5.New()
	_, err = io.Copy(ioutil.Discard, io.TeeReader(resp.Body, hash))
	if err != nil {
		slog.Warn("Failed to calculate MD5 hash for photo, skipping hash",
			"error", err,
			"url", url)
		return contentLength, ""
	}
	return contentLength, hex.EncodeToString(hash.Sum(nil))
}

func getContentSize(url string, mimeType string) int64 {
	var retries int = 5
	var resp *http.Response
	var err error
	switch mimeType[:5] {
	case "image":
		//e.g. image/jpeg image/png image/gif
		url = url + "=d"
	case "video":
		//e.g. video/mp4
		url = url + "=dv"
	default:
		slog.Warn(fmt.Sprintf("Unhandled mime type: %v", mimeType))
	}
	for retries > 0 {
		resp, err = http.Head(url)
		if err != nil {
			slog.Warn(fmt.Sprintf("Got error:%v. Will retry %v times", err, retries))
			retries -= 1
			continue
		}
		if resp.StatusCode != 200 {
			slog.Warn(fmt.Sprintf("Unexpected response status code %v", resp.StatusCode))
			rb, _ := io.ReadAll(resp.Body)
			slog.Warn(fmt.Sprintf("Response %v", string(rb)))
			slog.Warn(fmt.Sprintf("Will retry %v times", retries))
			retries -= 1
		}
		break
	}
	if resp == nil || resp.StatusCode != 200 {
		return 0
	}
	defer resp.Body.Close()
	contentLength, err := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	if err != nil {
		slog.Warn("Failed to parse Content-Length header, skipping size",
			"error", err,
			"url", url)
		return 0
	}
	io.Copy(ioutil.Discard, resp.Body)
	return contentLength
}

func getJson(r *http.Response, target interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(target)
}

type Album struct {
	Id                    string
	Title                 string
	ProductUrl            string
	MediaItemsCount       string
	CoverPhotoBaseUrl     string
	CoverPhotoMediaItemId string
}

type ListAlbumsResponse struct {
	Albums        []Album
	NextPageToken string
}

type MediaItem struct {
	Id              string
	Description     string
	ProductUrl      string
	BaseUrl         string
	MimeType        string
	Filename        string
	MediaMetadata   MediaMetadata
	ContributorInfo ContributorInfo
}

type MediaMetadata struct {
	CreationTime string
	Width        string
	Height       string

	// Union field metadata can be only one of the following:
	Photo Photo
	Video Video
}

type Photo struct {
	CameraMake      string
	CameraModel     string
	FocalLength     float32
	ApertureFNumber float32
	IsoEquivalent   int
	ExposureTime    string
}

type Video struct {
	CameraMake  string
	CameraModel string
	Fps         float32
}

type ContributorInfo struct {
	ProfilePictureBaseUrl string
	DisplayName           string
}

type ListMediaItemResponse struct {
	MediaItems    []MediaItem
	NextPageToken string
}

type SearchMediaItemRequest struct {
	AlbumId   string `json:"albumId"`
	PageSize  int    `json:"pageSize"`
	PageToken string `json:"pageToken"`
	OrderBy   string `json:"orderBy"`
}

type GPhotosScan struct {
	AlbumId      string
	FetchSize    bool
	FetchMd5Hash bool
	RefreshToken string
}
