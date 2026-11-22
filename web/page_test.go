//go:build page

package web

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"maps"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mxschmitt/playwright-go"

	"github.com/gnoverse/gnockpit/node"
)

// pageNow is the instant a page test reads, on the server and in the
// browser, unless the test names another.
var pageNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

const (
	// pageTimeout bounds every assertion and every wait of the harness.
	pageTimeout = 5 * time.Second
	// requestWait is how long the test server waits for a request on a
	// connection: far past the time a browser takes to send its request on a
	// connection it opened, well within pageTimeout.
	requestWait = 2 * time.Second
	// pageChainStuckSecs and pageMaxMissedInARow are main.go's defaults.
	pageChainStuckSecs  = 30
	pageMaxMissedInARow = 10

	chromium = "chromium"
	webkit   = "webkit"
)

// ---- Browsers

// pw is the Playwright driver TestMain starts for the package's tests.
var pw *playwright.Playwright

// browsers holds one browser per type, launched on first use.
var browsers = struct {
	sync.Mutex
	byType map[string]playwright.Browser
}{byType: map[string]playwright.Browser{}}

func TestMain(m *testing.M) {
	os.Exit(runPageTests(m))
}

// runPageTests runs the package's tests with Playwright started, then closes
// the browsers and the driver.
func runPageTests(m *testing.M) int {
	var err error
	if pw, err = playwright.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "page tests: start playwright (install it with make install-browsers): %v\n", err)
		return 1
	}
	code := m.Run()
	if err := stopPlaywright(); err != nil {
		fmt.Fprintf(os.Stderr, "page tests: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	return code
}

func stopPlaywright() error {
	browsers.Lock()
	defer browsers.Unlock()
	var errs []error
	for name, b := range browsers.byType {
		if err := b.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", name, err))
		}
	}
	if err := pw.Stop(); err != nil {
		errs = append(errs, fmt.Errorf("stop playwright: %w", err))
	}
	return errors.Join(errs...)
}

// browserOf returns the browser of the type, launching it on first use.
func browserOf(t *testing.T, browserType string) playwright.Browser {
	t.Helper()
	browsers.Lock()
	defer browsers.Unlock()
	if b, ok := browsers.byType[browserType]; ok {
		return b
	}
	var bt playwright.BrowserType
	switch browserType {
	case chromium:
		bt = pw.Chromium
	case webkit:
		bt = pw.WebKit
	default:
		t.Fatalf("unknown browser type %q", browserType)
	}
	b, err := bt.Launch()
	if err != nil {
		t.Fatalf("launch %s: %v", browserType, err)
	}
	browsers.byType[browserType] = b
	return b
}

// ---- Profiles

// profile is a browser and the window a test drives it in: viewport for a
// profile without a device, the device's own window and input otherwise.
type profile struct {
	name, browser, device string
	viewport              playwright.Size
}

var (
	desktop = profile{name: "desktop", browser: chromium, viewport: playwright.Size{Width: 1280, Height: 800}}
	// tablet sits in the page's 481–768 and 641–720 px bands.
	tablet = profile{name: "tablet", browser: chromium, viewport: playwright.Size{Width: 700, Height: 1000}}
	// phone stands in for iOS Safari: touch, no hover, and neither
	// PushManager nor Notification.
	phone = profile{name: "phone", browser: webkit, device: "iPhone 17"}
	// desktopWebKit stands in for Safari on a desktop: a mouse, hover, no
	// touch. It covers behaviour that differs on Safari desktop.
	desktopWebKit = profile{name: "desktopWebKit", browser: webkit, viewport: playwright.Size{Width: 1280, Height: 800}}
)

// hasPush tells whether the profile's browser offers web push: Chromium
// always, and WebKit without a device, which has PushManager like Safari on
// a desktop. WebKit's iPhone emulation has none, so the page never asks the
// server for its push key there.
func (p profile) hasPush() bool { return p.browser == chromium || p.device == "" }

// touch tells whether the profile's screen is a touch screen, as its device
// says: the phone and the landscape iPhone are; a profile without a device
// has a mouse.
func (p profile) touch() bool {
	d, ok := pw.Devices[p.device]
	return ok && d.HasTouch
}

