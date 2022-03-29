package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jyothri/hdd/constants"
	"github.com/jyothri/hdd/db"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/time/rate"
)

var client *http.Client
var photosApiBaseUrl = "https://photoslibrary.googleapis.com/"
var throttler = rate.NewLimiter(150, 10)

func init() {
	config := &oauth2.Config{
		ClientID:     constants.OauthClientId,
		ClientSecret: constants.OauthClientSecret,
		Endpoint:     google.Endpoint,
		Scopes: []string{
			"https://www.googleapis.com/auth/photoslibrary.readonly",
			"https://www.googleapis.com/auth/photoslibrary.sharing"},
	}
	tokenSrc := oauth2.Token{
		RefreshToken: constants.RefreshToken,
	}
	client = config.Client(context.Background(), &tokenSrc)
	client.Timeout = 10 * time.Second
}

func Photos(queryString string) int {
	photosMediaItem := make(chan db.PhotosMediaItem, 10)
	scanId := db.LogStartScan("photos")
	go db.SaveScanMetadata("", queryString, scanId)
	go startPhotosScan(scanId, queryString, photosMediaItem)
	go db.SavePhotosMediaItemToDb(scanId, photosMediaItem)
	return scanId
}

func startPhotosScan(scanId int, queryString string, photosMediaItem chan<- db.PhotosMediaItem) {
	lock.Lock()
	defer lock.Unlock()
	ticker := time.NewTicker(5 * time.Second)
	done := make(chan bool)
	go logProgressToConsole(done, ticker)
	var wg sync.WaitGroup
	if queryString != "" {
		listMediaItemsForAlbum(queryString, photosMediaItem, &wg)
	} else {
		listMediaItems(photosMediaItem, &wg)
	}
	wg.Wait()
	done <- true
	ticker.Stop()
	close(photosMediaItem)
}

func processMediaItem(mediaItem MediaItem, photosMediaItem chan<- db.PhotosMediaItem, wg *sync.WaitGroup) {
	defer wg.Done()
	size := getContentSize(mediaItem.BaseUrl, mediaItem.MimeType)
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
	}
	layout := "2006-01-02T15:04:05Z"
	str := mediaItem.MediaMetadata.CreationTime
	t, err := time.Parse(layout, str)

	if err == nil {
		pmi.FileModTime = t
	} else {
		fmt.Printf("err parsing time. err=%v\n", err)
	}

	photosMediaItem <- pmi
	counter_processed += 1
	counter_pending -= 1
}

func ListAlbums() []Album {
	albums := make([]Album, 0)
	url := photosApiBaseUrl + "v1/albums"
	nextPageToken := ""
	hasNextPage := true
	for hasNextPage {
		err := throttler.Wait(context.Background())
		checkError(err, fmt.Sprintf("Error with limiter: %s", err))
		nextPageUrl := url + "?pageToken=" + nextPageToken
		req, err := http.NewRequest("GET", nextPageUrl, nil)
		checkError(err)
		resp, err := client.Do(req)
		checkError(err)
		if resp.StatusCode != 200 {
			fmt.Printf("Unexpected response status code %v\n", resp.StatusCode)
			rb, _ := io.ReadAll(resp.Body)
			fmt.Printf("Response %v\n", string(rb))
			return albums
		}
		albumResponse := new(ListAlbumsResponse)
		err = getJson(resp, albumResponse)
		checkError(err)
		nextPageToken = albumResponse.NextPageToken
		albums = append(albums, albumResponse.Albums...)
		if len(nextPageToken) == 0 {
			hasNextPage = false
		}
	}
	return albums
}

