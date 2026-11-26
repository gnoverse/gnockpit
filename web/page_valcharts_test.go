//go:build page

package web

import (
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// ---- Charts

// TestPageValidatorUptimeChart checks the Uptime chart on a wide screen: a
// bar per UTC day from the validator's first to the report's today, banded
// by its share signed, empty on a day not in the set; a dot on each day an
// outage covers, one however many outages; the month axis; the legend; and
// each kind of day's popover.
func TestPageValidatorUptimeChart(t *testing.T) {
	e := openPage(t, desktop, monthNetwork(t), withURL("/?val="+valBravo.address()))
	e.expectValidatorReport("bravo")
	box := e.chartBox("uptime")
	svg := box.Locator(".val-axis > svg")
	px := e.numberOf(box, "el => el.clientWidth") / valChartsDayCount
	width := valChartsDayCount * px
	e.must(e.Expect.Locator(svg).ToHaveAttribute("height", "59"))
	if got := e.numberOf(svg, `el => Number(el.getAttribute('width'))`); math.Abs(got-width) > 1e-6 {
		t.Errorf("svg width = %v, want %v: %d days %v px wide", got, width, valChartsDayCount, px)
	}

	var bars []valChartsShape
	for i, fill := range valChartsBravoUptime(t) {
		bars = append(bars, valChartsShape{Tag: "rect", X: float64(i)*px + 1, Y: 9, Width: px - 2, Height: 28, RX: 1, Fill: fill})
	}
	valChartsSameShapes(t, "bars", e.valChartsShapes(svg, ":scope > rect"), bars)
	var dots []valChartsShape
	for _, day := range []string{"2026-10-22", "2026-10-23"} {
		i := float64(valChartsDayIndex(t, day))
		dots = append(dots, valChartsShape{Tag: "circle", CX: i*px + px/2, CY: 4, R: 2.5, Fill: fgColor, PointerEvents: "none"})
	}
	valChartsSameShapes(t, "outage dots", e.valChartsShapes(svg, ":scope > circle"), dots)

	// Days narrower than 40 px: a month's name at its first day, and the
	// first day's month at the left, no month starting near it.
	nov1 := float64(valChartsDayIndex(t, "2026-11-01")) * px
	valChartsSameShapes(t, "axis lines", e.valChartsShapes(svg, ":scope > line"), []valChartsShape{
		{Tag: "line", X2: width, Y1: 41, Y2: 41, Stroke: borderColor},
		{Tag: "line", X1: nov1, X2: nov1, Y1: 41, Y2: 45, Stroke: fg2Color},
	})
	valChartsSameShapes(t, "axis labels", e.valChartsShapes(svg, ":scope > text"), []valChartsShape{
		{Tag: "text", X: nov1 + 2, Y: 55, Fill: fg2Color, FontSize: "10", Text: "Nov"},
		{Tag: "text", X: 2, Y: 55, Fill: fg2Color, FontSize: "10", Text: "Oct"},
	})

	e.must(e.Expect.Locator(box.Locator(".val-cap > .unit")).ToHaveText("one bar per UTC day"))
	keys := box.Locator(".val-cap > .keys > span")
	e.must(e.Expect.Locator(keys).ToHaveText([]string{"≥ 99.9 % signed", "95 to 99.9 %", "≤ 95 %", "not in the set", "an outage that day"}))
	for i, c := range []string{greenColor, yellowColor, redColor, borderColor, fgColor} {
		e.must(e.Expect.Locator(keys.Nth(i).Locator(".sw")).ToHaveCSS("background-color", c))
	}
	e.must(e.Expect.Locator(keys.Nth(4).Locator(".sw")).ToHaveClass("sw round"))

	bar := func(day string) playwright.Locator {
		return svg.Locator(":scope > rect").Nth(valChartsDayIndex(t, day))
	}
	for _, tc := range []struct {
		day  string
		want rowsPopover
	}{
		{"2026-10-21", rowsPopover{
			labels:   []string{"signed", "", "absent", "other block", "nil votes"},
			values:   []string{"697 of 720", "96.80 %", "22", "1", "2 (signed)"},
			swatches: []string{yellowColor},
		}},
		{"2026-10-22", rowsPopover{
			labels:   []string{"signed", "", "absent", "other block", "nil votes", "● outage", ""},
			values:   []string{"690 of 720", "95.83 %", "30", "0", "0 (signed)", "60 blocks, 2 h 00 min", "from 23:00 UTC, #2,131"},
			swatches: []string{yellowColor},
		}},
		// The newest outage first; one that began the day before names its
		// day; each counts all its blocks.
		{"2026-10-23", rowsPopover{
			labels: []string{"signed", "", "absent", "other block", "nil votes", "● outage", "", "● outage", ""},
			values: []string{
				"674 of 720", "93.61 %", "46", "0", "0 (signed)",
				"15 blocks, 30 min 00 s", "from 10:00 UTC, #2,461",
				"60 blocks, 2 h 00 min", "from 2026-10-22 23:00 UTC, #2,131",
			},
			swatches: []string{redColor},
		}},
		{"2026-10-24", rowsPopover{labels: []string{"not in the set"}, values: []string{""}, swatches: []string{borderColor}}},
		{"2026-11-18", rowsPopover{
			labels:   []string{"signed", "", "absent", "other block", "nil votes"},
			values:   []string{"299 of 330", "90.60 %", "31", "0", "0 (signed)"},
			swatches: []string{redColor},
		}},
	} {
		tc.want.title = tc.day
		e.point(bar(tc.day))
		e.expectPopover(tc.want)
	}
}

// TestPageValidatorOngoingOutageDotsItsLastDay checks that an outage still
// going on dots the day of the newest block, its last absence, even when
// that block is at midnight, and is listed in that day's popover.
func TestPageValidatorOngoingOutageDotsItsLastDay(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, ongoingOutageNetwork(t), withURL("/?val="+valBravo.address()))
		e.expectValidatorReport("bravo")
		// From 2026-11-15: 2026-11-17 and 2026-11-18 are the third and fourth
		// days, dotted at their middles, over a bar or in a calendar's cell.
		svg := e.chartBox("uptime").Locator("svg").Last()
		var middles, dots [][2]float64
		for _, d := range e.valChartsShapes(svg, ":scope > rect")[2:] {
			cy := 4.0
			if p.device != "" {
				cy = d.Y + d.Height/2
			}
			middles = append(middles, [2]float64{d.X + d.Width/2, cy})
		}
		for _, c := range e.valChartsShapes(svg, ":scope > circle") {
			dots = append(dots, [2]float64{c.CX, c.CY})
		}
		if !slices.EqualFunc(dots, middles, func(a, b [2]float64) bool { return nearPx(a[0], b[0]) && nearPx(a[1], b[1]) }) {
			t.Errorf("outage dots at %v, want at the middles of 2026-11-17 and 2026-11-18, %v", dots, middles)
		}
		e.point(svg.Locator(":scope > rect").Nth(3))
		e.expectPopover(rowsPopover{
			title:  "2026-11-18",
			labels: []string{"signed", "", "absent", "other block", "nil votes", "● outage", ""},
			values: []string{
				"0 of 1", "0.00 %", "1", "0", "0 (signed)", "31 blocks, 1 h 00 min, ongoing", "from 2026-11-17 23:00 UTC, #2,131",
			},
			swatches: []string{redColor},
		})
	})
}

