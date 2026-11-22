//go:build page

package web

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// ---- Formats

// The page's formats of times, all in UTC.
func etaDay(t time.Time) string { return t.UTC().Format("Mon 2 Jan") }

func etaHMS(t time.Time) string { return t.UTC().Format("15:04:05") }

func etaHM(t time.Time) string { return t.UTC().Format("15:04") }

// etaOnDay is clock, a time of t, after t's day when that is not pageNow's
// day in UTC.
func etaOnDay(t time.Time, clock string) string {
	if etaDay(t) == etaDay(pageNow) {
		return clock
	}
	return etaDay(t) + " " + clock
}

// etaTime parses a time of an /api/eta answer.
func etaTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}

// etaNF is n with thousands separators, as the page writes heights.
func etaNF(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// etaCountdown is sec as the count shows it: [Nd ]HH:MM:SS.
func etaCountdown(sec int64) string {
	sec = max(0, sec)
	s := fmt.Sprintf("%02d:%02d:%02d", sec%86400/3600, sec%3600/60, sec%60)
	if d := sec / 86400; d > 0 {
		s = fmt.Sprintf("%dd %s", d, s)
	}
	return s
}

// etaInShort is sec to the nearest minute: Nd Hh, Hh Mm or Mm.
func etaInShort(sec int64) string {
	m := (sec + 30) / 60
	d, h := m/1440, m%1440/60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m%60)
	}
	return fmt.Sprintf("%dm", m)
}

// etaDrift is a scenario's offset from the ETA: +12s, −5m 07s, +2h 05m, or
// ±0s.
func etaDrift(sec int64) string {
	sign := "±"
	switch {
	case sec > 0:
		sign = "+"
	case sec < 0:
		sign = "−"
	}
	a := max(sec, -sec)
	switch {
	case a < 60:
		return fmt.Sprintf("%s%ds", sign, a)
	case a < 3600:
		return fmt.Sprintf("%s%dm %02ds", sign, a/60, a%60)
	}
	return fmt.Sprintf("%s%dh %02dm", sign, a/3600, a%3600/60)
}

// etaWhen is a time in UTC, then on the clock of a viewer in loc unless loc
// is UTC there.
func etaWhen(t time.Time, loc *time.Location) string {
	s := etaDay(t) + " " + etaHMS(t) + " UTC"
	l := t.In(loc)
	if _, off := l.Zone(); off == 0 {
		return s
	}
	local := l.Format("15:04")
	if l.Weekday() != t.UTC().Weekday() {
		local += " " + l.Format("Mon")
	}
	return s + " (" + local + " your time)"
}

// etaRangeDays is a time range, with its days when it spans two.
func etaRangeDays(from, to time.Time) string {
	if etaDay(from) == etaDay(to) {
		return etaHM(from) + "–" + etaHM(to)
	}
	return etaDay(from) + " " + etaHM(from) + "–" + etaDay(to) + " " + etaHM(to)
}

// etaPaceOf names the look-back spans of the scenarios.
var etaPaceOf = map[string]string{
	"last_hour":   "last hour",
	"last_24h":    "last 24 h",
	"last_7d":     "last 7 days",
	"last_30d":    "last 30 days",
	"all_history": "all history",
}

// ---- Card expectations

// etaCardSpec is how a card shows an answer.
type etaCardSpec struct {
	kind    string         // "halt" or "link"
	label   string         // the link's label
	loc     *time.Location // the viewer's timezone; nil for UTC
	elapsed int64          // seconds since the answer came
	stale   string         // the error of a later poll, while the answer stays
}

// etaCardView is what a countdown card shows.
type etaCardView struct {
	count, target, inline string
	keys, values          []string
	chips                 []etaChip
}

// etaChip is a scenario chip: its pace, its ETA, and its offset from the
// ETA with the offset's colour.
type etaChip struct{ k, v, d, color string }

// row adds a row of the card's key-value list.
func (v *etaCardView) row(k, val string) {
	v.keys = append(v.keys, k)
	v.values = append(v.values, val)
}

