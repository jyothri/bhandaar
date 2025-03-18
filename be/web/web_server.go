package web

import (
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/constants"
	"github.com/rs/cors"
)

func Server() {
	slog.Info("Starting web server.")
	r := mux.NewRouter()
	api(r)
	oauth(r)
	sse(r)
	cors := cors.New(cors.Options{
		AllowedOrigins:   []string{constants.FrontendUrl},
		AllowCredentials: true,
	})
	handler := cors.Handler(r)
	srv := &http.Server{
		Handler: handler,
		Addr:    ":8090",
		// Good practice: enforce timeouts for servers you create!
		WriteTimeout: 10 * time.Second,
		ReadTimeout:  10 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