// TestPageValidatorProposalsChart checks the Proposals chart on a wide
// screen: a bar per day banded by its share of proposals missed, empty on
// a day none was expected, no outage dots; the axis; the legend; and each
// kind of day's popover.
func TestPageValidatorProposalsChart(t *testing.T) {
	e := openPage(t, desktop, monthNetwork(t), withURL("/?val="+valBravo.address()))
	e.expectValidatorReport("bravo")
	box := e.chartBox("proposals")
	svg := box.Locator(".val-axis > svg")
	px := e.numberOf(box, "el => el.clientWidth") / valChartsDayCount
	e.must(e.Expect.Locator(svg).ToHaveAttribute("height", "50"))

	fills := make([]string, valChartsDayCount)
	for i := range fills {
		fills[i] = borderColor
	}
	fills[valChartsDayIndex(t, "2026-10-26")] = greenColor  // 0 of 40 missed
	fills[valChartsDayIndex(t, "2026-10-27")] = yellowColor // 1 of 40
	fills[valChartsDayIndex(t, "2026-10-28")] = redColor    // 1 of 20
	var bars []valChartsShape
	for i, fill := range fills {
		bars = append(bars, valChartsShape{Tag: "rect", X: float64(i)*px + 1, Width: px - 2, Height: 28, RX: 1, Fill: fill})
	}
	valChartsSameShapes(t, "bars", e.valChartsShapes(svg, ":scope > rect"), bars)
	e.must(e.Expect.Locator(svg.Locator("circle")).ToHaveCount(0))
	nov1 := float64(valChartsDayIndex(t, "2026-11-01")) * px
	valChartsSameShapes(t, "axis", e.valChartsShapes(svg, ":scope > line, :scope > text"), []valChartsShape{
		{Tag: "line", X2: valChartsDayCount * px, Y1: 32, Y2: 32, Stroke: borderColor},
		{Tag: "line", X1: nov1, X2: nov1, Y1: 32, Y2: 36, Stroke: fg2Color},
		{Tag: "text", X: nov1 + 2, Y: 46, Text: "Nov"},
		{Tag: "text", X: 2, Y: 46, Text: "Oct"},
	})

	e.must(e.Expect.Locator(box.Locator(".val-cap > .unit")).ToHaveText("one bar per UTC day"))
	keys := box.Locator(".val-cap > .keys > span")
	e.must(e.Expect.Locator(keys).ToHaveText([]string{"none missed", "under 5 % missed", "5 % or more", "none expected"}))
	for i, c := range []string{greenColor, yellowColor, redColor, borderColor} {
		e.must(e.Expect.Locator(keys.Nth(i).Locator(".sw")).ToHaveCSS("background-color", c))
	}

	bar := func(day string) playwright.Locator {
		return svg.Locator(":scope > rect").Nth(valChartsDayIndex(t, day))
	}
	for _, tc := range []struct {
		day  string
		want rowsPopover
	}{
		{"2026-10-26", rowsPopover{labels: []string{"missed", ""}, values: []string{"0 of 40", "0.0 %"}, swatches: []string{greenColor}}},
		{"2026-10-27", rowsPopover{labels: []string{"missed", ""}, values: []string{"1 of 40", "2.5 %"}, swatches: []string{yellowColor}}},
		{"2026-10-28", rowsPopover{labels: []string{"missed", ""}, values: []string{"1 of 20", "5.0 %"}, swatches: []string{redColor}}},
		{"2026-10-20", rowsPopover{labels: []string{"expected"}, values: []string{"none"}, swatches: []string{borderColor}}},
		{"2026-10-24", rowsPopover{labels: []string{"expected"}, values: []string{"none"}, swatches: []string{borderColor}}},
	} {
		tc.want.title = tc.day
		e.point(bar(tc.day))
		e.expectPopover(tc.want)
	}
}

// valChartsRecoveryHeight is the height of the bar of a recovery that took
// d: on a log scale up to 24 h, 8 px at least.
func valChartsRecoveryHeight(d time.Duration) float64 {
	s := min(d.Seconds(), 86400)
	return max(math.Log(1+s/60)/math.Log(1+1440)*70, 8)
}

