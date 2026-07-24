package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// statusLinkRefreshInterval governs how often BetterStack status pages are
// polled for their aggregate state.
const statusLinkRefreshInterval = 45 * time.Second

// Link is a static header link button.
type Link struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// ParseLink parses a "Title|URL" flag value.
func ParseLink(spec string) (Link, error) {
	title, rawURL, ok := strings.Cut(spec, "|")
	title = strings.TrimSpace(title)
	rawURL = strings.TrimSpace(rawURL)
	if !ok || title == "" || rawURL == "" {
		return Link{}, fmt.Errorf("invalid link %q: want \"Title|URL\"", spec)
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return Link{}, fmt.Errorf("invalid link URL %q: must start with http:// or https://", rawURL)
	}
	return Link{Title: title, URL: rawURL}, nil
}

// StatusLink is a header link whose dot reflects a BetterStack status page's
// aggregate state, polled from "<URL>/index.json".
type StatusLink struct {
	Title string
	URL   string

	mu    sync.RWMutex
	state string
}

// NewStatusLink builds a StatusLink from a parsed Link.
func NewStatusLink(l Link) *StatusLink {
	return &StatusLink{Title: l.Title, URL: l.URL, state: "unknown"}
}

// State returns the last polled aggregate state.
func (sl *StatusLink) State() string {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	return sl.state
}

func (sl *StatusLink) setState(v string) {
	sl.mu.Lock()
	sl.state = v
	sl.mu.Unlock()
}

// indexJSONURL is the BetterStack public status JSON for the page.
func (sl *StatusLink) indexJSONURL() string {
	return strings.TrimRight(sl.URL, "/") + "/index.json"
}

// poll fetches the BetterStack aggregate_state; any failure records "unknown".
func (sl *StatusLink) poll(ctx context.Context, client *http.Client) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sl.indexJSONURL(), nil)
	if err != nil {
		sl.setState("unknown")
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		sl.setState("unknown")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		sl.setState("unknown")
		return
	}
	var body struct {
		Data struct {
			Attributes struct {
				AggregateState string `json:"aggregate_state"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		sl.setState("unknown")
		return
	}
	st := body.Data.Attributes.AggregateState
	if st == "" {
		st = "unknown"
	}
	sl.setState(st)
}

// statusLinkRefreshLoop polls each BetterStack status link on a slow timer.
func (s *Server) statusLinkRefreshLoop(ctx context.Context) {
	if len(s.StatusLinks) == 0 {
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	refresh := func() {
		for _, sl := range s.StatusLinks {
			sl.poll(ctx, client)
		}
	}
	refresh()
	ticker := time.NewTicker(statusLinkRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}

// handleLinks serves the header links + status links (with current state).
func (s *Server) handleLinks(w http.ResponseWriter, r *http.Request) {
	type statusLinkOut struct {
		Title string `json:"title"`
		URL   string `json:"url"`
		State string `json:"state"`
	}
	out := struct {
		Links       []Link          `json:"links"`
		StatusLinks []statusLinkOut `json:"statusLinks"`
	}{Links: s.Links}
	for _, sl := range s.StatusLinks {
		out.StatusLinks = append(out.StatusLinks, statusLinkOut{sl.Title, sl.URL, sl.State()})
	}
	if out.Links == nil {
		out.Links = []Link{}
	}
	if out.StatusLinks == nil {
		out.StatusLinks = []statusLinkOut{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
