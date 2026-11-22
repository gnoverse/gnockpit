package web

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gnoverse/gnockpit/node"
)

var seamNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func newSeamServer(t *testing.T) *Server {
	t.Helper()
	client := node.NewClient("http://localhost:1", time.Second)
	client.Names = node.NewNameRegistryWithPersist(filepath.Join(t.TempDir(), "names.json"))
	srv := NewServer(client, "127.0.0.1:0", time.Hour)
	srv.ChainStuckSecs = 30
	return srv
}

func seamSnapshot(height string, blockTime time.Time) *node.Snapshot {
	return &node.Snapshot{
		Status: &node.Status{
			NodeInfo: node.NodeInfo{Network: "test-x"},
			SyncInfo: node.SyncInfo{LatestBlockHeight: height, LatestBlockTime: blockTime.Format(time.RFC3339Nano)},
		},
		Timestamp: seamNow,
	}
}

func TestRoutesServeEveryPath(t *testing.T) {
	srv := newSeamServer(t)
	srv.NotifyTestToken = "seam-token" // the notify routes answer 404 while disabled
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)

	// The WebSocket path logs the failed upgrade; keep the test output clean.
	prev := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(prev) })

	paths := []string{
		"/", "/api", "/api/boot", "/manifest.json", "/icon.svg", "/icon-32.png",
		"/icon-192.png", "/icon-512.png", "/apple-touch-icon.png", "/service-worker.js",
		"/api/push/vapid-key", "/api/push/subscribe", "/api/push/entities", "/api/push/test",
		"/api/push/reminder", "/api/notify/targets", "/api/notify/test", "/api/links",
		"/api/status", "/api/stats", "/api/eta", "/api/history", "/api/validator", "/badge.svg",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			resp, err := http.Get(ts.URL + p)
			if err != nil {
				t.Fatalf("GET %s: %v", p, err)
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound {
				t.Errorf("GET %s = %d, want not 404", p, resp.StatusCode)
			}
		})
	}

	t.Run("/ws", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/ws")
		if err != nil {
			t.Fatalf("GET /ws: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET /ws = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
	})

	t.Run("/nope", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/nope")
		if err != nil {
			t.Fatalf("GET /nope: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET /nope = %d, want %d", resp.StatusCode, http.StatusNotFound)
		}
	})
}

func TestPublishBroadcastsUpdate(t *testing.T) {
	srv := newSeamServer(t)
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)

	srv.setSnapshot(seamSnapshot("41", seamNow))
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", wsURL, err)
	}
	t.Cleanup(func() { conn.Close() })

	// handleWS registers the client before sending the current snapshot, so
	// receiving it proves the client is registered for the broadcast.
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read initial snapshot: %v", err)
	}

	snap := seamSnapshot("42", seamNow)
	srv.publish(t.Context(), snap)

	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read update: %v", err)
	}
	var msg struct {
		Type string `json:"type"`
		Data struct {
			Status struct {
				SyncInfo struct {
					LatestBlockHeight string `json:"latest_block_height"`
				} `json:"sync_info"`
			} `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if msg.Type != "update" {
		t.Errorf("message type = %q, want update", msg.Type)
	}
	if got := msg.Data.Status.SyncInfo.LatestBlockHeight; got != "42" {
		t.Errorf("latest_block_height = %q, want 42", got)
	}
	if srv.getSnapshot() != snap {
		t.Error("getSnapshot() is not the published snapshot")
	}
}

func TestServerClockDrivesHandlers(t *testing.T) {
	statusOf := func(t *testing.T, srv *Server) string {
		t.Helper()
		ts := httptest.NewServer(srv.routes())
		t.Cleanup(ts.Close)
		resp, err := http.Get(ts.URL + "/api/status")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body.Status
	}

	t.Run("fixed clock reads the chain as up", func(t *testing.T) {
		srv := newSeamServer(t)
		srv.clock = func() time.Time { return seamNow }
		srv.setSnapshot(seamSnapshot("42", seamNow.Add(-10*time.Second)))
		if got := statusOf(t, srv); got != "operational" {
			t.Errorf("/api/status status = %q, want operational", got)
		}
	})

	t.Run("wall clock reads the same snapshot as stuck", func(t *testing.T) {
		srv := newSeamServer(t)
		srv.setSnapshot(seamSnapshot("42", seamNow.Add(-10*time.Second)))
		if got := statusOf(t, srv); got != "down" {
			t.Errorf("/api/status status = %q, want down", got)
		}
	})
}
