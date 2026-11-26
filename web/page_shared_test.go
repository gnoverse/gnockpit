//go:build page

package web

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// ---- Input

// press clicks the element l locates, or taps it on a touch screen.
func (e *pageEnv) press(l playwright.Locator) {
	e.T.Helper()
	if e.profile.touch() {
		e.must(l.Tap())
		return
	}
	e.must(l.Click())
}

// point taps the element l locates on a touch screen, or hovers it with the
// pointer coming from the page's corner, once l is scrolled into view and
// the scroll's event has passed: a scroll hides popovers, and an
// intersection observer, which ToBeInViewport waits on, reports after the
// frame that dispatches it.
func (e *pageEnv) point(l playwright.Locator) {
	e.T.Helper()
	e.must(l.ScrollIntoViewIfNeeded())
	e.must(e.Expect.Locator(l).ToBeInViewport())
	if e.profile.touch() {
		e.must(l.Tap())
		return
	}
	e.must(e.Page.Mouse().Move(0, 0))
	e.must(l.Hover())
}

// ---- Popover

// pagePopover is the page's one popover.
const pagePopover = "#popover"

// showPopover opens l's popover: a tap on a touch screen, a hover otherwise
// (see point).
func (e *pageEnv) showPopover(l playwright.Locator) {
	e.T.Helper()
	e.point(l)
	e.must(e.expect(pagePopover).ToBeVisible())
}

// watchPopover starts recording the popover's class changes, an
// observation only: the function it returns counts the times the popover
// lost its "visible" class since.
func (e *pageEnv) watchPopover() func() int {
	e.T.Helper()
	if _, err := e.Page.Evaluate(`() => {
		const pop = document.getElementById('popover')
		const before = []
		const watch = new MutationObserver((records) => {
			for (const r of records) before.push(r.oldValue)
		})
		watch.observe(pop, { attributes: true, attributeFilter: ['class'], attributeOldValue: true })
		window.popoverClassWatch = { before, watch }
	}`); err != nil {
		e.T.Fatalf("watch the popover: %v", err)
	}
	return func() int {
		e.T.Helper()
		v, err := e.Page.Evaluate(`() => {
			const { before, watch } = window.popoverClassWatch
			for (const r of watch.takeRecords()) before.push(r.oldValue)
			const states = before.concat(document.getElementById('popover').className)
			const shown = (c) => /\bvisible\b/.test(c)
			let lost = 0
			for (let i = 1; i < states.length; i++) if (shown(states[i - 1]) && !shown(states[i])) lost++
			return lost
		}`)
		if err != nil {
			e.T.Fatalf("read the popover's class changes: %v", err)
		}
		return decodeValue[int](e.T, v)
	}
}

// rowsPopover is a rows popover as shown: its title, its rows' labels
// and values, and the colours of the swatches before labels, in order.
type rowsPopover struct {
	title          string
	labels, values []string
	swatches       []string
}

// expectPopover waits until the popover shows want.
func (e *pageEnv) expectPopover(want rowsPopover) {
	e.T.Helper()
	e.must(e.expect("#popover").ToBeVisible())
	e.must(e.expect("#popover > .t").ToHaveText(want.title))
	e.must(e.expect("#popover td.l").ToHaveText(want.labels))
	e.must(e.expect("#popover td.v").ToHaveText(want.values))
	sw := e.Page.Locator("#popover td.l .sw")
	e.must(e.Expect.Locator(sw).ToHaveCount(len(want.swatches)))
	for i, c := range want.swatches {
		e.must(e.Expect.Locator(sw.Nth(i)).ToHaveCSS("background-color", c))
	}
}

// popoverPlacement reads where the popover sits beside target.
func (e *pageEnv) popoverPlacement(target playwright.Locator) placement {
	e.T.Helper()
	e.must(e.expect("#popover").ToBeVisible())
	v, err := target.Evaluate(`el => {
		const t = el.getBoundingClientRect(), pop = document.getElementById('popover'), p = pop.getBoundingClientRect()
		return [p.top, p.left, pop.offsetWidth, pop.offsetHeight, t.top, t.bottom, t.left, innerWidth, innerHeight]
	}`, nil)
	if err != nil {
		e.T.Fatalf("read the popover's place: %v", err)
	}
	n := decodeValue[[9]float64](e.T, v)
	return placement{n[0], n[1], n[2], n[3], n[4], n[5], n[6], n[7], n[8]}
}