// onProfiles runs fn as one subtest per profile, named after it.
func onProfiles(t *testing.T, ps []profile, fn func(t *testing.T, p profile)) {
	t.Helper()
	for _, p := range ps {
		t.Run(p.name, func(t *testing.T) { fn(t, p) })
	}
}

// ---- Options

// pageConfig is what the options of openPage set.
type pageConfig struct {
	path          string
	navigate      bool
	storage       map[string]string
	timezone      string
	locale        string
	browserOffset time.Duration
	installClock  bool
	expects       []*expectation
	// notificationPermission is the push stub's; nil grants it.
	notificationPermission *notificationPermission
}

// pageOption changes how openPage opens the page.
type pageOption func(*pageConfig)

// withURL opens path instead of "/".
func withURL(path string) pageOption {
	return func(c *pageConfig) { c.path = path }
}

// withStorage puts entries in the page's localStorage before it loads, once
// for the test: a reload keeps what the page itself stored.
func withStorage(entries map[string]string) pageOption {
	return func(c *pageConfig) { c.storage = entries }
}

// withTimezone runs the browser in the IANA timezone id instead of UTC.
func withTimezone(id string) pageOption {
	return func(c *pageConfig) { c.timezone = id }
}

// withLocale runs the browser in the BCP 47 locale id instead of en-US.
func withLocale(id string) pageOption {
	return func(c *pageConfig) { c.locale = id }
}

// withBrowserOffset sets the browser's clock d away from the server's.
func withBrowserOffset(d time.Duration) pageOption {
	return func(c *pageConfig) { c.browserOffset = d }
}

// withInstalledClock installs Playwright's fake timers, paused at the test's
// instant: no timer, animation frame included, fires until the test moves
// the clock with runFor.
func withInstalledClock() pageOption {
	return func(c *pageConfig) { c.installClock = true }
}

// expectConsole declares a console error or warning matching re, which must
// occur. re is matched against the level and the text: "error: …" or
// "warning: …".
func expectConsole(re string) pageOption {
	pattern := regexp.MustCompile(re)
	return func(c *pageConfig) {
		c.expects = append(c.expects, &expectation{
			what:     "console message matching " + re,
			required: true,
			matches:  func(ev pageEvent) bool { return ev.kind == consoleEvent && pattern.MatchString(ev.text) },
		})
	}
}

// expectStatus declares an answer code to a request for path (query
// excluded), which must occur, and the console error the browser logs for it.
func expectStatus(path string, code int) pageOption {
	return func(c *pageConfig) { c.expects = append(c.expects, statusAnswer(path, code)...) }
}

// noNavigate leaves the page blank: the test opens it with navigate.
func noNavigate() pageOption {
	return func(c *pageConfig) { c.navigate = false }
}

// ---- Page environment

// pageEnv is one test's server, browser context and page.
type pageEnv struct {
	T      *testing.T
	Srv    *Server
	Page   playwright.Page
	Ctx    playwright.BrowserContext
	Expect playwright.PlaywrightAssertions
	Push   *fakePushService // nil unless the scenario has push

	profile profile
	clock   *serverClock // the server's
	ts      *httptest.Server
	gate    *gate
	hygiene *hygiene
	// atLoad are the answers each load gets from the scenario's disabled
	// features.
	atLoad []*expectation
	// published are the snapshots the server took: it changes one as it
	// publishes it, while a WebSocket client may read it.
	published map[*node.Snapshot]bool
}

// openPages counts the page environments open in the process.
var openPages atomic.Int32