// TestPageValidatorRecoveryChart checks the Recovery chart: a bar per
// restart on its day, side by side on a shared day, its height on a log
// scale of the time to sign again and banded by it, none for a pending one;
// the blue line over the ones the report scores; the axis; the legend; and
// a bar's popover.
func TestPageValidatorRecoveryChart(t *testing.T) {
	e := openPage(t, desktop, monthNetwork(t), withURL("/?val="+valBravo.address()))
	e.expectValidatorReport("bravo")
	box := e.chartBox("recovery")
	svg := box.Locator(".val-axis > svg")
	px := e.numberOf(box, "el => el.clientWidth") / valChartsDayCount
	span := max(px-2, 6)
	e.must(e.Expect.Locator(svg).ToHaveAttribute("height", "120"))

	// A bar of its day's j of n: its share of the day's span.
	bar := func(day string, j, n int, d time.Duration, fill string) valChartsShape {
		h := valChartsRecoveryHeight(d)
		x := float64(valChartsDayIndex(t, day))*px + px/2 - span/2 + float64(j)*span/float64(n)
		return valChartsShape{Tag: "rect", X: x, Y: 94 - h, Width: span / float64(n), Height: h, RX: 1, Fill: fill}
	}
	bars := []valChartsShape{
		bar("2026-10-25", 0, 1, 10*time.Minute, greenColor),
		bar("2026-11-04", 0, 1, 24*time.Hour, redColor),
		bar("2026-11-10", 0, 2, 2*time.Minute, greenColor),
		bar("2026-11-10", 1, 2, 12*time.Minute, yellowColor),
	}
	valChartsSameShapes(t, "bars", e.valChartsShapes(svg, ":scope > rect"), bars)
	nov1 := float64(valChartsDayIndex(t, "2026-11-01")) * px
	valChartsSameShapes(t, "lines", e.valChartsShapes(svg, ":scope > line"), []valChartsShape{
		// The last three, from 2026-11-04 to 2026-11-10's second.
		{Tag: "line", X1: bars[1].X, X2: bars[3].X + bars[3].Width, Y1: 16, Y2: 16, Stroke: blueColor},
		{Tag: "line", X2: valChartsDayCount * px, Y1: 96, Y2: 96, Stroke: borderColor},
		{Tag: "line", X1: nov1, X2: nov1, Y1: 96, Y2: 100, Stroke: fg2Color},
	})
	e.must(e.Expect.Locator(svg.Locator(":scope > line").First()).ToHaveClass("val-scored-line"))
	e.must(e.Expect.Locator(svg.Locator(":scope > line").First()).ToHaveAttribute("stroke-width", "2"))
	e.must(e.Expect.Locator(svg.Locator(":scope > text")).ToHaveText([]string{"Nov", "Oct"}))

	e.must(e.Expect.Locator(box.Locator(".val-cap > .unit")).ToHaveText("one bar per restart, on its day"))
	keys := box.Locator(".val-cap > .keys > span")
	e.must(e.Expect.Locator(keys).ToHaveText([]string{"≤ 10 min", "10 min to 24 h", "≥ 24 h", "its last 3, the ones scored"}))
	for i, c := range []string{greenColor, yellowColor, redColor, blueColor} {
		e.must(e.Expect.Locator(keys.Nth(i).Locator(".sw")).ToHaveCSS("background-color", c))
	}
	e.must(e.Expect.Locator(keys.Nth(3).Locator(".sw")).ToHaveClass("sw line"))

	rects := svg.Locator(":scope > rect")
	for i, want := range []rowsPopover{
		{title: "2026-10-25", values: []string{"10 min 00 s", "#2,880", "no"}, swatches: []string{greenColor}},
		{title: "2026-11-04", values: []string{"24 h 00 min", "#9,360", "yes (its last 3)"}, swatches: []string{redColor}},
		{title: "2026-11-10", values: []string{"2 min 00 s", "#13,859", "yes (its last 3)"}, swatches: []string{greenColor}},
		{title: "2026-11-10", values: []string{"12 min 00 s", "#14,189", "yes (its last 3)"}, swatches: []string{yellowColor}},
	} {
		want.labels = []string{"signed again after", "restart", "scored"}
		e.point(rects.Nth(i))
		e.expectPopover(want)
	}
}

// valChartsDelayFills are the vote delay chart's colours, earliest range
// first.
var valChartsDelayFills = []string{
	"rgb(72, 79, 88)", "rgb(110, 118, 129)", "rgb(46, 160, 67)", "rgb(126, 231, 135)",
	"rgb(210, 153, 34)", "rgb(240, 136, 62)", "rgb(248, 81, 73)",
}

// TestPageValidatorVoteDelayChart checks the Vote delay chart: a bar per day
// with votes, its precommits stacked by delay range to 100 %, the earliest at
// the bottom, no part for an empty range; none on a day out of the set or
// without votes; the axis; the legend with the ranges; and a day's popover:
// its mean, then each range's share from the latest.
func TestPageValidatorVoteDelayChart(t *testing.T) {
	e := openPage(t, desktop, monthNetwork(t), withURL("/?val="+valBravo.address()))
	e.expectValidatorReport("bravo")
	box := e.chartBox("delay")
	svg := box.Locator(".val-axis > svg")
	px := e.numberOf(box, "el => el.clientWidth") / valChartsDayCount
	e.must(e.Expect.Locator(svg).ToHaveAttribute("height", "128"))

	// stack is a day's bar of counts by range.
	stack := func(i int, counts ...int) valChartsShape {
		votes := 0
		for _, n := range counts {
			votes += n
		}
		g := valChartsShape{Tag: "g", Children: []valChartsShape{}}
		y := 104.0
		for j, n := range counts {
			h := float64(n) / float64(votes) * 100
			y -= h
			if h > 0 {
				g.Children = append(g.Children, valChartsShape{
					Tag: "rect", X: float64(i)*px + 1, Y: y, Width: px - 2, Height: h, Fill: valChartsDelayFills[j],
				})
			}
		}
		return g
	}
	var want []valChartsShape
	for i := range valChartsDayCount {
		switch day := valChartsFirstDay.AddDate(0, 0, i).Format(time.DateOnly); day {
		case "2026-10-20":
			want = append(want, stack(i, 1, 180, 360, 108, 36, 18, 17))
		case "2026-10-21":
			want = append(want, stack(i, 0, 698, 0, 0, 0, 0, 0))
		case "2026-10-24", "2026-11-04": // not in the set; absent all day
		default:
			want = append(want, stack(i, 0, 0, 1, 0, 0, 0, 0))
		}
	}
	valChartsSameShapes(t, "bars", e.valChartsShapes(svg, ":scope > g"), want)
	e.must(e.Expect.Locator(svg.Locator(":scope > text")).ToHaveText([]string{"Nov", "Oct"}))
	e.must(e.Expect.Locator(svg.Locator(":scope > line").First()).ToHaveAttribute("y1", "106"))

	ranges := []string{"< −1 s", "−1 to −0.25 s", "±0.25 s", "0.25 to 1 s", "1 to 2.5 s", "2.5 to 5 s", "> 5 s"}
	e.must(e.Expect.Locator(box.Locator(".val-cap > .unit")).ToHaveText("one bar per UTC day, its precommits by delay from the commit time"))
	keys := box.Locator(".val-cap > .keys > span")
	e.must(e.Expect.Locator(keys).ToHaveText(ranges))
	for i, c := range valChartsDelayFills {
		e.must(e.Expect.Locator(keys.Nth(i).Locator(".sw")).ToHaveCSS("background-color", c))
	}

	latestFirst := slices.Clone(ranges)
	slices.Reverse(latestFirst)
	swatches := slices.Clone(valChartsDelayFills)
	slices.Reverse(swatches)
	groups := svg.Locator(":scope > g")
	for i, tc := range []struct {
		day    string
		values []string
	}{
		{"2026-10-20", []string{"+0.25 s", "2 %", "3 %", "5 %", "15 %", "50 %", "25 %", "< 1 %"}},
		{"2026-10-21", []string{"−0.30 s", "0 %", "0 %", "0 %", "0 %", "0 %", "100 %", "0 %"}},
	} {
		e.point(groups.Nth(i))
		e.expectPopover(rowsPopover{
			title: tc.day, labels: append([]string{"mean"}, latestFirst...), values: tc.values, swatches: swatches,
		})
	}
}

// ---- Phone calendars

// valChartsCell is the cell of a calendar's day i, its weeks from Monday,
// first the first day's weekday from Monday.
func valChartsCell(i, first int, fill string) valChartsShape {
	col, row := float64((i+first)/7), float64((i+first)%7)
	return valChartsShape{Tag: "rect", X: col * 16, Y: 16 + row*16, Width: 13, Height: 13, RX: 2, Fill: fill}
}

