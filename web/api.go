package web

import (
	"encoding/json"
	"log"
	"net/http"
)

// errorBody is a JSON API payload for a request it cannot answer.
type errorBody struct {
	Error string `json:"error"`
}

// serveJSON writes payload as indented JSON with status, CORS-open and
// uncached; what names the endpoint in the log of an encoding failure.
func serveJSON(w http.ResponseWriter, what string, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache")
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		log.Printf("%s: encode: %v", what, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(status)
	w.Write(body)
}