// openPage starts a server on sc, opens a fresh browser context of profile p
// on it and loads the dashboard. Everything is torn down with the test, which
// fails on any console error or warning, page error, failed request or
// answer of 400 or more it did not declare.
//
// Page tests must not call t.Parallel: the capture of the server's log
// swaps the process-wide log output. openPage fails a test that opens a page
// while another is open.
func openPage(t *testing.T, p profile, sc *scenario, opts ...pageOption) *pageEnv {
	t.Helper()
	open := openPages.Add(1)
	t.Cleanup(func() { openPages.Add(-1) })
	if open > 1 {
		t.Fatal("openPage: another page is open: page tests must not run in parallel, the server's log capture swaps the process-wide log output")
	}
	cfg := pageConfig{path: "/", navigate: true, timezone: "UTC", locale: "en-US"}
	for _, o := range opts {
		o(&cfg)
	}

	captureServerLog(t)
	e := &pageEnv{
		T:       t,
		Expect:  playwright.NewPlaywrightAssertions(float64(pageTimeout.Milliseconds())),
		profile: p,
		hygiene: &hygiene{},
	}
	if sc.push {
		e.Push = newFakePushService(t)
	}
	e.clock = &serverClock{at: sc.clock()}
	e.Srv = newPageServer(t, sc, e.clock)
	e.published = map[*node.Snapshot]bool{sc.snap: true}
	var stub *pushStub
	if sc.push {
		stub = newPushStub(t, e.Push, e.Srv.PushManager.VAPIDPublicKey(), cfg.notificationPermission)
	} else if cfg.notificationPermission != nil {
		t.Fatal("withNotificationPermission: the scenario has no push stub")
	}
	e.gate = &gate{next: e.Srv.routes(), closed: make(chan struct{})}
	e.ts = httptest.NewUnstartedServer(e.gate)
	// Every request gets its own connection: a browser sends a request again
	// when a reused connection closes unanswered, which would defeat drop.
	e.ts.Config.SetKeepAlivesEnabled(false)
	// WebKit sends a dropped request again on a new connection, and at
	// times leaves that connection without the request: closed, it sends
	// the request on another, so that the drop ends in the failure.
	e.ts.Config.ReadHeaderTimeout = requestWait
	e.ts.Start()
	t.Cleanup(e.ts.Close)
	t.Cleanup(e.gate.close)

	e.atLoad = loadAnswers(p, sc)
	e.hygiene.allow(e.atLoad...)
	e.hygiene.declare(cfg.expects...)
	if !sc.push {
		e.hygiene.allow(blockedServiceWorker)
	}
	e.Ctx = newBrowserContext(t, p, cfg, e.ts.URL, sc.push)
	e.listen()
	if stub != nil {
		script := stub.initScript()
		if err := e.Ctx.AddInitScript(playwright.Script{Content: &script}); err != nil {
			t.Fatalf("add push stub: %v", err)
		}
	}
	e.setClock(sc.clock().Add(cfg.browserOffset), cfg.installClock)
	page, err := e.Ctx.NewPage()
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	e.Page = page
	t.Cleanup(e.finish)

	if cfg.navigate {
		e.navigate(cfg.path)
	}
	return e
}

// newBrowserContext opens the context of profile p on origin, tracing it,
// and closes it with the test.
func newBrowserContext(t *testing.T, p profile, cfg pageConfig, origin string, push bool) playwright.BrowserContext {
	t.Helper()
	opts := playwright.BrowserNewContextOptions{
		Locale:         playwright.String(cfg.locale),
		TimezoneId:     playwright.String(cfg.timezone),
		ServiceWorkers: playwright.ServiceWorkerPolicyBlock,
	}
	if push {
		opts.ServiceWorkers = playwright.ServiceWorkerPolicyAllow
		opts.Permissions = append(opts.Permissions, "notifications")
	}
	if p.browser == chromium {
		opts.Permissions = append(opts.Permissions, "clipboard-read", "clipboard-write")
	}
	if p.device != "" {
		d, ok := pw.Devices[p.device]
		if !ok {
			t.Fatalf("profile %s: no device %q", p.name, p.device)
		}
		opts.UserAgent = playwright.String(d.UserAgent)
		opts.Viewport = d.Viewport
		opts.Screen = d.Screen
		opts.DeviceScaleFactor = playwright.Float(d.DeviceScaleFactor)
		opts.IsMobile = playwright.Bool(d.IsMobile)
		opts.HasTouch = playwright.Bool(d.HasTouch)
	} else {
		vp := p.viewport
		opts.Viewport = &vp
	}
	if len(cfg.storage) > 0 {
		entries := make([]playwright.NameValue, 0, len(cfg.storage))
		for _, k := range slices.Sorted(maps.Keys(cfg.storage)) {
			entries = append(entries, playwright.NameValue{Name: k, Value: cfg.storage[k]})
		}
		opts.StorageState = &playwright.OptionalStorageState{
			Origins: []playwright.Origin{{Origin: origin, LocalStorage: entries}},
		}
	}
	ctx, err := browserOf(t, p.browser).NewContext(opts)
	if err != nil {
		t.Fatalf("new %s context: %v", p.name, err)
	}
	t.Cleanup(func() {
		if err := ctx.Close(); err != nil {
			t.Errorf("close browser context: %v", err)
		}
	})
	ctx.SetDefaultTimeout(float64(pageTimeout.Milliseconds()))
	if err := ctx.Tracing().Start(playwright.TracingStartOptions{
		Title:       playwright.String(t.Name()),
		Screenshots: playwright.Bool(true),
		Snapshots:   playwright.Bool(true),
	}); err != nil {
		t.Fatalf("start tracing: %v", err)
	}
	return ctx
}