// TestPageValidatorCalendars checks the Uptime and Proposals calendars of a
// phone: a cell per day, weeks as columns from Monday, filled and dotted as
// the bars are, the weekdays' names beside them, a month's name over the
// week of its first day; Recovery and Vote delay stay bar charts.
func TestPageValidatorCalendars(t *testing.T) {
	e := openPage(t, phone, monthNetwork(t), withURL("/?val="+valBravo.address()))
	e.expectValidatorReport("bravo")
	for _, key := range []string{"uptime", "proposals"} {
		box := e.chartBox(key)
		e.must(e.Expect.Locator(box.Locator(":scope > .val-cal")).ToHaveCount(1))
		e.must(e.Expect.Locator(box.Locator(".val-axis")).ToHaveCount(0))
		e.must(e.Expect.Locator(box.Locator(".val-cap > .unit")).ToHaveText("one cell per UTC day"))
		labels := box.Locator(".val-cal > svg")
		e.must(e.Expect.Locator(labels).ToHaveAttribute("width", "30"))
		e.must(e.Expect.Locator(labels).ToHaveAttribute("height", "128"))
		valChartsSameShapes(t, key+" weekdays", e.valChartsShapes(labels, "text"), []valChartsShape{
			{Tag: "text", Y: 26, Text: "Mon", Fill: fg2Color}, {Tag: "text", Y: 58, Text: "Wed"}, {Tag: "text", Y: 90, Text: "Fri"},
		})
	}
	for _, key := range []string{"recovery", "delay"} {
		e.must(e.Expect.Locator(e.chartBox(key).Locator(":scope > .val-axis > svg")).ToHaveCount(1))
		e.must(e.Expect.Locator(e.chartBox(key).Locator(".val-cal")).ToHaveCount(0))
	}

	// 2026-10-20 is a Tuesday: five weeks; November's name over its first
	// day's week, the second, and no name over the first.
	const first = 1
	cal := e.chartBox("uptime").Locator(".val-cal-scroll > svg")
	e.must(e.Expect.Locator(cal).ToHaveAttribute("width", "80"))
	e.must(e.Expect.Locator(cal).ToHaveAttribute("height", "128"))
	var cells []valChartsShape
	for i, fill := range valChartsBravoUptime(t) {
		cells = append(cells, valChartsCell(i, first, fill))
	}
	valChartsSameShapes(t, "uptime cells", e.valChartsShapes(cal, ":scope > rect"), cells)
	var dots []valChartsShape
	for _, day := range []string{"2026-10-22", "2026-10-23"} {
		c := cells[valChartsDayIndex(t, day)]
		dots = append(dots, valChartsShape{Tag: "circle", CX: c.X + 6.5, CY: c.Y + 6.5, R: 2.5, Fill: fgColor, PointerEvents: "none"})
	}
	valChartsSameShapes(t, "outage dots", e.valChartsShapes(cal, ":scope > circle"), dots)
	valChartsSameShapes(t, "months", e.valChartsShapes(cal, ":scope > text"), []valChartsShape{
		{Tag: "text", X: 16, Y: 10, Text: "Nov", Fill: fg2Color, FontSize: "10"},
	})

	proposals := e.chartBox("proposals").Locator(".val-cal-scroll > svg")
	var fills []string
	for i := range valChartsDayCount {
		switch valChartsFirstDay.AddDate(0, 0, i).Format(time.DateOnly) {
		case "2026-10-26":
			fills = append(fills, greenColor)
		case "2026-10-27":
			fills = append(fills, yellowColor)
		case "2026-10-28":
			fills = append(fills, redColor)
		default:
			fills = append(fills, borderColor)
		}
	}
	cells = nil
	for i, fill := range fills {
		cells = append(cells, valChartsCell(i, first, fill))
	}
	valChartsSameShapes(t, "proposals cells", e.valChartsShapes(proposals, ":scope > rect"), cells)
	e.must(e.Expect.Locator(proposals.Locator("circle")).ToHaveCount(0))

	e.point(cal.Locator(":scope > rect").Nth(valChartsDayIndex(t, "2026-10-22")))
	e.expectPopover(rowsPopover{
		title:    "2026-10-22",
		labels:   []string{"signed", "", "absent", "other block", "nil votes", "● outage", ""},
		values:   []string{"690 of 720", "95.83 %", "30", "0", "0 (signed)", "60 blocks, 2 h 00 min", "from 23:00 UTC, #2,131"},
		swatches: []string{yellowColor},
	})
	e.point(proposals.Locator(":scope > rect").Nth(valChartsDayIndex(t, "2026-10-27")))
	e.expectPopover(rowsPopover{
		title: "2026-10-27", labels: []string{"missed", ""}, values: []string{"1 of 40", "2.5 %"}, swatches: []string{yellowColor},
	})
}

// TestPageValidatorCalendarNamesItsFirstMonth checks that a calendar with no
// month starting in its first two weeks names the first day's month over its
// first week.
func TestPageValidatorCalendarNamesItsFirstMonth(t *testing.T) {
	e := openPage(t, phone, healthyNetwork(), withURL("/?val="+valBravo.address()))
	e.expectValidatorReport("bravo")
	// From Sunday 2026-11-15 to 2026-11-18: two weeks.
	cal := e.chartBox("uptime").Locator(".val-cal-scroll > svg")
	e.must(e.Expect.Locator(cal).ToHaveAttribute("width", "32"))
	valChartsSameShapes(t, "months", e.valChartsShapes(cal, ":scope > text"), []valChartsShape{
		{Tag: "text", X: 0, Y: 10, Text: "Nov"},
	})
	valChartsSameShapes(t, "cells", e.valChartsShapes(cal, ":scope > rect"), []valChartsShape{
		valChartsCell(0, 6, greenColor), valChartsCell(1, 6, greenColor),
		valChartsCell(2, 6, greenColor), valChartsCell(3, 6, greenColor),
	})
}

// ---- Day axes and ranges

