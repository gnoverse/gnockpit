package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/ranking"
	_ "modernc.org/sqlite"
)

const (
	rankChain = "test-chain-a"
	rankKeyA  = "g1keya"
	rankKeyB  = "g1keyb"
)

// rankNow is noon UTC on the day the test chain is published for.
var rankNow = time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

// rankingHead is the test chain's last height: a block every 6 hours from 32
// days before rankNow's day, the last at 06:00 on rankNow's day.
const rankingHead = 130

// openRankingDB opens an empty database closed with the test.
func openRankingDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

// seedRankingStore stores the test chain, two operators signing every block
// of one validator set, in a block store over db.
func seedRankingStore(t *testing.T, db *sql.DB) *blocks.Store {
	t.Helper()
	ctx := context.Background()
	s, err := blocks.NewStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{rankKeyA, rankKeyB}
	members := make([]blocks.Member, len(keys))
	for i, k := range keys {
		members[i] = blocks.Member{Address: k, PubKey: "pub-" + k, Power: 1}
	}
	if err := s.PutValset(ctx, "SET", members); err != nil {
		t.Fatal(err)
	}
	first := rankNow.Truncate(24*time.Hour).AddDate(0, 0, -32)
	recs := make([]blocks.Record, rankingHead)
	for i := range recs {
		r := blocks.Record{Height: int64(i + 1), Time: first.Add(time.Duration(i) * 6 * time.Hour), Proposer: rankKeyA, ValsetHash: "SET"}
		for _, k := range keys {
			r.Delays = append(r.Delays, blocks.Delay{Address: k, Ms: 100})
		}
		recs[i] = r
	}
	if err := s.Write(ctx, recs); err != nil {
		t.Fatal(err)
	}
	return s
}

// rankingFill is a block fill whose progress is fixed.
type rankingFill blocks.Progress

func (f rankingFill) Progress() blocks.Progress { return blocks.Progress(f) }

