package push

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/ranking"
)

// everyAlert is a config sending every routable alert to each of urls.
func everyAlert(urls ...string) NotifyConfig {
	var cfg NotifyConfig
	for _, u := range urls {
		cfg.Targets = append(cfg.Targets, NotifyRoute{URL: u, Alerts: map[AlertType]AlertOptions{
			AlertChainStuck: {}, AlertValidatorMissingBlocks: {}, AlertValsetChanged: {}, AlertHaltScheduled: {},
			AlertHaltApproaching: {Lead: Lead{Time: time.Hour}},
		}})
	}
	return cfg
}

// hooks records the paths a local webhook server is posted to.
type hooks struct {
	mu    sync.Mutex
	paths []string
}

func (h *hooks) got() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.paths)
}

// hookServer returns the host of a webhook server recording into hooks.
func hookServer(t *testing.T) (string, *hooks) {
	t.Helper()
	h := &hooks{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		h.mu.Lock()
		h.paths = append(h.paths, r.URL.Path)
		h.mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), h
}

// writeNotifyConfig writes body to path with modification time mod.
func writeNotifyConfig(t *testing.T, path, body string, mod time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func target(host, path, alerts string) string {
	return `{"url": "generic+http://` + host + path + `", "alerts": ` + alerts + `}`
}

func TestManagerSendsEachTargetOnlyItsAlerts(t *testing.T) {
	host, h := hookServer(t)
	path := filepath.Join(t.TempDir(), "notify.json")
	writeNotifyConfig(t, path, `{"targets": [`+target(host, "/a", `["chain_stuck"]`)+`, `+
		target(host, "/b", `[{"validator_missing_blocks": ["g1a"]}]`)+`]}`, time.Now())
	m := newTestManager(t, false)
	captureLog(t)
	if err := m.LoadNotifyConfig(path); err != nil {
		t.Fatal(err)
	}
	m.NotifyAlert(Alert{Type: AlertChainStuck, Firing: true, Title: "Chain stuck"})
	m.NotifyAlert(Alert{Type: AlertValidatorMissingBlocks, EntityID: "g1b", Firing: true})
	m.NotifyAlert(Alert{Type: AlertValidatorMissingBlocks, EntityID: "g1a", Firing: false})
	if got := h.got(); !slices.Equal(got, []string{"/a", "/b"}) {
		t.Errorf("posts = %v, want /a (chain stuck), /b (g1a's recovery)", got)
	}
}

func TestManagerReloadsAChangedNotifyConfig(t *testing.T) {
	host, h := hookServer(t)
	path := filepath.Join(t.TempDir(), "notify.json")
	at := time.Now().Add(-time.Hour)
	writeNotifyConfig(t, path, `{"targets": [`+target(host, "/a", `["chain_stuck"]`)+`]}`, at)
	m := newTestManager(t, false)
	captureLog(t)
	if err := m.LoadNotifyConfig(path); err != nil {
		t.Fatal(err)
	}
	m.ReloadNotifyConfigIfChanged() // unchanged: nothing to do
	writeNotifyConfig(t, path, `{"targets": [`+target(host, "/a", `["chain_stuck"]`)+`, `+target(host, "/c", `["chain_stuck"]`)+`]}`, at.Add(time.Minute))
	m.ReloadNotifyConfigIfChanged()
	m.NotifyAlert(Alert{Type: AlertChainStuck, Firing: true})
	if got := h.got(); !slices.Equal(got, []string{"/a", "/c"}) {
		t.Errorf("posts = %v, want /a and the added /c", got)
	}
}

func TestManagerKeepsTheNotifyConfigOnABrokenEdit(t *testing.T) {
	host, h := hookServer(t)
	path := filepath.Join(t.TempDir(), "notify.json")
	at := time.Now().Add(-time.Hour)
	writeNotifyConfig(t, path, `{"targets": [`+target(host, "/a", `["chain_stuck"]`)+`]}`, at)
	m := newTestManager(t, false)
	logs := captureLog(t)
	if err := m.LoadNotifyConfig(path); err != nil {
		t.Fatal(err)
	}
	writeNotifyConfig(t, path, `{"targets": [`, at.Add(time.Minute))
	for range 3 {
		m.ReloadNotifyConfigIfChanged()
	}
	m.NotifyAlert(Alert{Type: AlertChainStuck, Firing: true})
	if got := h.got(); !slices.Equal(got, []string{"/a"}) {
		t.Errorf("posts = %v, want /a: the previous config stays", got)
	}
	if n := strings.Count(logs.String(), "keeping the previous one"); n != 1 {
		t.Errorf("logs = %q, want the broken edit logged once", logs)
	}
	if want := "notify config " + path + ": unexpected EOF; keeping the previous one"; !strings.Contains(logs.String(), want) {
		t.Errorf("logs = %q, want %q", logs, want)
	}
	writeNotifyConfig(t, path, `{"targets": [`+target(host, "/c", `["chain_stuck"]`)+`]}`, at.Add(2*time.Minute))
	m.ReloadNotifyConfigIfChanged()
	m.NotifyAlert(Alert{Type: AlertChainStuck, Firing: true})
	if got := h.got(); !slices.Equal(got, []string{"/a", "/c"}) {
		t.Errorf("posts = %v, want /c once the file is fixed", got)
	}
}

func TestLoadNotifyConfigRejectsABadURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify.json")
	writeNotifyConfig(t, path, `{"targets": [{"url": "invalid://bogus", "alerts": ["chain_stuck"]}]}`, time.Now())
	if err := newTestManager(t, false).LoadNotifyConfig(path); err == nil || !strings.Contains(err.Error(), "target 1") {
		t.Errorf("LoadNotifyConfig = %v, want an error naming target 1", err)
	}
	if err := newTestManager(t, false).LoadNotifyConfig(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("LoadNotifyConfig(missing file) = nil, want an error")
	}
}

