//go:build page

package web

import (
	"fmt"
	"math"
	"testing"

	"github.com/mxschmitt/playwright-go"
)

// valChartsShape is an SVG element of a chart as drawn: its geometry
// attributes, its computed fill and stroke, and its text.
type valChartsShape struct {
	Tag                     string
	X, Y, Width, Height, RX float64
	X1, X2, Y1, Y2          float64
	CX, CY, R               float64
	Fill, Stroke            string
	FontSize                string
	PointerEvents           string
	Text                    string
	Children                []valChartsShape
}

// valChartsShapes reads the elements selector matches in the svg: their
// geometry, computed colours and text, and their children's.
func (e *pageEnv) valChartsShapes(svg playwright.Locator, selector string) []valChartsShape {
	e.T.Helper()
	v, err := svg.Evaluate(`(svg, selector) => {
		const shape = (el) => {
			const n = (a) => Number(el.getAttribute(a) || 0)
			const cs = getComputedStyle(el)
			return {
				Tag: el.localName,
				X: n('x'), Y: n('y'), Width: n('width'), Height: n('height'), RX: n('rx'),
				X1: n('x1'), X2: n('x2'), Y1: n('y1'), Y2: n('y2'),
				CX: n('cx'), CY: n('cy'), R: n('r'),
				Fill: cs.fill, Stroke: cs.stroke, FontSize: el.getAttribute('font-size') || '',
				PointerEvents: el.getAttribute('pointer-events') || '',
				Text: el.localName === 'text' ? el.textContent : '',
				Children: [...el.children].map(shape),
			}
		}
		return [...svg.querySelectorAll(selector)].map(shape)
	}`, selector)
	if err != nil {
		e.T.Fatalf("read %s of %v: %v", selector, svg, err)
	}
	return decodeValue[[]valChartsShape](e.T, v)
}

// valChartsSameShapes fails the test unless got is want, shape by shape:
// every number within a millionth, and every string want sets.
func valChartsSameShapes(t *testing.T, what string, got, want []valChartsShape) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d shapes, want %d:\n got %+v\nwant %+v", what, len(got), len(want), got, want)
	}
	for i := range want {
		if d := valChartsShapeDiff(got[i], want[i]); d != "" {
			t.Errorf("%s: shape %d: %s\n got %+v\nwant %+v", what, i, d, got[i], want[i])
		}
	}
}

func valChartsShapeDiff(got, want valChartsShape) string {
	numbers := []struct {
		name      string
		got, want float64
	}{
		{"x", got.X, want.X}, {"y", got.Y, want.Y}, {"width", got.Width, want.Width}, {"height", got.Height, want.Height},
		{"rx", got.RX, want.RX}, {"x1", got.X1, want.X1}, {"x2", got.X2, want.X2}, {"y1", got.Y1, want.Y1},
		{"y2", got.Y2, want.Y2}, {"cx", got.CX, want.CX}, {"cy", got.CY, want.CY}, {"r", got.R, want.R},
	}
	for _, n := range numbers {
		if math.Abs(n.got-n.want) > 1e-6 {
			return fmt.Sprintf("%s = %v, want %v", n.name, n.got, n.want)
		}
	}
	strs := []struct {
		name      string
		got, want string
	}{
		{"tag", got.Tag, want.Tag}, {"fill", got.Fill, want.Fill}, {"stroke", got.Stroke, want.Stroke},
		{"font-size", got.FontSize, want.FontSize}, {"pointer-events", got.PointerEvents, want.PointerEvents},
		{"text", got.Text, want.Text},
	}
	for _, s := range strs {
		if s.want != "" && s.got != s.want {
			return fmt.Sprintf("%s = %q, want %q", s.name, s.got, s.want)
		}
	}
	if want.Children == nil {
		return ""
	}
	if len(got.Children) != len(want.Children) {
		return fmt.Sprintf("%d children, want %d", len(got.Children), len(want.Children))
	}
	for i := range want.Children {
		if d := valChartsShapeDiff(got.Children[i], want.Children[i]); d != "" {
			return fmt.Sprintf("child %d: %s", i, d)
		}
	}
	return ""
}