// setClock sets the browser's clock to at: fixed with the timers running, or
// paused with Playwright's fake timers installed.
func (e *pageEnv) setClock(at time.Time, install bool) {
	e.T.Helper()
	clock := e.Ctx.Clock()
	if !install {
		if err := clock.SetFixedTime(at); err != nil {
			e.T.Fatalf("set the browser clock: %v", err)
		}
		return
	}
	if err := clock.Install(playwright.ClockInstallOptions{Time: at}); err != nil {
		e.T.Fatalf("install the browser clock: %v", err)
	}
	if err := clock.PauseAt(at); err != nil {
		e.T.Fatalf("pause the browser clock: %v", err)
	}
}

// navigate loads path from the server and waits for the answers this load
// gets from the scenario's disabled features.
func (e *pageEnv) navigate(path string) {
	e.T.Helper()
	e.navigateTo(e.ts.URL + path)
}

// navigateTo loads rawURL, from the server or from a proxy in front of it,
// and waits for the answers this load gets from the scenario's disabled
// features.
func (e *pageEnv) navigateTo(rawURL string) {
	e.T.Helper()
	from := e.hygiene.mark()
	if _, err := e.Page.Goto(rawURL); err != nil {
		e.T.Fatalf("open %s: %v", rawURL, err)
	}
	var answers []*expectation
	for _, x := range requiredOf(e.atLoad) {
		answers = append(answers, since(from, x))
	}
	if err := e.hygiene.await(answers); err != nil {
		e.T.Fatalf("open %s: %v", rawURL, err)
	}
}

// publish makes snap the server's snapshot and sends it to the open pages,
// as the publish loop does after a fetch. The server takes snap and changes
// it as it publishes it, while the pages connecting read it: each publish
// takes a snapshot of its own, which publish checks of snap itself.
func (e *pageEnv) publish(snap *node.Snapshot) {
	e.T.Helper()
	if e.published[snap] {
		e.T.Fatal("publish: the server already took this snapshot; build a fresh one")
	}
	e.published[snap] = true
	publishSnapshot(e.T.Context(), e.Srv, snap)
}

// clockStep is the longest step runFor moves the browser's clock in one go.
const clockStep = time.Second

// runFor moves the browser's installed clock d forward, firing the timers
// due on the way, and the server's clock with it. The fake clock jumps while
// answers take real time, and a page that bounds a read would otherwise give
// up one a browser gets answered in milliseconds, so runFor:
//   - moves in steps of clockStep, the server's clock first, so that a
//     timer due at a step's end reads the same now on both;
//   - after each step, waits until every request the browser has in flight
//     is one the gate holds, and fails the test after pageTimeout, listing
//     the URIs still without an answer;
//   - settles a request whose start reaches the harness only after a step's
//     wait with the next step's wait, and does not wait for one that reaches
//     it after the last step's.
//
// Calling Ctx.Clock().RunFor directly moves the browser's clock without any
// of this.
func (e *pageEnv) runFor(d time.Duration) {
	e.T.Helper()
	for left := d; left > 0; left -= clockStep {
		step := min(left, clockStep)
		e.clock.advance(step)
		if err := e.Ctx.Clock().RunFor(step.Milliseconds()); err != nil {
			e.T.Fatalf("run the browser clock for %s: %v", d, err)
		}
		if err := e.gate.settle(); err != nil {
			e.T.Fatalf("run the browser clock for %s: %v", d, err)
		}
	}
}

