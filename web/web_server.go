package web

import (
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
)

func StartWebServer() {
	r := mux.NewRouter()
	api(r)
	oauth(r)
	spa(r)
	srv := &http.Server{
		Handler: r,
		Addr:    ":8090",
		// Good practice: enforce timeouts for servers you create!
		WriteTimeout: 10 * time.Second,
		ReadTimeout:  10 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
