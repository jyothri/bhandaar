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

	// Apply global default size limit to all routes (512 KB)
	r.Use(RequestSizeLimitMiddleware(DefaultMaxBodySize))

	api(r)
	oauth(r)
	sse(r)
	cors := cors.New(cors.Options{
		AllowedOrigins:   []string{constants.FrontendUrl},
		AllowCredentials: true,
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowedMethods:   []string{"GET", "POST", "DELETE", "OPTIONS"},
		MaxAge:           300, // Cache preflight for 5 minutes
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