func TestNotifyTargetsListTheirAlerts(t *testing.T) {
	m := newTestManager(t, false)
	captureLog(t)
	if err := m.SetNotifyConfig(NotifyConfig{Targets: []NotifyRoute{{URL: "generic+http://example.com/hook", Alerts: map[AlertType]AlertOptions{
		AlertValsetChanged: {}, AlertChainStuck: {},
	}}}}); err != nil {
		t.Fatal(err)
	}
	targets := m.NotifyTargets()
	if len(targets) != 1 || !slices.Equal(targets[0].Alerts, []string{"chain_stuck", "valset_changed"}) {
		t.Errorf("NotifyTargets() = %+v, want one target with chain_stuck and valset_changed", targets)
	}
}

func TestManagerRereadsAFixedConfigOfTheSameModTime(t *testing.T) {
	host, h := hookServer(t)
	path := filepath.Join(t.TempDir(), "notify.json")
	at := time.Now().Add(-time.Hour)
	writeNotifyConfig(t, path, `{"targets": [`+target(host, "/a", `["chain_stuck"]`)+`]}`, at)
	m := newTestManager(t, false)
	captureLog(t)
	if err := m.LoadNotifyConfig(path); err != nil {
		t.Fatal(err)
	}
	// An editor truncates, then writes, within one tick of a coarse clock.
	tick := at.Add(time.Minute)
	writeNotifyConfig(t, path, ``, tick)
	m.ReloadNotifyConfigIfChanged()
	writeNotifyConfig(t, path, `{"targets": [`+target(host, "/c", `["chain_stuck"]`)+`]}`, tick)
	m.ReloadNotifyConfigIfChanged()
	m.NotifyAlert(Alert{Type: AlertChainStuck, Firing: true})
	if got := h.got(); !slices.Equal(got, []string{"/c"}) {
		t.Errorf("posts = %v, want /c: the fixed file is read though its time did not change", got)
	}
}

func TestParseNotifyConfigRejectsTrailingData(t *testing.T) {
	if _, err := ParseNotifyConfig([]byte(`{"targets": []} {"targets": []}`)); err == nil {
		t.Error("ParseNotifyConfig accepted a second value after the config")
	}
}

// bodyServer returns the host of a webhook server recording the bodies it is
// posted to.
func bodyServer(t *testing.T) (string, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(bodies)
	}
}