// etaCard is what a card shows for rep as spec says.
func etaCard(t *testing.T, rep etaReply, spec etaCardSpec) etaCardView {
	t.Helper()
	if rep.Error != "" {
		t.Fatalf("answer: %s", rep.Error)
	}
	loc := spec.loc
	if loc == nil {
		loc = time.UTC
	}
	head := "#" + etaNF(rep.Target.Height)
	switch {
	case spec.kind == "halt":
		head = "halt " + head
		if v := rep.Halt.MinVersion; v != "" {
			head += " · min " + v
		}
	case spec.label != "":
		head += " · " + spec.label
	}
	var v etaCardView
	at := etaTime(t, rep.ETA)
	reached := rep.ReachedAt != ""
	if reached {
		v.count, v.target = "reached", head
		v.inline = "reached · " + head + " · " + etaDay(at) + " " + etaHMS(at) + " UTC"
		v.row("reached", etaWhen(at, loc))
	} else {
		left := *rep.SecondsLeft - spec.elapsed
		v.count, v.target = "due", head+" · ETA passed, waiting for the block"
		if left > 0 {
			blocks := " blocks"
			if rep.Target.BlocksLeft == 1 {
				blocks = " block"
			}
			v.count = etaCountdown(left)
			v.target = "until " + head + " (" + etaNF(rep.Target.BlocksLeft) + blocks + " to go)"
		}
		v.inline = v.count + " · " + head + " · " + etaDay(at) + " " + etaHM(at) + " UTC"
		v.row("eta", etaWhen(at, loc))
		likely := "pending: the block-time history is still too short"
		if r80, r95 := rep.Range80, rep.Range95; r80 != nil {
			likely = etaRangeDays(etaTime(t, r80.From), etaTime(t, r80.To)) + " UTC (80%) · " +
				etaRangeDays(etaTime(t, r95.From), etaTime(t, r95.To)) + " (95%)"
		}
		v.row("likely", likely)
	}
	if o := rep.Original; o != nil {
		oeta, asOf := etaTime(t, o.ETA), etaTime(t, o.AsOf)
		seen := "estimated "
		if spec.kind == "halt" {
			seen = "first seen "
		}
		v.row("original", etaDay(oeta)+" "+etaHMS(oeta)+" UTC ("+seen+etaDay(asOf)+" "+etaHM(asOf)+")")
		drift := "on time"
		if d := o.DriftSeconds; d <= -60 || d >= 60 {
			word := " later"
			if d < 0 {
				word = " earlier"
			}
			drift = etaInShort(max(d, -d)) + word + " than the original"
		}
		v.row("drift", drift)
	}
	if !reached {
		v.row("pace", fmt.Sprintf("%.3f s/block (last %s blocks)", rep.Pace.SecondsPerBlock, etaNF(rep.Pace.OverBlocks)))
		if h := rep.History; !h.Complete {
			loading := "still loading"
			if h.From != "" {
				loading += ", back to " + etaDay(etaTime(t, h.From))
			}
			v.row("history", loading)
		}
		v.chips = etaChips(t, rep)
	}
	var warns []string
	if rep.Warning != "" {
		warns = append(warns, rep.Warning)
	}
	if spec.stale != "" {
		warns = append(warns, spec.stale+"; showing the last answer")
	}
	if len(warns) > 0 {
		v.row("warning", strings.Join(warns, " · "))
		v.inline += " ⚠"
	}
	return v
}

// etaChips are the scenario chips of rep: the current pace first, then one
// per look-back span.
func etaChips(t *testing.T, rep etaReply) []etaChip {
	t.Helper()
	at := etaTime(t, rep.ETA)
	chips := []etaChip{{k: fmt.Sprintf("current pace · %.3f s", rep.Pace.SecondsPerBlock), v: etaOnDay(at, etaHMS(at))}}
	for _, s := range rep.Scenarios {
		st := etaTime(t, s.ETA)
		dd := int64(st.Sub(at).Seconds())
		color := "blue"
		switch {
		case dd > -60 && dd < 60:
			color = "fg2"
		case dd > 0:
			color = "yellow"
		}
		name, ok := etaPaceOf[s.PaceOf]
		if !ok {
			t.Fatalf("scenario pace_of %q has no name", s.PaceOf)
		}
		chips = append(chips, etaChip{k: fmt.Sprintf("%s · %.3f s", name, s.SecondsPerBlock), v: etaOnDay(st, etaHMS(st)), d: etaDrift(dd), color: color})
	}
	return chips
}

