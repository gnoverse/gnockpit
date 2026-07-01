package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseLink(t *testing.T) {
	good, err := ParseLink("  Explorer | https://explorer.gno.land ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if good.Title != "Explorer" || good.URL != "https://explorer.gno.land" {
		t.Errorf("parsed = %+v", good)
	}
	for _, bad := range []string{"NoPipe", "Title|", "|https://x", "Title|ftp://x", "  |  "} {
		if _, err := ParseLink(bad); err == nil {
			t.Errorf("ParseLink(%q) should have errored", bad)
		}
	}
}

func TestStatusLinkIndexJSONURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://status.gno.land":  "https://status.gno.land/index.json",
		"https://status.gno.land/": "https://status.gno.land/index.json",
	} {
		sl := NewStatusLink(Link{Title: "S", URL: in})
		if got := sl.indexJSONURL(); got != want {
			t.Errorf("indexJSONURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStatusLinkPoll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.json" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"data":{"attributes":{"aggregate_state":"degraded"}}}`))
	}))
	defer srv.Close()

	sl := NewStatusLink(Link{Title: "S", URL: srv.URL})
	if sl.State() != "unknown" {
		t.Errorf("initial state = %q, want unknown", sl.State())
	}
	sl.poll(context.Background(), http.DefaultClient)
	if sl.State() != "degraded" {
		t.Errorf("after poll state = %q, want degraded", sl.State())
	}

	// Unreachable / bad responses fall back to "unknown".
	bad := NewStatusLink(Link{Title: "B", URL: "http://127.0.0.1:0"})
	bad.poll(context.Background(), http.DefaultClient)
	if bad.State() != "unknown" {
		t.Errorf("bad poll state = %q, want unknown", bad.State())
	}
}

func TestHandleLinks(t *testing.T) {
	srv := &Server{
		Links:       []Link{{Title: "Docs", URL: "https://docs.gno.land"}},
		StatusLinks: []*StatusLink{NewStatusLink(Link{Title: "Status", URL: "https://status.gno.land"})},
	}
	srv.StatusLinks[0].setState("operational")

	w := httptest.NewRecorder()
	srv.handleLinks(w, httptest.NewRequest(http.MethodGet, "/api/links", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"Docs", "docs.gno.land", "Status", "status.gno.land", "operational"} {
		if !strings.Contains(body, want) {
			t.Errorf("response missing %q: %s", want, body)
		}
	}
}
