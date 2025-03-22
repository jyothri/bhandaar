package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/constants"
	"github.com/jyothri/hdd/notification"
)

func sse(r *mux.Router) {
	sse := r.PathPrefix("/sse").Subrouter()
	sse.HandleFunc("/events", sseHandler)
	sse.HandleFunc("/scanprogress", scanProgressHandler)

}

func scanProgressHandler(w http.ResponseWriter, r *http.Request) {
	setHeaders(w)
	subscriber := notification.GetSubscriber(notification.NOTIFICATION_ALL)
	rc := http.NewResponseController(w)
	clientGone := r.Context().Done()
	slog.Info("[scan events] Client Connected.")
	start := time.Now()
	for {
		select {
		case <-clientGone:
			slog.Info(fmt.Sprintf("[scan events] Client disconnected.Connection Duration: %s", time.Since(start)))
			return
		case progress, more := <-subscriber:
			slog.Info(fmt.Sprintf("[scan events] Got progress notification: %v", progress))
			timestamp := strconv.FormatInt(time.Now().UTC().UnixMilli(), 10)
			if !more {
				if _, err := fmt.Fprintf(w, "event:close\nretry: 10000\nid:%s\ndata:close at %s \n\n", timestamp, time.Now().Format(time.RFC850)); err != nil {
					slog.Warn(fmt.Sprintf("[scan events] Unable to write. err: %s", err.Error()))
					return
				}
			}
			serializedBody, err := json.Marshal(progress)
			if err != nil {
				slog.Warn(fmt.Sprintf("[scan events] Unable to Serialize. err: %s", err.Error()))
				continue
			}
			if _, err := fmt.Fprintf(w, "event:progress\nretry: 10000\nid:%s\ndata:%v \n\n", timestamp, string(serializedBody)); err != nil {
				slog.Warn(fmt.Sprintf("[scan events] Unable to write. err: %s", err.Error()))
			}
			rc.SetWriteDeadline(time.Time{})
			rc.Flush()
		}
	}
}

func sseHandler(w http.ResponseWriter, r *http.Request) {
	setHeaders(w)
	lastEventId := r.Header.Get("Last-Event-Id")
	rc := http.NewResponseController(w)
	clientGone := r.Context().Done()
	ticker := time.NewTicker(4 * time.Second)
	timer := time.NewTimer(20 * time.Second)
	defer ticker.Stop()

	slog.Info(fmt.Sprintf("Client Connected. last eventId: %s", lastEventId))
	start := time.Now()
	for {
		select {
		case <-clientGone:
			slog.Info(fmt.Sprintf("Client disconnected. EventId of Client: %s Connection Duration: %s", lastEventId, time.Since(start)))
			return
		case <-ticker.C:
			timestamp := strconv.FormatInt(time.Now().UTC().UnixMilli(), 10)
			if _, err := fmt.Fprintf(w, "event:timer\nretry: 10000\nid:%s\ndata:%s \n\n", timestamp, time.Now().Format(time.RFC850)); err != nil {
				slog.Warn(fmt.Sprintf("Unable to write. EventId of Client: %s err: %s", lastEventId, err.Error()))
				return
			}
			slog.Info(fmt.Sprintf("Writing event to client. EventId of Client: %s", lastEventId))
			rc.SetWriteDeadline(time.Time{})
			rc.Flush()
		case <-timer.C:
			timestamp := strconv.FormatInt(time.Now().UTC().UnixMilli(), 10)
			if _, err := fmt.Fprintf(w, "event:close\nretry: 10000\nid:%s\ndata:close at %s \n\n", timestamp, time.Now().Format(time.RFC850)); err != nil {
				slog.Warn(fmt.Sprintf("Unable to write. EventId of Client: %s err: %s", lastEventId, err.Error()))
				return
			}
			slog.Info(fmt.Sprintf("Closing event to client. ClientEventId: %s", lastEventId))
			rc.SetWriteDeadline(time.Time{})
			rc.Flush()
			return
		}
	}
}

func setHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("access-control-allow-origin", constants.FrontendUrl)
}