// placement is where the popover sits beside its target, in the
// window's CSS pixels.
type placement struct {
	top, left, width, height            float64
	targetTop, targetBottom, targetLeft float64
	innerWidth, innerHeight             float64
}

// ---- Values

// thousands is n with thousands separators, as the page writes heights and
// counts.
func thousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// Colours of the page, as the browser computes them.
const (
	fgColor     = "rgb(201, 209, 217)" // --fg: a value outside any band
	fg2Color    = "rgb(139, 148, 158)" // --fg2: what qualifies a value, a value of no note
	blueColor   = "rgb(88, 166, 255)"  // --blue
	greenColor  = "rgb(63, 185, 80)"   // --green
	yellowColor = "rgb(210, 153, 34)"  // --yellow
	redColor    = "rgb(248, 81, 73)"   // --red
	borderColor = "rgb(48, 54, 61)"    // --border
)

// bandColor is the colour of each band a value is classed in.
var bandColor = map[string]string{
	"ok":   greenColor,
	"warn": yellowColor,
	"err":  redColor,
}

// expectValue checks a value's text, its class, which carries "changed" too
// while the value flashes, and its colour: its band's when the class has
// one.
func expectValue(e *pageEnv, selector, text, class string) {
	e.T.Helper()
	e.must(e.expect(selector).ToHaveText(text))
	e.must(e.expect(selector).ToHaveClass(regexp.MustCompile(`^` + regexp.QuoteMeta(class) + `(?: changed)?$`)))
	color := fgColor
	for _, c := range strings.Fields(class) {
		if band, ok := bandColor[c]; ok {
			color = band
		}
	}
	e.must(e.expect(selector).ToHaveCSS("color", color))
}

// How often the page reads: the boot status, which a read may take
// readBootWithin to answer; the header links; the block history's fill; the
// ETA cards' estimates, which a read of /api/eta may take cardReadWithin to
// answer.
const (
	readBootEvery  = 5 * time.Second
	readBootWithin = 4 * time.Second
	readLinksEvery = 30 * time.Second
	syncPoll       = 10 * time.Second
	cardPoll       = 10 * time.Second
	cardReadWithin = 10 * time.Second
)

// ---- Reading the page

// evalFloats evaluates expr, which gives numbers, in the page.
func evalFloats(e *pageEnv, expr string, arg any) []float64 {
	e.T.Helper()
	v, err := e.Page.Evaluate(expr, arg)
	e.must(err)
	list, ok := v.([]any)
	if !ok {
		e.T.Fatalf("%s = %T, want a list", expr, v)
	}
	out := make([]float64, len(list))
	for i, x := range list {
		switch n := x.(type) {
		case float64:
			out[i] = n
		case int:
			out[i] = float64(n)
		default:
			e.T.Fatalf("%s[%d] = %T, want a number", expr, i, x)
		}
	}
	return out
}

// hasClassNow tells whether the element sel matches has class, read once,
// without waiting: it checks a redraw before the browser's own hover update,
// which follows a redraw by a frame on Chromium and by several on WebKit.
func hasClassNow(e *pageEnv, sel, class string) bool {
	e.T.Helper()
	v, err := e.Page.Locator(sel).GetAttribute("class")
	e.must(err)
	return slices.Contains(strings.Fields(v), class)
}

// boxOf returns the bounding box of the first element selector matches, in
// the viewport's CSS pixels.
func boxOf(e *pageEnv, selector string) *playwright.Rect {
	e.T.Helper()
	b, err := e.Page.Locator(selector).First().BoundingBox()
	if err != nil || b == nil {
		e.T.Fatalf("bounding box of %s: %v", selector, err)
	}
	return b
}

