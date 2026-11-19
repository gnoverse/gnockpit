package push

import (
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