// fastForward jumps the browser's installed clock d forward, and the
// server's with it, firing the timers due on the way at most once, as
// Clock.FastForward does.
func (e *pageEnv) fastForward(d time.Duration) {
	e.T.Helper()
	e.clock.advance(d)
	if err := e.Ctx.Clock().FastForward(d.Milliseconds()); err != nil {
		e.T.Fatalf("fast-forward the browser clock %s: %v", d, err)
	}
}

// expect returns the retrying assertions on the elements selector matches.
func (e *pageEnv) expect(selector string) playwright.LocatorAssertions {
	return e.Expect.Locator(e.Page.Locator(selector))
}

// must fails the test at once on a failed assertion.
func (e *pageEnv) must(err error) {
	e.T.Helper()
	if err != nil {
		e.T.Fatal(err)
	}
}

// closeWebSockets closes every WebSocket connection from the server's end,
// as a server going away does: with a close frame, which spares the test the
// console error WebKit logs for a connection lost without one.
func (e *pageEnv) closeWebSockets() {
	e.T.Helper()
	bye := websocket.FormatCloseMessage(websocket.CloseGoingAway, "")
	e.Srv.wsmu.RLock()
	defer e.Srv.wsmu.RUnlock()
	for c := range e.Srv.wsClients {
		err := c.conn.WriteControl(websocket.CloseMessage, bye, time.Now().Add(pageTimeout))
		if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, websocket.ErrCloseSent) {
			e.T.Errorf("send a WebSocket close frame: %v", err)
		}
		if err := c.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			e.T.Errorf("close a WebSocket connection: %v", err)
		}
	}
}

// clipboard reads the clipboard; Chromium profiles only, which are granted
// its permissions.
func (e *pageEnv) clipboard() string {
	e.T.Helper()
	if e.profile.browser != chromium {
		e.T.Fatalf("clipboard: read on Chromium profiles only, not %s", e.profile.name)
	}
	v, err := e.Page.Evaluate(`() => navigator.clipboard.readText()`)
	if err != nil {
		e.T.Fatalf("read the clipboard: %v", err)
	}
	s, ok := v.(string)
	if !ok {
		e.T.Fatalf("read the clipboard: got %T, want a string", v)
	}
	return s
}

// storage reads key from the page's localStorage; false when it is not set.
func (e *pageEnv) storage(key string) (string, bool) {
	e.T.Helper()
	v, err := e.Page.Evaluate(`(k) => localStorage.getItem(k)`, key)
	if err != nil {
		e.T.Fatalf("read localStorage %q: %v", key, err)
	}
	if v == nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		e.T.Fatalf("read localStorage %q: got %T, want a string", key, v)
	}
	return s, true
}

// finish checks the test's hygiene and that each hold got its request,
// then keeps a trace and a screenshot of a failed test.
func (e *pageEnv) finish() {
	t := e.T
	e.hygiene.verify(t)
	for _, h := range e.gate.unclaimed() {
		t.Errorf("hold %s: no request came for it", h.prefix)
	}
	if !t.Failed() {
		if err := e.Ctx.Tracing().Stop(); err != nil {
			t.Errorf("stop tracing: %v", err)
		}
		return
	}
	dir := os.Getenv("PAGE_ARTIFACTS")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "gnockpit-page")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Errorf("artifacts: %v", err)
		return
	}
	base := filepath.Join(dir, artifactName.ReplaceAllString(t.Name(), "_"))
	shot, trace := base+".png", base+".zip"
	if _, err := e.Page.Screenshot(playwright.PageScreenshotOptions{Path: &shot, FullPage: playwright.Bool(true)}); err != nil {
		t.Errorf("screenshot: %v", err)
	}
	if err := e.Ctx.Tracing().Stop(trace); err != nil {
		t.Errorf("save the trace: %v", err)
	}
	t.Logf("trace %s, screenshot %s", trace, shot)
}

// artifactName matches what a file name of an artifact does not keep of a
// test name.
var artifactName = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// captureServerLog sends the log of the server under test to a buffer shown
// when the test fails, so a passing test's output stays clean.
func captureServerLog(t *testing.T) {
	t.Helper()
	var buf lockedBuffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(prev)
		if s := buf.String(); t.Failed() && s != "" {
			t.Logf("server log:\n%s", s)
		}
	})
}

// lockedBuffer is a buffer safe for concurrent use.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