// etaAssertCard checks that the card shows v.
func (e *pageEnv) etaAssertCard(card string, v etaCardView) {
	e.T.Helper()
	e.must(e.expect(card).ToBeVisible())
	e.must(e.expect(card + " .eta-count").ToHaveText(v.count))
	e.must(e.expect(card + " .eta-target").ToHaveText(v.target))
	e.must(e.expect(card + " .eta-inline").ToHaveText(v.inline))
	e.must(e.expect(card + " .kv .k").ToHaveText(v.keys))
	e.must(e.expect(card + " .kv .v").ToHaveText(v.values))
	chips := card + " .eta-scen .s"
	e.must(e.expect(chips).ToHaveCount(len(v.chips)))
	for i, c := range v.chips {
		chip := e.Page.Locator(chips).Nth(i)
		e.must(e.Expect.Locator(chip.Locator(".k")).ToHaveText(c.k))
		e.must(e.Expect.Locator(chip.Locator(".v")).ToHaveText(c.v))
		d := e.Expect.Locator(chip.Locator(".d"))
		if c.d == "" {
			e.must(d.ToHaveCount(0))
			continue
		}
		e.must(d.ToHaveText(c.d))
		e.must(d.ToHaveAttribute("style", "color: var(--"+c.color+")"))
	}
}

// etaYOffset is an offset label of the chart's left axis.
var etaYOffset = regexp.MustCompile(`^[+−]\d+m$`)

// etaAssertChart checks the chart the card draws for rep: the 80% band, a cone
// to a pending ETA with a range, the zero line named after the original or
// the ETA, the now line, and the time axis from an hour before now (or the
// trail's start) to the ETA, or to the block once reached.
func (e *pageEnv) etaAssertChart(card string, rep etaReply) {
	e.T.Helper()
	svg := card + " .eta-chart svg"
	e.must(e.expect(card + " .eta-chart-title").ToHaveText("ETA over time · cone = 80% range"))
	for _, el := range []string{"polyline.line", "polygon.band", "line.zero", "line.axis", "line.nowline", `circle[r="4"]`} {
		e.must(e.expect(svg + " " + el).ToHaveCount(1))
	}
	reached := rep.ReachedAt != ""
	cone := 1
	if reached || rep.Range80 == nil {
		cone = 0
	}
	e.must(e.expect(svg + " polygon.cone").ToHaveCount(cone))
	zero := "ETA"
	if rep.Original != nil {
		zero = "orig"
	}
	e.must(e.expect(svg + " text.orig").ToHaveText(zero))

	end := etaTime(e.T, rep.ETA)
	now, nowWord := etaTime(e.T, rep.Now.BlockTime), "now"
	if reached {
		now, nowWord = end, "reached"
	}
	x0 := now.Add(-time.Hour)
	if len(rep.Trail) > 0 {
		if first := etaTime(e.T, rep.Trail[0].At); first.Before(x0) {
			x0 = first
		}
	}
	texts, err := e.Page.Locator(svg + " text").AllTextContents()
	if err != nil {
		e.T.Fatalf("read the chart's labels: %v", err)
	}
	n := len(texts)
	endLabel := "ETA " + etaOnDay(end, etaHM(end))
	if reached {
		endLabel = "reached " + etaOnDay(end, etaHM(end))
	}
	if n < 4 || texts[0] != nowWord || texts[n-2] != etaDay(x0)+" "+etaHM(x0) || texts[n-1] != endLabel {
		e.T.Errorf("chart labels = %q, want %q first, then the offsets around %q, then %q and %q",
			texts, nowWord, zero, etaDay(x0)+" "+etaHM(x0), endLabel)
	}
	for _, l := range texts[1 : n-2] {
		if l != zero && !etaYOffset.MatchString(l) {
			e.T.Errorf("chart offset label %q, want %q or +Nm / −Nm", l, zero)
		}
	}
	height := "170px"
	if e.narrow() {
		height = "130px"
	}
	e.must(e.expect(svg).ToHaveCSS("height", height))

	// The now label goes left of its line within 50 px of the right edge.
	w, err := e.Page.Locator(svg).Evaluate(`el => el.getBoundingClientRect().width`, nil)
	if err != nil {
		e.T.Fatalf("read the chart's width: %v", err)
	}
	width, ok := w.(float64)
	if !ok {
		width = float64(w.(int))
	}
	const left, right = 40, 8
	x1 := end
	if soon := now.Add(time.Minute); soon.After(x1) {
		x1 = soon
	}
	xNow := left + float64(now.Sub(x0))/float64(x1.Sub(x0))*(width-left-right)
	nowLabel := e.Expect.Locator(e.Page.Locator(svg + " text").First())
	if xNow > width-right-50 {
		e.must(nowLabel.ToHaveAttribute("text-anchor", "end"))
	} else {
		e.must(nowLabel.Not().ToHaveAttribute("text-anchor", "end"))
	}
}

