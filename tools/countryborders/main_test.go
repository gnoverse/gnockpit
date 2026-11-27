package main

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gnoverse/gnockpit/location"
)

// fixture is a tiny Natural Earth file, its features in the shape of the
// real one's:
//   - France, whose ISO_A2 is -99 and ISO_A2_EH FR, one ring to round to the
//     grid, two of its points one once rounded, its closing point dropped;
//   - Norway, a multipolygon: a square with a hole, a ring that collapses to
//     one point at the grid, and a ring that collapses to a line;
//   - Somaliland, -99 in both codes: left out;
//   - three Australian features, merged in their order, the last, Christmas
//     I., keyed by its ISO_A2_EH, AU, before its ISO_A2, CX;
//   - Taiwan, whose ISO_A2 CN-TW is no code a declaration may name, keyed by
//     its ISO_A2_EH;
//   - a feature whose ISO_A2_EH is -99, keyed by its ISO_A2.
const fixture = `{"type": "FeatureCollection", "features": [
{"type": "Feature", "properties": {"NAME": "France", "ISO_A2": "-99", "ISO_A2_EH": "FR"},
 "geometry": {"type": "Polygon", "coordinates": [[[2.04, 48.86], [2.06, 48.84], [2.31, 48.86], [2.33, 48.86], [2.3, 49.1], [2.04, 48.86]]]}},
{"type": "Feature", "properties": {"NAME": "Norway", "ISO_A2": "-99", "ISO_A2_EH": "NO"},
 "geometry": {"type": "MultiPolygon", "coordinates": [
  [[[10, 60], [11, 60], [11, 61], [10, 61], [10, 60]], [[10.4, 60.4], [10.4, 60.6], [10.6, 60.6], [10.6, 60.4], [10.4, 60.4]]],
  [[[5.01, 62.01], [5.02, 62.02], [5.03, 62.01], [5.01, 62.01]]],
  [[[6, 62], [6.5, 62], [7, 62], [6, 62]]]
 ]}},
{"type": "Feature", "properties": {"NAME": "Somaliland", "ISO_A2": "-99", "ISO_A2_EH": "-99"},
 "geometry": {"type": "Polygon", "coordinates": [[[44, 9], [45, 9], [45, 10], [44, 9]]]}},
{"type": "Feature", "properties": {"NAME": "Australia", "ISO_A2": "AU", "ISO_A2_EH": "AU"},
 "geometry": {"type": "Polygon", "coordinates": [[[130, -25], [135, -25], [135, -20], [130, -25]]]}},
{"type": "Feature", "properties": {"NAME": "Ashmore and Cartier Is.", "ISO_A2": "-99", "ISO_A2_EH": "AU"},
 "geometry": {"type": "Polygon", "coordinates": [[[123, -12.2], [123.2, -12.2], [123.2, -12], [123, -12.2]]]}},
{"type": "Feature", "properties": {"NAME": "Christmas I.", "ISO_A2": "CX", "ISO_A2_EH": "AU"},
 "geometry": {"type": "Polygon", "coordinates": [[[105.6, -10.5], [105.7, -10.5], [105.7, -10.4], [105.6, -10.5]]]}},
{"type": "Feature", "properties": {"NAME": "ISO_A2 alone", "ISO_A2": "XK", "ISO_A2_EH": "-99"},
 "geometry": {"type": "Polygon", "coordinates": [[[20, 42], [21, 42], [21, 43], [20, 42]]]}},
{"type": "Feature", "properties": {"NAME": "Taiwan", "ISO_A2": "CN-TW", "ISO_A2_EH": "TW"},
 "geometry": {"type": "Polygon", "coordinates": [[[120, 22], [122, 22], [122, 25], [120, 22]]]}}
]}`

// fixtureBorders reads fixture as the program reads Natural Earth's file.
func fixtureBorders(t *testing.T) reading {
	t.Helper()
	b, err := readBorders(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("readBorders(fixture) = %v", err)
	}
	return b
}

func TestBordersKeyCountriesByTheirCode(t *testing.T) {
	b := fixtureBorders(t)
	got := slices.Sorted(maps.Keys(b.countries))
	if want := []string{"AU", "FR", "NO", "TW", "XK"}; !slices.Equal(got, want) {
		t.Errorf("codes = %q, want %q", got, want)
	}
	for _, code := range got {
		if !location.ValidCountry(code) {
			t.Errorf("code %q is no code a declaration may name (location.CountryCodes)", code)
		}
	}
	if want := []string{"Somaliland"}; !slices.Equal(b.leftOut, want) {
		t.Errorf("left out %q, want %q", b.leftOut, want)
	}
}

func TestBordersRoundToTheGrid(t *testing.T) {
	b := fixtureBorders(t)
	// (2.04, 48.86) and (2.06, 48.84) are (20, 489) and (21, 488); (2.31,
	// 48.86) and (2.33, 48.86) are both (23, 489), written once; the
	// closing point is the first.
	want := [][]int{{20, 489, 21, 488, 23, 489, 23, 491}}
	if got := b.countries["FR"]; !reflect.DeepEqual(got, want) {
		t.Errorf("FR = %v, want %v", got, want)
	}
}