// newRankingPublisher returns a publisher over the seeded test chain as of
// rankNow, its profiles read, excluding exclude, and not yet refreshed.
func newRankingPublisher(t *testing.T, exclude ...string) *ranking.Publisher {
	t.Helper()
	db := openRankingDB(t)
	s := seedRankingStore(t, db)
	low, high, err := s.Range(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	days, err := ranking.NewDayStore(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return &ranking.Publisher{
		Blocks:   s,
		Fill:     rankingFill{ChainID: rankChain, From: low, To: high, Complete: true},
		Days:     days,
		Profiles: func() (ranking.Profiles, bool) { return nil, true },
		Names:    func(key string) string { return "node of " + key },
		Exclude:  exclude,
		Now:      func() time.Time { return rankNow },
	}
}

// refreshedRankingServer returns a server serving newRankingPublisher's
// first day.
func refreshedRankingServer(t *testing.T, exclude ...string) *Server {
	t.Helper()
	pub := newRankingPublisher(t, exclude...)
	if err := pub.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	return &Server{Ranking: pub}
}

// rankingBody is /api/ranking's answer.
type rankingBody struct {
	State struct {
		Kind          string  `json:"kind"`
		StoredShare   float64 `json:"stored_share"`
		EventsPending bool    `json:"events_pending"`
	} `json:"state"`
	HideExcluded bool `json:"hide_excluded"`
	Ranking      *struct {
		Chain      string   `json:"chain"`
		Day        string   `json:"day"`
		Exclusions []string `json:"exclusions"`
		Formula    int      `json:"formula"`
		Entries    []struct {
			Operator string `json:"operator"`
			Status   string `json:"status"`
			Rank     int    `json:"rank"`
		} `json:"entries"`
	} `json:"ranking"`
	Error string `json:"error"`
}

func getRanking(t *testing.T, srv *Server, method string) (int, rankingBody, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleRanking(rec, httptest.NewRequest(method, "/api/ranking", nil))
	var body rankingBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return rec.Code, body, rec
}

func TestRankingAPIOffIs404(t *testing.T) {
	code, body, rec := getRanking(t, &Server{}, http.MethodGet)
	if code != http.StatusNotFound || body.Error != "ranking disabled" {
		t.Errorf("GET /api/ranking with the ranking off = %d %q, want 404 %q", code, body.Error, "ranking disabled")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS header = %q, want *", got)
	}
}

func TestRankingAPIRefusesPost(t *testing.T) {
	for name, srv := range map[string]*Server{"on": {Ranking: newRankingPublisher(t)}, "off": {}} {
		t.Run(name, func(t *testing.T) {
			code, body, rec := getRanking(t, srv, http.MethodPost)
			if code != http.StatusMethodNotAllowed || body.Error != "method not allowed" {
				t.Errorf("POST /api/ranking with the ranking %s = %d %q, want 405 %q", name, code, body.Error, "method not allowed")
			}
			if got := rec.Header().Get("Allow"); got != http.MethodGet {
				t.Errorf("POST /api/ranking Allow = %q, want %q", got, http.MethodGet)
			}
		})
	}
}

func TestRankingAPIServesThePublishedDay(t *testing.T) {
	code, body, rec := getRanking(t, refreshedRankingServer(t), http.MethodGet)
	if code != http.StatusOK {
		t.Fatalf("GET /api/ranking = %d, want 200: %s", code, rec.Body)
	}
	if body.State.Kind != "ready" || body.HideExcluded {
		t.Errorf("state %q, hide_excluded %v; want ready, false", body.State.Kind, body.HideExcluded)
	}
	r := body.Ranking
	if r == nil || r.Chain != rankChain || r.Day != "2026-11-25" || r.Formula != 1 || len(r.Entries) != 2 {
		t.Fatalf("ranking = %+v, want chain %s, day 2026-11-25, formula 1 and 2 entries", r, rankChain)
	}
	for _, e := range r.Entries {
		if e.Status != "ranked" || e.Rank != 1 {
			t.Errorf("entry %s = status %s, rank %d; want ranked, 1", e.Operator, e.Status, e.Rank)
		}
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS header = %q, want *", got)
	}
}

func TestRankingAPIFilling(t *testing.T) {
	pub := newRankingPublisher(t)
	// A fill that has stored heights 999 and 1000 only: short of the span's start.
	pub.Fill = rankingFill{ChainID: rankChain, From: 999, To: 1000}
	if err := pub.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	code, body, rec := getRanking(t, &Server{Ranking: pub}, http.MethodGet)
	if code != http.StatusOK || body.State.Kind != "filling" || body.Ranking != nil {
		t.Errorf("GET /api/ranking while filling = %d, state %q, ranking %v; want 200, filling, null: %s", code, body.State.Kind, body.Ranking, rec.Body)
	}
}

func TestRankingAPIFillingBeforeTheFirstPass(t *testing.T) {
	code, body, _ := getRanking(t, &Server{Ranking: newRankingPublisher(t)}, http.MethodGet)
	if code != http.StatusOK || body.State.Kind != "filling" || body.Ranking != nil {
		t.Errorf("GET /api/ranking before a pass = %d, state %q, ranking %v; want 200, filling, null", code, body.State.Kind, body.Ranking)
	}
}

// TestRankingAPIReadyWithoutADay checks the answer while the state is ready
// and no day is published, here for lack of the profiles: 200, the state,
// and a null ranking.
func TestRankingAPIReadyWithoutADay(t *testing.T) {
	pub := newRankingPublisher(t)
	pub.Profiles = func() (ranking.Profiles, bool) { return nil, false }
	if err := pub.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	code, body, rec := getRanking(t, &Server{Ranking: pub}, http.MethodGet)
	if code != http.StatusOK || body.State.Kind != "ready" || body.Ranking != nil {
		t.Errorf("GET /api/ranking ready with no day = %d, state %q, ranking %v; want 200, ready, null: %s", code, body.State.Kind, body.Ranking, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"ranking": null`) {
		t.Errorf("answer = %s, want an explicit \"ranking\": null", rec.Body)
	}
}

func TestRankingWaitsForEveryValoperProfile(t *testing.T) {
	// The publisher reads the server's profiles, as main wires it; g1opb's
	// profile fails on the first read.
	n := &valopersNode{pages: [][]string{{"g1opa", "g1opb"}}, failProfile: map[string]bool{"g1opb": true}}
	srv := valopersServer(t, n)
	pub := newRankingPublisher(t)
	pub.Profiles = func() (ranking.Profiles, bool) {
		m, ok := srv.Valopers()
		return ranking.Profiles(m), ok
	}
	logsOf(func() { srv.refreshValopers(context.Background()) })
	if err := pub.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := pub.Snapshot().Published; got != nil {
		t.Errorf("published %s's ranking with g1opb's profile unread, want none", got.Day)
	}
	n.set(func() { n.failProfile = nil })
	srv.refreshValopers(context.Background())
	if err := pub.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := pub.Snapshot().Published; got == nil || got.Day != "2026-11-25" {
		t.Errorf("published = %+v after every profile read, want 2026-11-25's ranking", got)
	}
}

func TestRankingAPIListsAnExclusionNotInTheSet(t *testing.T) {
	const outsider = "g1outsider"
	_, base, _ := getRanking(t, refreshedRankingServer(t), http.MethodGet)
	_, body, _ := getRanking(t, refreshedRankingServer(t, outsider), http.MethodGet)
	if body.Ranking == nil || len(body.Ranking.Exclusions) != 1 || body.Ranking.Exclusions[0] != outsider {
		t.Fatalf("ranking = %+v, want exclusions [%s]", body.Ranking, outsider)
	}
	if len(body.Ranking.Entries) != len(base.Ranking.Entries) {
		t.Fatalf("%d entries, want the %d of the ranking without the exclusion", len(body.Ranking.Entries), len(base.Ranking.Entries))
	}
	for i, e := range body.Ranking.Entries {
		if b := base.Ranking.Entries[i]; e.Operator != b.Operator || e.Rank != b.Rank || e.Status != b.Status {
			t.Errorf("entry %d = %s rank %d %s, want %s rank %d %s", i, e.Operator, e.Rank, e.Status, b.Operator, b.Rank, b.Status)
		}
	}
}

func TestRankingAPIHideExcludedKeepsTheList(t *testing.T) {
	srv := refreshedRankingServer(t, rankKeyB)
	srv.RankingHideExcluded = true
	_, body, _ := getRanking(t, srv, http.MethodGet)
	if !body.HideExcluded {
		t.Errorf("hide_excluded = false, want true")
	}
	if body.Ranking == nil || len(body.Ranking.Exclusions) != 1 || body.Ranking.Exclusions[0] != rankKeyB {
		t.Errorf("ranking = %+v, want exclusions [%s] still listed", body.Ranking, rankKeyB)
	}
}

func TestBootSaysWhetherRankingIsOn(t *testing.T) {
	for _, tc := range []struct {
		name string
		srv  *Server
		want bool
	}{
		{"off", &Server{}, false},
		{"on", &Server{Ranking: newRankingPublisher(t)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.srv.handleBootStatus(rec, httptest.NewRequest(http.MethodGet, "/api/boot", nil))
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode %s: %v", rec.Body, err)
			}
			if got, ok := body["ranking"]; !ok || got != tc.want {
				t.Errorf("/api/boot ranking = %v (present %v), want %v", got, ok, tc.want)
			}
		})
	}
}

func TestMonikerExported(t *testing.T) {
	client := node.NewClient("http://localhost:1", time.Second)
	client.Names = node.NewNameRegistryWithPersist(filepath.Join(t.TempDir(), "names.json"))
	client.Names.SeedFromValopers(map[string]string{"g1signa": "Alice"})
	srv := &Server{Client: client}
	if got := srv.Moniker("g1signa"); got != "Alice" {
		t.Errorf("Moniker(g1signa) = %q, want Alice", got)
	}
	if got := srv.Moniker("g1unknown"); got != "" {
		t.Errorf("Moniker(g1unknown) = %q, want \"\"", got)
	}
}

// TestValopersReadsOnceARefreshSucceeded checks that Valopers is false until
// the first valoper refresh succeeds, true with its profiles after, and
// false after a refresh that fails, its profiles kept.
func TestValopersReadsOnceARefreshSucceeded(t *testing.T) {
	answer := func(w http.ResponseWriter, r *http.Request) {
		raw, _ := base64.StdEncoding.DecodeString(strings.Trim(r.URL.Query().Get("data"), `"`))
		arg := string(raw)
		if strings.Contains(r.URL.Query().Get("path"), "qeval") {
			ans := "(true bool)"
			if strings.HasSuffix(arg, ".LastRotationHeight") {
				ans = "(5 int64)"
			}
			fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"response":{"ResponseBase":{"Error":null,"Data":%q}}}}`, base64.StdEncoding.EncodeToString([]byte(ans)))
			return
		}
		md := "no valopers"
		switch {
		case strings.HasSuffix(arg, "valopers:"):
			md = " * [Alice](/r/gnops/valopers:g1opa) - [profile](/r/demo/profile:u/g1opa)\n"
		case strings.HasSuffix(arg, ":g1opa"):
			md = "## Alice\nAlice's node.\n\n- Operator Address: g1opa\n- Signing Address: g1signa\n"
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"response":{"ResponseBase":{"Error":null,"Data":%q}}}}`, base64.StdEncoding.EncodeToString([]byte(md)))
	}
	healthy := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		answer(w, r)
	}))
	defer ts.Close()
	srv := &Server{Client: node.NewClient(ts.URL, 5*time.Second)}
	srv.Client.Names = node.NewNameRegistryWithPersist(filepath.Join(t.TempDir(), "names.json"))

	if got, ok := srv.Valopers(); ok || len(got) != 0 {
		t.Errorf("Valopers() before any refresh = %v, %v; want none, false", got, ok)
	}
	// A failed refresh leaves them unread. Its log line is not under test.
	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	srv.refreshValopers(context.Background())
	log.SetOutput(prev)
	if got, ok := srv.Valopers(); ok || len(got) != 0 || !strings.Contains(logs.String(), "valoper refresh failed") {
		t.Errorf("Valopers() after a failed refresh = %v, %v, log %q; want none, false, the failure logged", got, ok, logs.String())
	}
	healthy = true
	srv.refreshValopers(context.Background())
	got, ok := srv.Valopers()
	if !ok || got["g1opa"].Moniker != "Alice" {
		t.Errorf("Valopers() after a refresh = %v, %v; want Alice's profile, true", got, ok)
	}
	healthy = false
	logsOf(func() { srv.refreshValopers(context.Background()) })
	if got, ok := srv.Valopers(); ok || got["g1opa"].Moniker != "Alice" {
		t.Errorf("Valopers() after a failed refresh = %v, %v; want Alice's last profile, false: the list is unknown", got, ok)
	}
}
