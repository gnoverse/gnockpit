//go:build page

package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	// etaShortHistory measures a pace but holds too few past estimates for
	// a margin.
	etaShortHistory int64 = 5_000
	// etaPacelessHistory is too short to measure a pace.
	etaPacelessHistory int64 = 2_000

	// etaReadBound is how long a read of /api/eta waits for its answer.
	etaReadBound = 10 * time.Second
	// etaDebounce is how long the planner waits after a keystroke.
	etaDebounce = 300 * time.Millisecond
)

var (
	// etaStalledChain has produced nothing for 10 minutes.
	etaStalledChain = timedChain{tip: pageNow.Add(-10 * time.Minute)}
)

// etaLinkQuery is the /api/eta query of a link card for height and since.
func etaLinkQuery(height int64, since time.Time) string {
	q := url.Values{"height": {strconv.FormatInt(height, 10)}}
	if !since.IsZero() {
		q.Set("since", since.UTC().Format(time.RFC3339))
	}
	return q.Encode()
}

// etaAnswer is the server's answer to /api/eta?query, the one the page
// shows: an answer, never an error.
func (e *pageEnv) etaAnswer(query string) etaReply {
	e.T.Helper()
	resp, err := http.Get(e.ts.URL + "/api/eta?" + query)
	if err != nil {
		e.T.Fatalf("GET /api/eta?%s: %v", query, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		e.T.Fatalf("GET /api/eta?%s: read: %v", query, err)
	}
	if resp.StatusCode != http.StatusOK {
		e.T.Fatalf("GET /api/eta?%s = %d %s, want 200", query, resp.StatusCode, body)
	}
	var rep etaReply
	if err := json.Unmarshal(body, &rep); err != nil {
		e.T.Fatalf("GET /api/eta?%s: decode %q: %v", query, body, err)
	}
	return rep
}