func TestBordersDropCollapsedRings(t *testing.T) {
	b := fixtureBorders(t)
	// The square and its hole stay; the ring within 0.05° of one point
	// and the ring along a parallel enclose nothing at the grid.
	want := [][]int{
		{100, 600, 110, 600, 110, 610, 100, 610},
		{104, 604, 104, 606, 106, 606, 106, 604},
	}
	if got := b.countries["NO"]; !reflect.DeepEqual(got, want) {
		t.Errorf("NO = %v, want %v", got, want)
	}
	if b.collapsed != 2 {
		t.Errorf("collapsed rings = %d, want 2", b.collapsed)
	}
}

// neighbours are two countries sharing a border that bends at (1.1, 0.3),
// 0.0096° off the line through its ends: within half a grid step, so that
// either country's ring simplified alone would drop it.
const neighbours = `{"type": "FeatureCollection", "features": [
{"type": "Feature", "properties": {"NAME": "Belgium", "ISO_A2": "BE", "ISO_A2_EH": "BE"},
 "geometry": {"type": "Polygon", "coordinates": [[[0, 0], [1, 0], [1.1, 0.3], [1.3, 1], [0, 1], [0, 0]]]}},
{"type": "Feature", "properties": {"NAME": "Netherlands", "ISO_A2": "NL", "ISO_A2_EH": "NL"},
 "geometry": {"type": "Polygon", "coordinates": [[[1, 0], [2, 0], [2, 1], [1.3, 1], [1.1, 0.3], [1, 0]]]}}
]}`

func TestBordersKeepTheBordersCountriesShare(t *testing.T) {
	b, err := readBorders(strings.NewReader(neighbours))
	if err != nil {
		t.Fatalf("readBorders(neighbours) = %v", err)
	}
	want := countryBorders{
		"BE": {{10, 0, 11, 3, 13, 10, 0, 10, 0, 0}},
		"NL": {{10, 0, 20, 0, 20, 10, 13, 10, 11, 3}},
	}
	if !reflect.DeepEqual(b.countries, want) {
		t.Errorf("borders = %v, want %v", b.countries, want)
	}
}

func TestOnGridDropsPointsAlongAStraightEdge(t *testing.T) {
	// A 0.2° square drawn through the middle of its south and east edges,
	// and through its first corner again before closing.
	ring := [][2]float64{{0, 0}, {0.1, 0}, {0.2, 0}, {0.2, 0.1}, {0.2, 0.2}, {0, 0.2}, {0, 0.1}, {0, 0}}
	want := []int{0, 0, 2, 0, 2, 2, 0, 2}
	if got := onGrid(rounded(ring), nil); !reflect.DeepEqual(got, want) {
		t.Errorf("onGrid(%v) = %v, want %v", ring, got, want)
	}
}

func TestOnGridSimplifiesWithinHalfAGridStep(t *testing.T) {
	// (0.3, 0.1) lies 0.0096° off the edge from (0, 0) to (1, 0.3), within
	// half a grid step: it goes. (0.5, 0) lies 0.144° off it: it stays.
	tests := []struct {
		name string
		ring [][2]float64
		want []int
	}{
		{
			name: "a point within half a step of its edge",
			ring: [][2]float64{{0, 0}, {0.3, 0.1}, {1, 0.3}, {1, 1}, {0, 1}, {0, 0}},
			want: []int{0, 0, 10, 3, 10, 10, 0, 10},
		},
		{
			name: "a point further than half a step from its edge",
			ring: [][2]float64{{0, 0}, {0.5, 0}, {1, 0.3}, {1, 1}, {0, 1}, {0, 0}},
			want: []int{0, 0, 5, 0, 10, 3, 10, 10, 0, 10},
		},
		{
			// A spit rounded to a spike of no width: its tip lies on the
			// line through its neighbours, not between them, and stays.
			name: "the tip of a spike",
			ring: [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0.5, 1}, {0.5, 1.7}, {0.5, 1}, {0, 1}, {0, 0}},
			want: []int{0, 0, 10, 0, 10, 10, 5, 10, 5, 17, 5, 10, 0, 10},
		},
		{
			// (-2.9, -1) lies 0.032° off the line through (3, 1) and the
			// first point, but beyond the first point, 3° from that edge.
			name: "a point beyond the end of its edge",
			ring: [][2]float64{{0, 0}, {1.5, 0.8}, {3, 1}, {-2.9, -1}, {0, 0}},
			want: []int{0, 0, 15, 8, 30, 10, -29, -10},
		},
		{
			// Every point lies within half a step of the edges that would
			// replace it, leaving a line: the ring, which encloses area,
			// stays as rounded.
			name: "a sliver that simplifies to a line",
			ring: [][2]float64{{0, 0}, {1.4, 0.5}, {3, 1}, {1.6, 0.5}, {0, 0}},
			want: []int{0, 0, 14, 5, 30, 10, 16, 5},
		},
		{
			// A ring of Kiribati's that rounds to a grid cell drawn twice
			// over, enclosing nothing.
			name: "a ring enclosing nothing once rounded",
			ring: [][2]float64{{-159.3, 3.9}, {-159.3, 3.8}, {-159.4, 3.8}, {-159.3, 3.8}, {-159.3, 3.9}, {-159.4, 3.9}, {-159.4, 3.8}, {-159.4, 3.9}, {-159.3, 3.9}},
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := onGrid(rounded(tc.ring), nil); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("onGrid(%v) = %v, want %v", tc.ring, got, tc.want)
			}
		})
	}
}