// TestPageValidatorAxesWithDates checks the day axis of days 40 px wide or
// more: each day's date, the first day's and each first's with their
// month, a tick at each first; every bar chart as wide as the days.
func TestPageValidatorAxesWithDates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sc     func() *scenario
		days   int
		labels []string
		ticks  []int // the days a month starts on
	}{
		{"a month start", func() *scenario { return valChartsSince(utc2026(time.October, 28, 0, 0)) }, 22, []string{
			"28 Oct", "29", "30", "31", "1 Nov", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13", "14", "15", "16", "17", "18",
		}, []int{4}},
		{"no month start", healthyNetwork, 4, []string{"15 Nov", "16", "17", "18"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := openPage(t, desktop, tc.sc(), withURL("/?val="+valBravo.address()))
			e.expectValidatorReport("bravo")
			box := e.chartBox("uptime")
			px := e.numberOf(box, "el => el.clientWidth") / float64(tc.days)
			svg := box.Locator(".val-axis > svg")
			var labels []valChartsShape
			for i, l := range tc.labels {
				labels = append(labels, valChartsShape{Tag: "text", X: float64(i)*px + 2, Y: 55, Text: l, FontSize: "10", Fill: fg2Color})
			}
			valChartsSameShapes(t, "labels", e.valChartsShapes(svg, ":scope > text"), labels)
			lines := []valChartsShape{{Tag: "line", X2: float64(tc.days) * px, Y1: 41, Y2: 41}}
			for _, i := range tc.ticks {
				lines = append(lines, valChartsShape{Tag: "line", X1: float64(i) * px, X2: float64(i) * px, Y1: 41, Y2: 45})
			}
			valChartsSameShapes(t, "lines", e.valChartsShapes(svg, ":scope > line"), lines)
			svgs := e.Page.Locator("#val-body .val-axis > svg")
			e.must(e.Expect.Locator(svgs).ToHaveCount(4))
			for i := range 4 {
				if got := e.numberOf(svgs.Nth(i), `el => Number(el.getAttribute('width'))`); math.Abs(got-float64(tc.days)*px) > 1e-6 {
					t.Errorf("chart %d is %v px wide, want %v", i, got, float64(tc.days)*px)
				}
			}
		})
	}
}

// valChartsLongAgo is the first block of a history long enough to scroll:
// 202 days to the report's today.
var valChartsLongAgo = utc2026(time.May, 1, 0, 0)

// valChartsScrolls reads the scroll position of each of the validator
// page's day axes and calendars, in the page's order, and how far each
// scrolls.
func (e *pageEnv) valChartsScrolls() (at, end []float64) {
	e.T.Helper()
	v, err := e.Page.Locator("#val-body").Evaluate(`body => [...body.querySelectorAll('.val-axis, .val-cal-scroll')]
		.map(s => [s.scrollLeft, s.scrollWidth - s.clientWidth])`, nil)
	if err != nil {
		e.T.Fatalf("read the axes' scroll: %v", err)
	}
	for _, p := range decodeValue[[][2]float64](e.T, v) {
		at, end = append(at, p[0]), append(end, p[1])
	}
	return at, end
}

// TestPageValidatorAxesScrollTogether checks a history wider than the
// screen: days 8 px wide, month names alone at their firsts, every axis and
// calendar opened on the newest day, and a scrolled axis taking the other
// axes along, never the calendars.
func TestPageValidatorAxesScrollTogether(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, valChartsSince(valChartsLongAgo), withURL("/?val="+valBravo.address()))
		e.expectValidatorReport("bravo")
		axes := e.Page.Locator("#val-body .val-axis")
		const days = 202
		for i := range 2 {
			e.must(e.Expect.Locator(axes.Nth(i).Locator(":scope > svg")).ToHaveAttribute("width", fmt.Sprint(days*8)))
		}
		// May 1 is the first day: no other name at its left.
		e.must(e.Expect.Locator(axes.Last().Locator(":scope > svg > text")).ToHaveText([]string{"May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov"}))
		at, end := e.valChartsScrolls()
		for i := range at {
			if !nearPx(at[i], end[i]) || end[i] <= 0 {
				t.Errorf("axis %d opened at %v of %v, want its end", i, at[i], end[i])
			}
		}

		// The recovery axis scrolled, by a wheel to the middle of its
		// history on a pointer device, to its first day on a phone: every
		// other axis follows, the calendars stay.
		recovery := e.chartBox("recovery").Locator(".val-axis")
		want := 0.0
		if !p.touch() {
			const back = 300
			e.must(recovery.Hover())
			e.must(e.Page.Mouse().Wheel(-back, 0))
			want = end[len(end)-1] - back
		} else {
			e.must(recovery.Locator("svg > text").First().ScrollIntoViewIfNeeded())
		}
		e.must(e.Expect.Locator(recovery).ToHaveJSProperty("scrollLeft", want))
		n, err := axes.Count()
		e.must(err)
		for i := range n {
			e.must(e.Expect.Locator(axes.Nth(i)).ToHaveJSProperty("scrollLeft", want))
		}
		cals := e.Page.Locator("#val-body .val-cal-scroll")
		n, err = cals.Count()
		e.must(err)
		for i := range n {
			e.must(e.Expect.Locator(cals.Nth(i)).ToHaveJSProperty("scrollLeft", end[i]))
		}
	})
}

// TestPageValidatorChartsFollowTheWidth checks a resize: 150 ms after the
// last width change the charts are drawn again, every axis back on the
// newest day; a height change alone leaves them; at 640 px and under,
// Uptime and Proposals turn calendars, and bars again above.
func TestPageValidatorChartsFollowTheWidth(t *testing.T) {
	e := openPage(t, desktop, valChartsSince(valChartsLongAgo), withURL("/?val="+valBravo.address()), withInstalledClock())
	e.expectValidatorReport("bravo")
	axes := e.Page.Locator("#val-body .val-axis")
	atStart := func() {
		t.Helper()
		for i := range 4 {
			e.must(e.Expect.Locator(axes.Nth(i)).ToHaveJSProperty("scrollLeft", 0))
		}
	}
	atEnd := func() {
		t.Helper()
		at, end := e.valChartsScrolls()
		for i := range at {
			if !nearPx(at[i], end[i]) {
				t.Fatalf("axis %d at %v, want its end %v", i, at[i], end[i])
			}
		}
	}
	// Scrolled to the oldest day: a redraw shows, the axes back at the end.
	e.must(axes.Nth(2).Hover())
	e.must(e.Page.Mouse().Wheel(-5000, 0))
	e.renderFrame()
	atStart()

	e.resize(1280, 700)
	e.runFor(time.Second)
	atStart()

	e.resize(1200, 700)
	e.runFor(149 * time.Millisecond)
	atStart()
	e.runFor(time.Millisecond)
	atEnd()

	e.resize(640, 700)
	e.runFor(150 * time.Millisecond)
	for _, key := range []string{"uptime", "proposals"} {
		e.must(e.Expect.Locator(e.chartBox(key).Locator(":scope > .val-cal")).ToHaveCount(1))
	}
	e.must(e.Expect.Locator(axes).ToHaveCount(2))

	e.resize(641, 700)
	e.runFor(150 * time.Millisecond)
	e.must(e.Expect.Locator(e.Page.Locator("#val-body .val-cal")).ToHaveCount(0))
	e.must(e.Expect.Locator(axes).ToHaveCount(4))
}