func evalInt(e *pageEnv, expr string) int {
	e.T.Helper()
	v, err := e.Page.Evaluate(expr)
	e.must(err)
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	e.T.Fatalf("%s = %T, want a number", expr, v)
	return 0
}

// textRight returns where the text of the first element selector
// matches ends, in the viewport's CSS pixels.
func textRight(e *pageEnv, selector string) float64 {
	e.T.Helper()
	v, err := e.Page.Locator(selector).First().Evaluate(`(el) => {
		const r = document.createRange()
		r.selectNodeContents(el)
		return r.getBoundingClientRect().right
	}`, nil)
	e.must(err)
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	e.T.Fatalf("text end of %s = %T, want a number", selector, v)
	return 0
}

// pseudoContent returns the computed content of the pseudo-element of the
// first element selector matches.
func pseudoContent(e *pageEnv, selector, pseudo string) string {
	e.T.Helper()
	v, err := e.Page.Evaluate(`([sel, pseudo]) => getComputedStyle(document.querySelector(sel), pseudo).content`, []string{selector, pseudo})
	e.must(err)
	s, _ := v.(string)
	return s
}

// narrow tells whether the viewport is 768 px wide or less, the page's
// phone and tablet layout.
func (e *pageEnv) narrow() bool {
	w, err := e.Page.Evaluate(`() => window.innerWidth`)
	if err != nil {
		e.T.Fatalf("read the viewport width: %v", err)
	}
	px, ok := w.(int)
	if !ok {
		e.T.Fatalf("viewport width: got %T, want an int", w)
	}
	return px <= 768
}

// historyLength reads history.length.
func (e *pageEnv) historyLength() int {
	e.T.Helper()
	v, err := e.Page.Evaluate(`() => history.length`)
	if err != nil {
		e.T.Fatalf("read history.length: %v", err)
	}
	n, ok := v.(int)
	if !ok {
		e.T.Fatalf("history.length: got %T, want an int", v)
	}
	return n
}

// numberOf reads a number the expression gives on the element l
// finds, e.g. "el => el.clientWidth".
func (e *pageEnv) numberOf(l playwright.Locator, expression string) float64 {
	e.T.Helper()
	v, err := l.Evaluate(expression, nil)
	if err != nil {
		e.T.Fatalf("read %s of %v: %v", expression, l, err)
	}
	return decodeValue[float64](e.T, v)
}

// nearPx tells whether two lengths in CSS pixels are one within
// the 1/32 px a browser's layout may round them to.
func nearPx(a, b float64) bool {
	return math.Abs(a-b) <= 1.0/32
}

// decodeValue converts what an Evaluate returned into T.
func decodeValue[T any](t *testing.T, v any) T {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode %v: %v", v, err)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return out
}

// ---- Page mechanics

// focusByKeyboard moves the focus to the element selector matches with the
// Tab key, from the element before it.
func focusByKeyboard(e *pageEnv, selector string) {
	e.T.Helper()
	e.must(e.Page.Locator(selector).Focus())
	e.must(e.Page.Keyboard().Press("Shift+Tab"))
	e.must(e.Page.Keyboard().Press("Tab"))
	e.must(e.expect(selector).ToBeFocused())
}

// sectionToggle is the selector of the button in the title of the
// section named key, which folds the section.
func sectionToggle(key string) string {
	return sectionTitle(key) + " > button"
}

// expectSectionFolded checks that the section named key is folded or open:
// its title's class, its button's arrow and state, and whether its body
// shows.
func expectSectionFolded(e *pageEnv, key string, folded bool) {
	e.T.Helper()
	title := e.expect(sectionTitle(key))
	body := e.expect(fmt.Sprintf(`.card:has(> .card-header-row > %s) > .section-body`, sectionTitle(key)))
	arrow := `"▾"`
	if folded {
		arrow = `"▸"`
		e.must(title.ToHaveClass(regexp.MustCompile(`\bcollapsed\b`)))
		e.must(body.ToBeHidden())
	} else {
		e.must(title.Not().ToHaveClass(regexp.MustCompile(`\bcollapsed\b`)))
		e.must(body.ToBeVisible())
	}
	e.must(e.expect(sectionToggle(key)).ToHaveAttribute("aria-expanded", strconv.FormatBool(!folded)))
	if got := pseudoContent(e, sectionToggle(key), "::before"); got != arrow {
		e.T.Errorf("%s::before content = %s, want %s", sectionToggle(key), got, arrow)
	}
}