func listMediaItemsForAlbum(albumId string, photosMediaItem chan<- db.PhotosMediaItem, wg *sync.WaitGroup) {
	var retries int = 25
	url := photosApiBaseUrl + "v1/mediaItems:search"
	nextPageToken := ""
	hasNextPage := true
	for hasNextPage {
		err := throttler.Wait(context.Background())
		checkError(err, fmt.Sprintf("Error with limiter: %s", err))
		nextPageUrl := url + "?pageToken=" + nextPageToken
		request := &SearchMediaItemRequest{AlbumId: albumId}
		reqJson, err := json.Marshal(request)
		checkError(err)
		reqBody := strings.NewReader(string(reqJson))
		req, err := http.NewRequest("POST", nextPageUrl, reqBody)
		checkError(err)
		resp, err := client.Do(req)
		checkError(err)
		if resp.StatusCode != 200 {
			fmt.Printf("Unexepcted response status code %v", resp.StatusCode)
			rb, _ := io.ReadAll(resp.Body)
			fmt.Printf("Response %v\n", string(rb))
			if retries == 0 {
				return
			}
			retries -= 1
			continue
		}
		listMediaItemResponse := new(ListMediaItemResponse)
		err = getJson(resp, listMediaItemResponse)
		checkError(err)
		nextPageToken = listMediaItemResponse.NextPageToken
		wg.Add(len(listMediaItemResponse.MediaItems))
		counter_pending += len(listMediaItemResponse.MediaItems)
		for _, mediaItem := range listMediaItemResponse.MediaItems {
			err := throttler.Wait(context.Background())
			checkError(err, fmt.Sprintf("Error with limiter: %s", err))
			processMediaItem(mediaItem, photosMediaItem, wg)
		}
		if len(nextPageToken) == 0 {
			hasNextPage = false
		}
	}
}

func listMediaItems(photosMediaItem chan<- db.PhotosMediaItem, wg *sync.WaitGroup) {
	var retries int = 25
	url := photosApiBaseUrl + "v1/mediaItems"
	nextPageToken := ""
	hasNextPage := true
	for hasNextPage {
		err := throttler.Wait(context.Background())
		checkError(err, fmt.Sprintf("Error with limiter: %s", err))
		nextPageUrl := url + "?pageToken=" + nextPageToken
		req, err := http.NewRequest("GET", nextPageUrl, nil)
		checkError(err)
		resp, err := client.Do(req)
		checkError(err)
		if resp.StatusCode != 200 {
			fmt.Printf("Unexepcted response status code %v", resp.StatusCode)
			rb, _ := io.ReadAll(resp.Body)
			fmt.Printf("Response %v\n", string(rb))
			if retries == 0 {
				return
			}
			retries -= 1
			continue
		}
		listMediaItemResponse := new(ListMediaItemResponse)
		err = getJson(resp, listMediaItemResponse)
		checkError(err)
		nextPageToken = listMediaItemResponse.NextPageToken
		wg.Add(len(listMediaItemResponse.MediaItems))
		counter_pending += len(listMediaItemResponse.MediaItems)
		for _, mediaItem := range listMediaItemResponse.MediaItems {
			err := throttler.Wait(context.Background())
			checkError(err, fmt.Sprintf("Error with limiter: %s", err))
			processMediaItem(mediaItem, photosMediaItem, wg)
		}
		if len(nextPageToken) == 0 {
			hasNextPage = false
		}
	}
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
		fmt.Printf("Unhandled mime type: %v\n", mimeType)
	}
	for retries > 0 {
		resp, err = http.Head(url)
		if err != nil {
			fmt.Printf("Got error:%v. Will retry %v times\n", err, retries)
			retries -= 1
			continue
		}
		if resp.StatusCode != 200 {
			fmt.Printf("Unexepcted response status code %v", resp.StatusCode)
			rb, _ := io.ReadAll(resp.Body)
			fmt.Printf("Response %v\n", string(rb))
			fmt.Printf("Will retry %v times\n", retries)
			retries -= 1
		}
		break
	}
	if resp == nil || resp.StatusCode != 200 {
		return 0
	}
	defer resp.Body.Close()
	contentLength, err := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	checkError(err)
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