// TestPageValidatorWithoutHistory checks a validator the block history does
// not hold: each chart and the Timeline say so, without a legend.
func TestPageValidatorWithoutHistory(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, monthNetwork(t), withURL("/?val="+valDelta.address()))
		e.must(e.expect("#val-body [data-section=timeline]").ToBeVisible())
		boxes := e.Page.Locator("#val-body [data-chart]")
		e.must(e.Expect.Locator(boxes).ToHaveCount(4))
		for i := range 4 {
			e.must(e.Expect.Locator(boxes.Nth(i).Locator(":scope > *")).ToHaveText([]string{"nothing in the block history yet"}))
			e.must(e.Expect.Locator(boxes.Nth(i).Locator(":scope > .val-msg")).ToHaveCount(1))
		}
		timeline := e.Page.Locator("#val-body [data-section=timeline]")
		e.must(e.Expect.Locator(timeline.Locator(":scope > .val-msg")).ToHaveText("nothing in the block history yet"))
		e.must(e.Expect.Locator(timeline.Locator(".vt-row")).ToHaveCount(0))
	})
}

// valChartsScoredSpan reads the recovery chart's scored line's ends and its
// bars' left and right edges.
func (e *pageEnv) valChartsScoredSpan() (line [2]float64, bars [][2]float64) {
	e.T.Helper()
	svg := e.chartBox("recovery").Locator(".val-axis > svg")
	for _, b := range e.valChartsShapes(svg, ":scope > rect") {
		bars = append(bars, [2]float64{b.X, b.X + b.Width})
	}
	lines := e.valChartsShapes(svg, ":scope > line.val-scored-line")
	if len(lines) != 1 {
		e.T.Fatalf("%d scored lines, want 1", len(lines))
	}
	return [2]float64{lines[0].X1, lines[0].X2}, bars
}

// TestPageValidatorChartsReadTheReport checks that the charts take their
// days and their scored recoveries from the report alone: whatever the
// browser's clock and timezone, the axis ends on the report's today, rows
// after it are left out with their recoveries, and the scored line spans the
// recoveries the report scores.
func TestPageValidatorChartsReadTheReport(t *testing.T) {
	for _, tc := range []struct {
		name     string
		now      time.Time // the server's clock
		opts     []pageOption
		days     int
		scored   [2]int // the first and last bar scored
		lastDay  string
		barCount int
	}{
		{"browser ahead, east of UTC", pageNow, []pageOption{withBrowserOffset(3 * 24 * time.Hour), withTimezone("Pacific/Kiritimati")}, 30, [2]int{1, 3}, "2026-11-18", 4},
		{"browser behind, west of UTC", pageNow, []pageOption{withBrowserOffset(-10 * 24 * time.Hour), withTimezone("America/Los_Angeles")}, 30, [2]int{1, 3}, "2026-11-18", 4},
		// The history holds days after the server's today, 2026-11-09:
		// the restarts of 2026-11-10 are not drawn, and the report scores
		// the two before.
		{"history after today", utc2026(time.November, 9, 12, 0), nil, 21, [2]int{0, 1}, "2026-11-09", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := monthNetwork(t)
			sc.now = tc.now
			e := openPage(t, desktop, sc, append(tc.opts, withURL("/?val="+valBravo.address()))...)
			e.expectValidatorReport("bravo")
			uptime := e.chartBox("uptime").Locator(".val-axis > svg > rect")
			e.must(e.Expect.Locator(uptime).ToHaveCount(tc.days))
			e.point(uptime.First())
			e.must(e.expect("#popover > .t").ToHaveText("2026-10-20"))
			e.point(uptime.Last())
			e.must(e.expect("#popover > .t").ToHaveText(tc.lastDay))

			line, bars := e.valChartsScoredSpan()
			if len(bars) != tc.barCount {
				t.Fatalf("%d recovery bars, want %d", len(bars), tc.barCount)
			}
			if want := [2]float64{bars[tc.scored[0]][0], bars[tc.scored[1]][1]}; !nearPx(line[0], want[0]) || !nearPx(line[1], want[1]) {
				t.Errorf("scored line from %v to %v, want %v to %v: bars %d to %d", line[0], line[1], want[0], want[1], tc.scored[0], tc.scored[1])
			}
		})
	}
}

// ---- Bands

// TestPageValidatorBandsReadTheValues checks that a share is written never
// better than it is and banded on its value, as the ranking colours it:
// 99.895 % signed is written 99.89 % and is orange, 4.96 % of proposals
// missed is written 5.0 % and is orange, in a stats line and on a day.
func TestPageValidatorBandsReadTheValues(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, valChartsEdges(t), withURL("/?val="+valAlpha.address()))
		e.must(e.expect("#val-body [data-section=timeline]").ToBeVisible())
		e.expectStats("alpha", "uptime", statWindow{name: "last 30 days", stats: []statItem{
			{label: "signed", value: "99.89 %", band: "warn"}, {label: "outages", value: "0", band: "ok"}, {label: "nil votes", value: "0", band: ""}, {label: "other block", value: "0", band: ""},
		}})
		// From 2026-10-28: 2026-11-17 is the 21st day, 959 of 960 signed.
		day := e.chartBox("uptime").Locator("svg > rect").Nth(20)
		e.must(e.Expect.Locator(day).ToHaveCSS("fill", yellowColor))
		e.point(day)
		e.expectPopover(rowsPopover{
			title:    "2026-11-17",
			labels:   []string{"signed", "", "absent", "other block", "nil votes"},
			values:   []string{"959 of 960", "99.89 %", "1", "0", "0 (signed)"},
			swatches: []string{yellowColor},
		})

		e.navigate("/?val=" + valBravo.address())
		e.must(e.expect("#val-body [data-section=timeline]").ToBeVisible())
		e.expectStats("bravo", "proposals",
			statWindow{name: "last 30 days", stats: []statItem{{label: "missed", value: "31 of 625 (5.0 %)", band: "warn"}}},
			statWindow{name: "last 24 h", stats: []statItem{{label: "missed", value: "none expected", band: ""}}},
		)
		day = e.chartBox("proposals").Locator("svg > rect").Nth(13) // 2026-11-10
		e.must(e.Expect.Locator(day).ToHaveCSS("fill", yellowColor))
		e.point(day)
		e.expectPopover(rowsPopover{
			title: "2026-11-10", labels: []string{"missed", ""}, values: []string{"31 of 625", "5.0 %"}, swatches: []string{yellowColor},
		})
	})
}