// expectStored checks localStorage's value of key.
func expectStored(e *pageEnv, key, want string) {
	e.T.Helper()
	if got, ok := e.storage(key); !ok || got != want {
		e.T.Errorf("localStorage %q = %q (set %v), want %q", key, got, ok, want)
	}
}

// runningFlash returns the flash animation of the element selector
// matches, paused: it stays as it is, cancelled or not, however long the
// test takes. The test fails when the element has none.
func runningFlash(e *pageEnv, selector string) playwright.JSHandle {
	e.T.Helper()
	h, err := e.Page.Locator(selector).EvaluateHandle(`(el) => {
		const a = el.getAnimations().find((a) => a.animationName === 'flash')
		if (!a) throw new Error('no flash animation')
		a.pause()
		return a
	}`, nil)
	if err != nil {
		e.T.Fatalf("flash of %s: %v", selector, err)
	}
	return h
}

// flashState returns the play state of a flash animation: idle once it
// was cancelled, paused otherwise.
func flashState(e *pageEnv, flash playwright.JSHandle) string {
	e.T.Helper()
	v, err := flash.Evaluate(`(a) => a.playState`)
	if err != nil {
		e.T.Fatalf("flash state: %v", err)
	}
	s, _ := v.(string)
	return s
}

// awaitFlashCancelled waits until the page cancels flash, which a
// restart does.
func awaitFlashCancelled(e *pageEnv, flash playwright.JSHandle, when string) {
	e.T.Helper()
	if _, err := e.Page.WaitForFunction(`(a) => a.playState === 'idle'`, flash); err != nil {
		e.T.Fatalf("%s: the flash before the new value is %s, want it cancelled to restart: %v",
			when, flashState(e, flash), err)
	}
}

var (
	// toastOpaque and toastClear match the copy toast's style as
	// the page sets it, shown or faded: its computed opacity follows a
	// transition.
	toastOpaque = regexp.MustCompile(`opacity: 1;`)
	toastClear  = regexp.MustCompile(`opacity: 0;`)
)

// expectToast checks that the copy toast shows text.
func expectToast(e *pageEnv, text string) {
	e.T.Helper()
	e.must(e.expect(".copy-toast").ToHaveText(text))
	e.must(e.expect(".copy-toast").ToHaveAttribute("style", toastOpaque))
}

// fadeToast runs the browser's installed clock until the copy toast fades.
func fadeToast(e *pageEnv) {
	e.T.Helper()
	e.runFor(time.Second)
	e.must(e.expect(".copy-toast").ToHaveAttribute("style", toastClear))
}

// storageBlocked makes every use of localStorage throw, as a browser that
// blocks the site's data does.
const storageBlocked = `Object.defineProperty(window, 'localStorage', {
	configurable: true,
	get() {
		throw new DOMException('The operation is insecure.', 'SecurityError')
	},
})`

// sectionTitle is the selector of the title of the section named key.
func sectionTitle(key string) string {
	return fmt.Sprintf(`h2[data-section="%s"]`, key)
}

// ---- Window

// resize sets the viewport to width × height, then has the browser
// render a frame, which fires the window's resize event.
func (e *pageEnv) resize(width, height int) {
	e.T.Helper()
	e.must(e.Page.SetViewportSize(width, height))
	e.renderFrame()
}

// renderFrame has the browser render a frame, which a headless browser
// under an installed clock renders only on demand: resize and scroll events
// wait on it.
func (e *pageEnv) renderFrame() {
	e.T.Helper()
	if _, err := e.Page.Screenshot(); err != nil {
		e.T.Fatalf("render a frame: %v", err)
	}
}
