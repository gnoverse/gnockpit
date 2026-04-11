package hub

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Run starts the hub HTTP server with all endpoints.
// indexHTML is the embedded dashboard HTML (cluster-aware).
func (h *Hub) Run(ctx context.Context, indexHTML embed.FS) error {
	mux := http.NewServeMux()

	// Dashboard
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := indexHTML.ReadFile("index.html")
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	// API
	mux.HandleFunc("/api/mode", h.HandleAPIMode)
	mux.HandleFunc("/api/probes", h.HandleAPIProbes)
	mux.HandleFunc("/api/probes/", h.HandleAPIProbe)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		report := h.ComputeHealth()
		fmt.Fprintf(w, "%s", mustJSON(report))
	})

	// WebSocket endpoints
	mux.HandleFunc("/ws/probe", h.HandleProbeWS)
	mux.HandleFunc("/ws", h.HandleBrowserWS)
	mux.HandleFunc("/ws/doctor", h.HandleDoctorWS)

	srv := &http.Server{
		Addr:    h.Addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Printf("gnockpit hub: http://%s", h.Addr)
	return srv.ListenAndServe()
}

func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}