// TestPageValidatorAMissIsNeverNoneMissed checks that a missed proposal
// whose share is under 0.05 % reads 0.1 %, rounded up, and is banded as a
// miss, never as none missed: 1 of 2,001, in a stats line and on its day.
func TestPageValidatorAMissIsNeverNoneMissed(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, valChartsOneMiss(t), withURL("/?val="+valBravo.address()))
		e.must(e.expect("#val-body [data-section=timeline]").ToBeVisible())
		e.expectStats("bravo", "proposals",
			statWindow{name: "last 30 days", stats: []statItem{{label: "missed", value: "1 of 2,001 (0.1 %)", band: "warn"}}},
			statWindow{name: "last 24 h", stats: []statItem{{label: "missed", value: "none expected", band: ""}}},
		)
		day := e.chartBox("proposals").Locator("svg > rect").First() // 2026-11-12
		e.must(e.Expect.Locator(day).ToHaveCSS("fill", yellowColor))
		e.point(day)
		e.expectPopover(rowsPopover{
			title: "2026-11-12", labels: []string{"missed", ""}, values: []string{"1 of 2,001", "0.1 %"}, swatches: []string{yellowColor},
		})
	})
}

// TestPageValidatorRecoveryBandsReadTheDurations checks that a recovery is
// written never faster than it was, rounded up, and banded on its
// duration, as the ranking colours it: 600.4 s is written 10 min 01 s and
// is orange, past the 10 minutes of full marks; 86,399.6 s is written
// 24 h 00 min and is orange, short of the 24 hours that score 0; in the
// stats line, the chart and the Timeline.
func TestPageValidatorRecoveryBandsReadTheDurations(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, valChartsRecoveryEdges(t), noNavigate())
		for _, tc := range []struct {
			name       string
			val        string
			shown      string
			band, fill string
		}{
			{"bravo", valBravo.address(), "10 min 01 s", "warn", yellowColor},
			{"charlie", valCharlie.address(), "24 h 00 min", "warn", yellowColor},
		} {
			e.navigate("/?val=" + tc.val)
			e.must(e.expect("#val-body [data-section=timeline]").ToBeVisible())
			e.expectStats(tc.name, "recovery", statWindow{name: "last 180 days", stats: []statItem{
				{label: "median of its last 3", value: tc.shown, band: tc.band}, {label: "samples", value: "1", band: ""}, {label: "slowest", value: tc.shown, band: ""},
			}})
			bars := e.chartBox("recovery").Locator(".val-axis > svg > rect")
			e.must(e.Expect.Locator(bars).ToHaveCount(1))
			e.must(e.Expect.Locator(bars).ToHaveCSS("fill", tc.fill))
			recovered := e.Page.Locator("#val-body [data-section=timeline] > .vt-row > .w").First()
			e.must(e.Expect.Locator(recovered).ToHaveText("recovered in " + tc.shown + " restart at #181"))
			e.must(e.Expect.Locator(recovered.Locator("b")).ToHaveClass(tc.band))
		}
	})
}

// ---- Keyboard

// TestPageValidatorChartsFromTheKeyboard checks that each chart is one tab
// stop, on its newest day or restart, ringed when focused: the keyboard's
// focus shows its popover, ← and → move it to the day before and after,
// staying at the ends, Home and End to the first and the newest, an arrow
// with a modifier nowhere; Tab goes on to the next chart and from the last
// back to the page's first control; Escape hides the popover and leaves the
// page open.
func TestPageValidatorChartsFromTheKeyboard(t *testing.T) {
	onProfiles(t, []profile{desktop, desktopWebKit}, func(t *testing.T, p profile) {
		e := openPage(t, p, monthNetwork(t), withURL("/?val="+valBravo.address()))
		e.expectValidatorReport("bravo")
		press := func(keys ...string) {
			t.Helper()
			for _, key := range keys {
				e.must(e.Page.Keyboard().Press(key))
			}
		}
		focusedWith := func(l playwright.Locator, title string) {
			t.Helper()
			e.must(e.Expect.Locator(l).ToBeFocused())
			e.must(e.expect("#popover").ToBeVisible())
			e.must(e.expect("#popover > .t").ToHaveText(title))
		}
		// Each section's ⓘ is a stop before its chart.
		focusByKeyboard(e, sectionInfo("uptime"))
		uptime := e.chartBox("uptime").Locator(".val-axis > svg > rect")
		press("Tab")
		focusedWith(uptime.Last(), "2026-11-18")
		e.must(e.Expect.Locator(uptime.Last()).ToHaveCSS("stroke", fgColor))
		// The ring, astride a bar's edge, is not cut where a bar touches its
		// chart's top: the charts draw past their edges, inside a 1 px
		// margin of their scrolling box.
		e.must(e.Expect.Locator(uptime.Last().Locator("xpath=..")).ToHaveCSS("overflow", "visible"))
		e.must(e.Expect.Locator(e.chartBox("proposals").Locator(".val-axis")).ToHaveCSS("padding-top", "1px"))
		// Each item is named by its day, a restart by its height too.
		e.must(e.expect("#val-body [data-chart] [data-pop-rows]:not([aria-label])").ToHaveCount(0))
		e.must(e.Expect.Locator(uptime.Last()).ToHaveAttribute("aria-label", "2026-11-18"))
		e.must(e.Expect.Locator(e.chartBox("recovery").Locator(".val-axis > svg > rect").Last()).ToHaveAttribute("aria-label", "2026-11-10, restart #14,189"))
		e.must(e.Expect.Locator(e.chartBox("delay").Locator(".val-axis > svg > g").Last()).ToHaveAttribute("aria-label", "2026-11-18"))
		press("ArrowRight")
		focusedWith(uptime.Last(), "2026-11-18")
		press("ArrowLeft")
		focusedWith(uptime.Nth(28), "2026-11-17")
		for range valChartsDayCount {
			press("ArrowLeft")
		}
		focusedWith(uptime.First(), "2026-10-20")
		press("End")
		focusedWith(uptime.Last(), "2026-11-18")
		press("Home")
		focusedWith(uptime.First(), "2026-10-20")
		// An arrow with a modifier is the browser's, not a day's move.
		for _, key := range []string{"Alt+ArrowRight", "Control+ArrowRight", "Meta+ArrowRight"} {
			press(key)
			focusedWith(uptime.First(), "2026-10-20")
		}
		press("ArrowRight")
		focusedWith(uptime.Nth(1), "2026-10-21")

		press("Tab", "Tab")
		focusedWith(e.chartBox("proposals").Locator(".val-axis > svg > rect").Last(), "2026-11-18")
		// Back, the chart's stop is the day it was left on.
		press("Shift+Tab", "Shift+Tab")
		focusedWith(uptime.Nth(1), "2026-10-21")
		press("Tab", "Tab", "Tab", "Tab")
		recovery := e.chartBox("recovery").Locator(".val-axis > svg > rect")
		focusedWith(recovery.Last(), "2026-11-10")
		e.must(e.Expect.Locator(e.Page.Locator("#popover td.v").First()).ToHaveText("12 min 00 s"))
		press("ArrowLeft")
		focusedWith(recovery.Nth(2), "2026-11-10")
		e.must(e.Expect.Locator(e.Page.Locator("#popover td.v").First()).ToHaveText("2 min 00 s"))
		delay := e.chartBox("delay").Locator(".val-axis > svg > g").Last()
		press("Tab", "Tab")
		focusedWith(delay, "2026-11-18")
		// The Timeline's ⓘ, then back to the page's first control.
		press("Tab", "Tab")
		e.must(e.expect("#val-link-btn").ToBeFocused())
		e.must(e.expect("#popover").ToBeHidden())

		press("Shift+Tab", "Shift+Tab")
		focusedWith(delay, "2026-11-18")
		press("Escape")
		e.must(e.expect("#popover").ToBeHidden())
		// The page stays open, its day keeping the focus.
		e.must(e.Expect.Locator(delay).ToBeFocused())
		e.must(e.expect("#val-modal").ToHaveClass(modalOpenClass))
		press("Escape")
		e.expectValidatorPageClosed()
	})
}