func TestBordersMergeACountrysFeatures(t *testing.T) {
	b := fixtureBorders(t)
	want := [][]int{
		{1300, -250, 1350, -250, 1350, -200},
		{1230, -122, 1232, -122, 1232, -120},
		{1056, -105, 1057, -105, 1057, -104},
	}
	if got := b.countries["AU"]; !reflect.DeepEqual(got, want) {
		t.Errorf("AU = %v, want %v", got, want)
	}
}

func TestBordersRefuseAnUnknownGeometry(t *testing.T) {
	const line = `{"type": "FeatureCollection", "features": [
{"type": "Feature", "properties": {"NAME": "France", "ISO_A2": "FR", "ISO_A2_EH": "FR"},
 "geometry": {"type": "LineString", "coordinates": [[2, 48], [3, 49]]}}]}`
	if _, err := readBorders(strings.NewReader(line)); err == nil || !strings.Contains(err.Error(), "LineString") {
		t.Errorf("readBorders of a LineString = %v, want an error naming it", err)
	}
}

// TestEncodeIsDeterministic checks the asset's bytes: compact JSON, its keys
// sorted, its numbers integers, ending in a newline; the same for the same
// borders, whatever the order a map iterates in.
func TestEncodeIsDeterministic(t *testing.T) {
	b := fixtureBorders(t)
	first, err := encode(b.countries)
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		again, err := encode(b.countries)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(again, first) {
			t.Fatalf("encode gave\n%s\nthen\n%s", first, again)
		}
	}
	want := `{"AU":[[1300,-250,1350,-250,1350,-200],[1230,-122,1232,-122,1232,-120],[1056,-105,1057,-105,1057,-104]],` +
		`"FR":[[20,489,21,488,23,489,23,491]],` +
		`"NO":[[100,600,110,600,110,610,100,610],[104,604,104,606,106,606,106,604]],` +
		`"TW":[[1200,220,1220,220,1220,250]],` +
		`"XK":[[200,420,210,420,210,430]]}` + "\n"
	if string(first) != want {
		t.Errorf("encode =\n%s\nwant\n%s", first, want)
	}
}

// TestRunReadsALocalCopy checks the program on a local copy (-in): it writes
// the asset of the copy's borders, the same bytes on a second run.
func TestRunReadsALocalCopy(t *testing.T) {
	dir := t.TempDir()
	in, out := filepath.Join(dir, "countries.geojson"), filepath.Join(dir, "data", "country-borders.json")
	if err := os.WriteFile(in, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := run(&log, "http://127.0.0.1:0/unused", in, out); err != nil {
		t.Fatalf("run = %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want, err := encode(fixtureBorders(t).countries)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the asset =\n%s\nwant\n%s", got, want)
	}
	if !strings.Contains(log.String(), "Somaliland") {
		t.Errorf("the program's log %q names no feature left out, want Somaliland", log.String())
	}
	if err := run(&log, "http://127.0.0.1:0/unused", in, out); err != nil {
		t.Fatalf("run again = %v", err)
	}
	again, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, got) {
		t.Error("a second run wrote other bytes")
	}
}

// TestRunDownloads checks the program without a local copy: it downloads
// the file, and writes the asset of its borders.
func TestRunDownloads(t *testing.T) {
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.WriteString(w, fixture); err != nil {
			t.Errorf("serve the fixture: %v", err)
		}
	}))
	t.Cleanup(src.Close)
	out := filepath.Join(t.TempDir(), "country-borders.json")
	if err := run(io.Discard, src.URL, "", out); err != nil {
		t.Fatalf("run = %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want, err := encode(fixtureBorders(t).countries)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the asset =\n%s\nwant\n%s", got, want)
	}
}

// TestRunRefusesAFailedDownload checks that an error answer writes nothing
// and says its status.
func TestRunRefusesAFailedDownload(t *testing.T) {
	src := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(src.Close)
	out := filepath.Join(t.TempDir(), "country-borders.json")
	err := run(io.Discard, src.URL, "", out)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("run of a 404 = %v, want an error naming it", err)
	}
	if _, statErr := os.Stat(out); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("the asset after a failed download: %v, want none", statErr)
	}
}