// rankingManager is a manager sending the ranking to a webhook target and
// not to another that does not list it.
func rankingManager(t *testing.T) (*Manager, func() []string, func() []string) {
	t.Helper()
	host, rankingBodies := bodyServer(t)
	otherHost, otherBodies := bodyServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	err := m.SetNotifyConfig(NotifyConfig{Targets: []NotifyRoute{
		{URL: "generic+http://" + host + "/r", Alerts: map[AlertType]AlertOptions{AlertRanking: {}}},
		{URL: "generic+http://" + otherHost + "/o", Alerts: map[AlertType]AlertOptions{AlertChainStuck: {}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return m, rankingBodies, otherBodies
}

func TestNotifyRankingSendsOncePerKey(t *testing.T) {
	m, got, other := rankingManager(t)
	m.NotifyRanking("gnoland-1:2026-11-25", "Validator ranking", "#1 A 99.0")
	m.NotifyRanking("gnoland-1:2026-11-25", "Validator ranking", "#1 A 99.0")
	if n := len(got()); n != 1 {
		t.Errorf("NotifyRanking twice with one key sent %d messages, want 1", n)
	}
	m.NotifyRanking("gnoland-1:2026-11-26", "Validator ranking", "#1 A 99.0")
	if n := len(got()); n != 2 {
		t.Errorf("NotifyRanking with a new key sent %d messages in all, want 2", n)
	}
	if n := len(other()); n != 0 {
		t.Errorf("NotifyRanking sent %d messages to a target not listing it, want 0", n)
	}
}

func TestNotifyRankingSendsNothingAgainAfterARestart(t *testing.T) {
	host, got := bodyServer(t)
	cfg := NotifyConfig{Targets: []NotifyRoute{{URL: "generic+http://" + host + "/r", Alerts: map[AlertType]AlertOptions{AlertRanking: {}}}}}
	m := newTestManager(t, false)
	captureLog(t)
	if err := m.SetNotifyConfig(cfg); err != nil {
		t.Fatal(err)
	}
	m.NotifyRanking("gnoland-1:2026-11-25", "Validator ranking", "#1 A 99.0")
	restarted, err := NewManager(m.db, 30, 5)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.SetNotifyConfig(cfg); err != nil {
		t.Fatal(err)
	}
	restarted.NotifyRanking("gnoland-1:2026-11-25", "Validator ranking", "#1 A 99.0")
	if n := len(got()); n != 1 {
		t.Errorf("NotifyRanking of one key before and after a restart sent %d messages, want 1", n)
	}
}

func TestNotifyRankingSplitsAtLines(t *testing.T) {
	m, got, _ := rankingManager(t)
	lines := make([]string, 90)
	for i := range lines {
		lines[i] = fmt.Sprintf("#%d %s 99.0 =", i+1, strings.Repeat("n", 40))
	}
	body := strings.Join(lines, "\n")
	m.NotifyRanking("gnoland-1:2026-11-25", "Validator ranking", body)
	parts := got()
	if len(parts) != 3 {
		t.Fatalf("NotifyRanking of %d characters sent %d parts, want 3", len(body), len(parts))
	}
	for i, p := range parts {
		if n := len([]rune(p)); n > 2000 {
			t.Errorf("part %d has %d characters, want at most 2000", i, n)
		}
	}
	for i, l := range lines {
		if !slices.ContainsFunc(parts, func(p string) bool { return strings.Contains(p, l+"\n") || strings.HasSuffix(p, l) }) {
			t.Errorf("line %d %q is cut across the parts", i, l)
		}
	}
}

func TestSplitMessageCutsALongLineHard(t *testing.T) {
	long := strings.Repeat("é", 2500)
	got := splitMessage("short\n"+long+"\nend", 2000)
	want := []string{"short", strings.Repeat("é", 2000), strings.Repeat("é", 500) + "\nend"}
	if !slices.Equal(got, want) {
		t.Errorf("splitMessage = %d parts %q, want %d parts", len(got), got, len(want))
	}
}

func TestNotifyRankingKeepsTheLinkInsideTheCap(t *testing.T) {
	m, got, _ := rankingManager(t)
	m.SetPublicURL("https://x/")
	lines := make([]string, 90)
	for i := range lines {
		lines[i] = fmt.Sprintf("#%d %s 99.0 =", i+1, strings.Repeat("n", 40))
	}
	body := strings.Join(lines, "\n")
	m.NotifyRanking("gnoland-1:2026-11-25", "Validator ranking", body)
	a := Alert{Type: AlertRanking, Firing: true, Notice: true, Title: "Validator ranking", Body: body, Link: rankingLink}
	want := FormatAlert(m.ChainName(), "https://x/", a)
	parts := got()
	if joined := strings.Join(parts, "\n"); joined != want {
		t.Errorf("parts joined by line breaks differ from FormatAlert: got %d characters, want %d", len(joined), len(want))
	}
	last := parts[len(parts)-1]
	if !strings.HasSuffix(last, "\nhttps://x/?view=ranking") {
		t.Errorf("last part ends %q, want the link as its last line", last[max(0, len(last)-40):])
	}
	for i, p := range parts {
		if n := len([]rune(p)); n > 2000 {
			t.Errorf("part %d has %d characters, want at most 2000", i, n)
		}
	}
}

func TestSplitMessageKeepsBlankLines(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		limit      int
		want       []string
	}{
		{"blank after a split", "aaaa\nbbbb\n\ncccc", 9, []string{"aaaa\nbbbb", "\ncccc"}},
		{"leading blank", "\naaaa", 9, []string{"\naaaa"}},
		{"blank inside a part", "aa\n\nbb", 9, []string{"aa\n\nbb"}},
	} {
		got := splitMessage(tc.text, tc.limit)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: splitMessage(%q, %d) = %q, want %q", tc.name, tc.text, tc.limit, got, tc.want)
		}
		if joined := strings.Join(got, "\n"); joined != tc.text {
			t.Errorf("%s: parts joined = %q, want %q", tc.name, joined, tc.text)
		}
	}
}

// zwsp is the zero-width space a webhook's text carries after each '@' and
// '<', so that a name never mentions anyone.
const zwsp = "\xe2\x80\x8b"

// TestExternalNotificationsNeutraliseMentions checks the text a webhook
// gets for names that would mention: a validator named "@everyone" missing
// blocks, and a ranking listing it and one named "<!channel>". Each '@' and
// '<' is followed by a zero-width space; the link, whose address holds an
// '@', stays as it is.
func TestExternalNotificationsNeutraliseMentions(t *testing.T) {
	host, got := bodyServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	m.SetPublicURL("https://watch@gnockpit.example.com")
	err := m.SetNotifyConfig(NotifyConfig{Targets: []NotifyRoute{{URL: "generic+http://" + host + "/r", Alerts: map[AlertType]AlertOptions{
		AlertValidatorMissingBlocks: {}, AlertRanking: {},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	snap := func(missed int) *node.Snapshot {
		return &node.Snapshot{
			Status:     &node.Status{NodeInfo: node.NodeInfo{Network: "test-13"}, SyncInfo: node.SyncInfo{LatestBlockHeight: "100"}},
			Validators: []node.Validator{{Address: "g1aaa", Name: "@everyone"}, {Address: "g1bbb", Name: "<!channel>"}},
			Signing: &node.SigningStats{WindowSize: 100, ValidatorSigning: map[string]node.ValSigning{
				"g1aaa": {MissedInARow: missed}, "g1bbb": {SignedInARow: 10},
			}},
		}
	}
	m.EvaluateAndNotify(snap(0))
	m.EvaluateAndNotify(snap(5))
	day := ranking.Published{Chain: "test-13", Day: "2026-11-25", Result: ranking.Result{
		Formula: 1, FirstDay: "2026-10-26", LastDay: "2026-11-24",
		Entries: []ranking.Entry{
			{Operator: "g1aaa", Name: "@everyone", Status: ranking.Ranked, Rank: 1, Score: 99},
			{Operator: "g1bbb", Name: "<!channel>", Status: ranking.Ranked, Rank: 2, Score: 98},
		},
	}}
	title, body := ranking.Digest(day, nil)
	m.NotifyRanking("test-13:2026-11-25", title, body)
	want := []string{
		"🚨 test-13 — Validator missing blocks\n@" + zwsp + "everyone missed 5 blocks in a row\nhttps://watch@gnockpit.example.com",
		"🔔 test-13 — Validator ranking, 2026-10-26 – 2026-11-24 UTC (formula v1)\n#1 @" + zwsp + "everyone 99.0\n#2 <" + zwsp + "!channel> 98.0\n" +
			"Rank changes not shown: no earlier ranking\nhttps://watch@gnockpit.example.com/?view=ranking",
	}
	if bodies := got(); !slices.Equal(bodies, want) {
		t.Errorf("webhook texts = %q, want %q", bodies, want)
	}
}

// TestNotifyRankingSplitsTheTextAsSent checks that a ranking's parts stay
// within 2,000 characters counted on the text as sent: 1,950 characters of
// lines whose names hold '@', each written with a zero-width space after it,
// go as two parts.
func TestNotifyRankingSplitsTheTextAsSent(t *testing.T) {
	m, got, _ := rankingManager(t)
	lines := make([]string, 39)
	for i := range lines {
		lines[i] = fmt.Sprintf("#%02d %s 99.0", i+1, strings.Repeat("@", 40))
	}
	body := strings.Join(lines, "\n")
	m.NotifyRanking("gnoland-1:2026-11-25", "Validator ranking", body)
	parts := got()
	if len(parts) != 2 {
		t.Fatalf("NotifyRanking of %d characters, %d '@', sent %d parts, want 2", len([]rune(body)), strings.Count(body, "@"), len(parts))
	}
	for i, p := range parts {
		if n := len([]rune(p)); n > 2000 {
			t.Errorf("part %d has %d characters, want at most 2000", i, n)
		}
	}
}
