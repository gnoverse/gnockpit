package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/node"
)

func snapAt(blockTime string, catchingUp bool) *node.Snapshot {
	return &node.Snapshot{
		Status: &node.Status{
			NodeInfo: node.NodeInfo{Network: "test-13"},
			SyncInfo: node.SyncInfo{
				LatestBlockHeight: "100",
				LatestBlockTime:   blockTime,
				CatchingUp:        catchingUp,
			},
		},
	}
}

func TestDeriveStatus(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-3 * time.Second).Format(time.RFC3339Nano)
	old := now.Add(-5 * time.Minute).Format(time.RFC3339Nano)

	if got := deriveStatus(nil, 30, now); got.Status != "down" {
		t.Errorf("nil snapshot = %q, want down", got.Status)
	}
	if got := deriveStatus(&node.Snapshot{Error: "boom"}, 30, now); got.Status != "down" {
		t.Errorf("error snapshot = %q, want down", got.Status)
	}
	if got := deriveStatus(snapAt(old, false), 30, now); got.Status != "down" {
		t.Errorf("stuck chain = %q, want down", got.Status)
	}
	if got := deriveStatus(snapAt(recent, true), 30, now); got.Status != "degraded" {
		t.Errorf("catching up = %q, want degraded", got.Status)
	}
	// Reachable node but garbage block time → degraded (don't claim operational).
	if got := deriveStatus(snapAt("not-a-time", false), 30, now); got.Status != "degraded" {
		t.Errorf("unparseable block time = %q, want degraded", got.Status)
	}
	got := deriveStatus(snapAt(recent, false), 30, now)
	if got.Status != "operational" {
		t.Errorf("healthy = %q, want operational", got.Status)
	}
	if got.Chain != "test-13" || got.Height != "100" {
		t.Errorf("healthy meta = %+v", got)
	}
}

func TestStatusBadgeSVG(t *testing.T) {
	svg := statusBadgeSVG("test-13", "operational")
	if !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, "</svg>") {
		t.Error("not an svg document")
	}
	if !strings.Contains(svg, "#3fb950") { // green
		t.Error("operational badge should be green")
	}
	if !strings.Contains(svg, "test-13") || !strings.Contains(svg, "operational") {
		t.Error("badge missing label/status text")
	}
	if !strings.Contains(statusBadgeSVG("x", "down"), "#f85149") { // red
		t.Error("down badge should be red")
	}
}

func TestHandleStatusAndBadge(t *testing.T) {
	srv := &Server{ChainStuckSecs: 30}
	srv.setSnapshot(snapAt(time.Now().Format(time.RFC3339Nano), false))

	w := httptest.NewRecorder()
	srv.handleStatus(w, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	if ct := w.Header().Get("Access-Control-Allow-Origin"); ct != "*" {
		t.Errorf("status endpoint should be CORS-open, got %q", ct)
	}
	if !strings.Contains(w.Body.String(), `"operational"`) {
		t.Errorf("status body = %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	srv.handleBadge(w, httptest.NewRequest(http.MethodGet, "/badge.svg", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("badge: got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "image/svg") {
		t.Errorf("badge content-type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), "#3fb950") {
		t.Error("badge should be green for operational")
	}
}