// valChartsNarrowDesktop is a pointer and keyboard device as narrow as a
// phone: the page draws calendars.
var valChartsNarrowDesktop = profile{name: "narrow desktop", browser: chromium, viewport: playwright.Size{Width: 640, Height: 800}}

// TestPageValidatorCalendarsFromTheKeyboard checks a calendar as a grid: ↑
// and ↓ move to the day before and after, in the week's column, ← and → to
// the same day of the week before and after, staying at the ends.
func TestPageValidatorCalendarsFromTheKeyboard(t *testing.T) {
	e := openPage(t, valChartsNarrowDesktop, monthNetwork(t), withURL("/?val="+valBravo.address()))
	e.expectValidatorReport("bravo")
	cells := e.chartBox("uptime").Locator(".val-cal-scroll > svg > rect")
	// The ring of a cell in the first week is not cut at the left.
	e.must(e.Expect.Locator(e.chartBox("uptime").Locator(".val-cal-scroll")).ToHaveCSS("padding-left", "1px"))
	e.must(e.Expect.Locator(e.chartBox("uptime").Locator(".val-cal-scroll > svg")).ToHaveCSS("overflow", "visible"))
	focusByKeyboard(e, sectionInfo("uptime"))
	for _, step := range []struct {
		key string
		day string
	}{
		{"Tab", "2026-11-18"},
		{"ArrowUp", "2026-11-17"},
		{"ArrowLeft", "2026-11-10"},
		{"ArrowDown", "2026-11-11"},
		{"ArrowRight", "2026-11-18"},
		{"ArrowRight", "2026-11-18"},
		{"ArrowDown", "2026-11-18"},
		{"Home", "2026-10-20"},
		{"ArrowLeft", "2026-10-20"},
		{"ArrowUp", "2026-10-20"},
	} {
		e.must(e.Page.Keyboard().Press(step.key))
		e.must(e.Expect.Locator(cells.Nth(valChartsDayIndex(t, step.day))).ToBeFocused())
		e.must(e.expect("#popover > .t").ToHaveText(step.day))
	}
}

// TestPageValidatorChartsKeepTheKeyboardsDay checks that a redraw for a
// width change, as a zoom makes, keeps the focus on the day it was on, its
// popover shown: from bars to bars, to calendars and back.
func TestPageValidatorChartsKeepTheKeyboardsDay(t *testing.T) {
	onProfiles(t, []profile{desktop, desktopWebKit}, func(t *testing.T, p profile) {
		e := openPage(t, p, monthNetwork(t), withURL("/?val="+valBravo.address()), withInstalledClock())
		e.expectValidatorReport("bravo")
		focusByKeyboard(e, sectionInfo("uptime"))
		for _, key := range []string{"Tab", "ArrowLeft", "ArrowLeft"} {
			e.must(e.Page.Keyboard().Press(key))
		}
		// Bars or a calendar, the chart's day cells are its svgs' rects.
		day := e.chartBox("uptime").Locator("svg > rect").Nth(27)
		e.must(e.Expect.Locator(day).ToBeFocused())
		e.must(e.expect("#popover > .t").ToHaveText("2026-11-16"))

		for _, step := range []struct {
			width     int
			calendars int
		}{{1200, 0}, {640, 2}, {1200, 0}} {
			drawn, err := day.ElementHandle()
			e.must(err)
			e.resize(step.width, 800)
			e.runFor(150 * time.Millisecond)
			if connected, err := drawn.Evaluate("el => el.isConnected"); err != nil || connected != false {
				t.Fatalf("at %d px, the day drawn before is connected: %v (%v), want the charts drawn again", step.width, connected, err)
			}
			e.must(e.expect("#val-body .val-cal").ToHaveCount(step.calendars))
			e.must(e.Expect.Locator(day).ToBeFocused())
			e.must(e.Expect.Locator(day).ToHaveAttribute("tabindex", "0"))
			e.must(e.expect("#popover").ToBeVisible())
			e.must(e.expect("#popover > .t").ToHaveText("2026-11-16"))
		}
	})
}

// TestPageValidatorChartScrollsToTheKeyboardsDay checks that moving through
// a chart wider than the page scrolls the axes together to the day, its
// popover under it.
func TestPageValidatorChartScrollsToTheKeyboardsDay(t *testing.T) {
	onProfiles(t, []profile{desktop, desktopWebKit}, func(t *testing.T, p profile) {
		e := openPage(t, p, valChartsSince(valChartsLongAgo), withURL("/?val="+valBravo.address()))
		e.expectValidatorReport("bravo")
		uptime := e.chartBox("uptime").Locator(".val-axis > svg > rect")
		focusByKeyboard(e, sectionInfo("uptime"))
		e.must(e.Page.Keyboard().Press("Tab"))
		e.must(e.Expect.Locator(uptime.Last()).ToBeFocused())
		// From the newest day, 2026-11-18, to 2026-06-01, out of view.
		const back = 170
		for range back {
			e.must(e.Page.Keyboard().Press("ArrowLeft"))
		}
		day := uptime.Nth(201 - back)
		e.must(e.Expect.Locator(day).ToBeFocused())
		e.must(e.expect("#popover > .t").ToHaveText("2026-06-01"))
		at, end := e.valChartsScrolls()
		for i := range at {
			if at[i] >= end[i] || !nearPx(at[i], at[0]) {
				t.Errorf("axes at %v of %v, want every one scrolled back, together", at, end)
				break
			}
		}
		place := e.popoverPlacement(day)
		if !nearPx(place.top, place.targetBottom+4) || !nearPx(place.left, place.targetLeft) {
			t.Errorf("popover at (%v, %v), want under its day at (%v, %v)", place.left, place.top, place.targetLeft, place.targetBottom+4)
		}
	})
}