// etaAssertBelow checks that the element selector matches sits 4 px below
// anchor, as the page places a popover under its target.
func (e *pageEnv) etaAssertBelow(selector string, anchor playwright.Locator) {
	e.T.Helper()
	pop, err := e.Page.Locator(selector).BoundingBox()
	if err != nil {
		e.T.Fatalf("read the box of %s: %v", selector, err)
	}
	box, err := anchor.BoundingBox()
	if err != nil {
		e.T.Fatalf("read the anchor's box: %v", err)
	}
	if got, want := pop.Y, box.Y+box.Height+4; math.Abs(got-want) > 1 {
		e.T.Errorf("%s top = %.1f, want %.1f: 4 px below its anchor", selector, got, want)
	}
}

// ---- Card controls

// etaBar is the compact card's range bar for rep, px wide: the styles of
// its 95% and 80% bands, the original's diamond (empty without one) and the
// ETA's dot, and its labels.
type etaBar struct {
	b95, b80, orig, now string
	labels              []string
}

// etaBarOf places rep's ranges, original and ETA on a bar px wide: a range
// end closer to the ETA than its label's room goes unlabelled.
func etaBarOf(t *testing.T, rep etaReply, px float64) etaBar {
	t.Helper()
	ms := func(s string) float64 { return float64(etaTime(t, s).UnixMilli()) }
	at := ms(rep.ETA)
	r80 := [2]float64{ms(rep.Range80.From), ms(rep.Range80.To)}
	r95 := [2]float64{ms(rep.Range95.From), ms(rep.Range95.To)}
	orig := at
	if rep.Original != nil {
		orig = ms(rep.Original.ETA)
	}
	lo := math.Min(r95[0], math.Min(orig, at))
	hi := math.Max(r95[1], math.Max(orig, at))
	span := math.Max(hi-lo, 60000)
	a, b := lo-span*0.08, hi+span*0.08
	pct := func(v float64) string { return strconv.FormatFloat(v/(b-a)*100, 'f', 2, 64) + "%" }
	x := func(v float64) string { return pct(v - a) }
	bar := etaBar{
		b95: "left:" + x(r95[0]) + ";width:" + pct(r95[1]-r95[0]),
		b80: "left:" + x(r80[0]) + ";width:" + pct(r80[1]-r80[0]),
		now: "left:" + x(at),
	}
	if rep.Original != nil {
		bar.orig = "left:" + x(orig)
	}
	tm := func(v float64) time.Time { return time.UnixMilli(int64(v)) }
	if math.Abs(r95[0]-at)/(b-a)*px >= 44 {
		bar.labels = append(bar.labels, etaHM(tm(r95[0])))
	}
	bar.labels = append(bar.labels, etaHMS(tm(at)))
	if math.Abs(r95[1]-at)/(b-a)*px >= 44 {
		bar.labels = append(bar.labels, etaHM(tm(r95[1])))
	}
	return bar
}

