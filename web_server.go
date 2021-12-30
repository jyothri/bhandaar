package main

import (
	"net/http"
)

func StartWebServer() {
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/", fs)
	http.ListenAndServe(":8090", nil)
}