// etaAssertBar checks the card's range bar shows rep at the bar's width, and
// returns the bar it expected.
func (e *pageEnv) etaAssertBar(card string, rep etaReply) etaBar {
	e.T.Helper()
	rng := card + " .eta-range"
	w, err := e.Page.Locator(rng).Evaluate(`el => el.clientWidth`, nil)
	if err != nil {
		e.T.Fatalf("read the bar's width: %v", err)
	}
	px, ok := w.(int)
	if !ok {
		e.T.Fatalf("bar width: got %T, want an int", w)
	}
	bar := etaBarOf(e.T, rep, float64(px))
	at := etaTime(e.T, rep.ETA)
	e.must(e.expect(rng + " .track").ToHaveCount(1))
	e.must(e.expect(rng+" .b95").ToHaveAttribute("style", bar.b95))
	e.must(e.expect(rng+" .b80").ToHaveAttribute("style", bar.b80))
	e.must(e.expect(rng+" .now").ToHaveAttribute("style", bar.now))
	e.must(e.expect(rng+" .now").ToHaveAttribute("data-popover", "ETA "+etaHMS(at)+" UTC"))
	legend := []string{"80% likely", "95%", "● ETA", "◆ original"}
	if rep.Original == nil {
		e.must(e.expect(rng + " .orig").ToHaveCount(0))
		e.must(e.expect(card + " .eta-legend .orig").ToBeHidden())
	} else {
		o := etaTime(e.T, rep.Original.ETA)
		e.must(e.expect(rng+" .orig").ToHaveAttribute("style", bar.orig))
		e.must(e.expect(rng+" .orig").ToHaveAttribute("data-popover", "original ETA "+etaHMS(o)+" UTC"))
		e.must(e.expect(card + " .eta-legend .orig").ToBeVisible())
	}
	e.must(e.expect(card + " .eta-legend > span").ToHaveText(legend))
	e.must(e.expect(rng + " .lbl").ToHaveText(bar.labels))
	return bar
}

// etaCollapseKey is where the page keeps its collapsed sections: one map
// for wide screens, one for narrow ones.
func (e *pageEnv) etaCollapseKey() string {
	if e.narrow() {
		return "gnockpit-collapsed-mobile"
	}
	return "gnockpit-collapsed"
}

// ---- Planner

// What the empty planner says, in height and in time mode.
const (
	etaPlanHeightEmpty = "Enter a block height to see when it is expected."
	etaPlanTimeEmpty   = "Enter a UTC date and time to see which block it reaches."
)

// etaPlanAfresh types input in the planner once emptied, when it says
// empty: the answer to an earlier input cannot pass for this one's.
func (e *pageEnv) etaPlanAfresh(input, empty string) {
	e.T.Helper()
	e.plan("")
	e.must(e.expect("#eta-result").ToHaveText(empty))
	e.plan(input)
}

// etaPlanMode switches the planner to mode, "height" or "time".
func (e *pageEnv) etaPlanMode(mode string) {
	e.T.Helper()
	e.must(e.Page.Locator(`.eta-seg button[data-mode="` + mode + `"]`).Click())
}

// etaPlanned is what the planner shows for rep in height mode: when the
// height is expected and in how long (under a minute: <1m), or that the ETA
// passed, with its ranges, their days when they span two, or the margin
// pending, and the warning.
func etaPlanned(t *testing.T, rep etaReply) (big, all string) {
	t.Helper()
	if rep.ReachedAt != "" {
		at := etaTime(t, rep.ReachedAt)
		big = "Reached " + etaDay(at) + " " + etaHMS(at) + " UTC"
		return big, big
	}
	at := etaTime(t, rep.ETA)
	big = etaDay(at) + " " + etaHMS(at) + " UTC"
	all = big + "ETA passed, waiting for the block"
	switch left := *rep.SecondsLeft; {
	case left >= 60:
		all = big + "in " + etaInShort(left)
	case left > 0:
		all = big + "in <1m"
	}
	if r80, r95 := rep.Range80, rep.Range95; r80 != nil {
		all += " · 80% " + etaRangeDays(etaTime(t, r80.From), etaTime(t, r80.To)) +
			" · 95% " + etaRangeDays(etaTime(t, r95.From), etaTime(t, r95.To))
	} else {
		all += " · margin pending: the block-time history is still too short"
	}
	return big, all + rep.Warning
}

// etaPlannedHeight is what the planner shows for rep in time mode: the
// height expected, and its ranges, with the warning.
func etaPlannedHeight(t *testing.T, rep etaReply) (big, all string) {
	t.Helper()
	est := rep.Estimate
	big = "≈ #" + etaNF(est.Height)
	all = big + "margin pending: the block-time history is still too short"
	if r80, r95 := est.Range80, est.Range95; r80 != nil {
		all = fmt.Sprintf("%s80%% #%s–#%s · 95%% #%s–#%s", big, etaNF(r80.From), etaNF(r80.To), etaNF(r95.From), etaNF(r95.To))
	}
	return big, all + rep.Warning
}
