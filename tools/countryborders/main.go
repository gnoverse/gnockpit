// Command countryborders writes web/data/country-borders.json, the country
// borders the validator page's location form reads to find the country a
// point of its map lies in.
//
// The source is Natural Earth's 1:50m Admin 0 – Countries, release v5.1.2,
// which is in the public domain:
// https://raw.githubusercontent.com/nvkelso/natural-earth-vector/v5.1.2/geojson/ne_50m_admin_0_countries.geojson
//
// Each country is keyed by the ISO 3166-1 alpha-2 code a declaration names
// (location.CountryCodes): ISO_A2_EH, which Natural Earth fills where it
// writes -99 in ISO_A2 (France, Norway, Kosovo), else ISO_A2. A feature with
// neither is left out, and the program names it. Each ring is a flat list of
// longitude, latitude pairs in tenths of a degree, the record's 0.1° grid,
// its closing point and a point equal to the one before it dropped, then
// simplified within half a grid step, the points two countries share kept
// (see simplify); a ring that encloses nothing at that grid is dropped. The output is compact JSON,
// its keys sorted, so that a run on the same source writes the same bytes.
//
// Run it from the repository's root:
//
//	go run ./tools/countryborders
//
// -in reads a local copy of the source instead of downloading it; -out
// writes elsewhere.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/gnoverse/gnockpit/location"
)

// sourceURL is the Natural Earth release the borders come from.
const sourceURL = "https://raw.githubusercontent.com/nvkelso/natural-earth-vector/v5.1.2/geojson/ne_50m_admin_0_countries.geojson"

// downloadTimeout bounds the download of the source, about 3 MB.
const downloadTimeout = 2 * time.Minute

// countryBorders are the asset's borders: by country code, the country's
// rings, each a flat list of longitude, latitude pairs in tenths of a
// degree. A point lies in a country when a ray from it crosses its rings an
// odd number of times, holes included.
type countryBorders map[string][][]int

// reading is what readBorders reads of the source: the borders, the names of
// the features left out, and how many rings it dropped as enclosing nothing
// at the grid.
type reading struct {
	countries countryBorders
	leftOut   []string
	collapsed int
}

// source is the part of the source's GeoJSON the program reads.
type source struct {
	Features []struct {
		Properties struct {
			Name    string `json:"NAME"`
			ISOA2   string `json:"ISO_A2"`
			ISOA2EH string `json:"ISO_A2_EH"`
		} `json:"properties"`
		Geometry struct {
			Type        string          `json:"type"`
			Coordinates json.RawMessage `json:"coordinates"`
		} `json:"geometry"`
	} `json:"features"`
}

func main() {
	in := flag.String("in", "", "read the source from this local copy instead of downloading it")
	out := flag.String("out", filepath.Join("web", "data", "country-borders.json"), "the asset to write")
	flag.Parse()
	if err := run(os.Stderr, sourceURL, *in, *out); err != nil {
		fmt.Fprintln(os.Stderr, "countryborders:", err)
		os.Exit(1)
	}
}

// run reads the source, from in when set, else from url, and writes the
// asset of its borders to out, saying on log what it wrote and left out.
func run(log io.Writer, url, in, out string) error {
	data, err := readSource(url, in)
	if err != nil {
		return err
	}
	r, err := readBorders(bytes.NewReader(data))
	if err != nil {
		return err
	}
	asset, err := encode(r.countries)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(out, asset, 0o644); err != nil {
		return err
	}
	rings := 0
	for _, rs := range r.countries {
		rings += len(rs)
	}
	_, err = fmt.Fprintf(log, "wrote %s: %d countries, %d rings, %d bytes; dropped %d rings enclosing nothing at the grid; left out, no country code: %q\n",
		out, len(r.countries), rings, len(asset), r.collapsed, r.leftOut)
	return err
}

// readSource reads the source: the local copy in when set, else the
// download of url.
func readSource(url, in string) ([]byte, error) {
	if in != "" {
		return os.ReadFile(in)
	}
	client := &http.Client{Timeout: downloadTimeout}
	res, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w (fetch it otherwise and pass -in)", url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", url, res.StatusCode)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	return b, nil
}

// readBorders reads the source's countries (see the package's comment).
func readBorders(r io.Reader) (reading, error) {
	var src source
	if err := json.NewDecoder(r).Decode(&src); err != nil {
		return reading{}, fmt.Errorf("read the source: %w", err)
	}
	out := reading{countries: countryBorders{}}
	var rings []countryRing
	for _, f := range src.Features {
		code := countryCode(f.Properties.ISOA2EH, f.Properties.ISOA2)
		if code == "" {
			out.leftOut = append(out.leftOut, f.Properties.Name)
			continue
		}
		polygons, err := polygonsOf(f.Geometry.Type, f.Geometry.Coordinates)
		if err != nil {
			return reading{}, fmt.Errorf("%s: %w", f.Properties.Name, err)
		}
		for _, polygon := range polygons {
			for _, ring := range polygon {
				rings = append(rings, countryRing{code: code, pts: rounded(ring)})
			}
		}
	}
	shared := sharedPoints(rings)
	for _, r := range rings {
		grid := onGrid(r.pts, shared)
		if grid == nil {
			out.collapsed++
			continue
		}
		out.countries[r.code] = append(out.countries[r.code], grid)
	}
	return out, nil
}

// countryRing is a ring of a country's borders on the grid, its country's
// code with it.
type countryRing struct {
	code string
	pts  [][2]int
}

// sharedPoints is the set of the points of rings that rings of two
// countries or more hold: the points of the borders they share, which
// simplify keeps, so that a shared border stays one line.
func sharedPoints(rings []countryRing) map[[2]int]bool {
	codes := map[[2]int]string{}
	shared := map[[2]int]bool{}
	for _, r := range rings {
		for _, p := range r.pts {
			c, seen := codes[p]
			switch {
			case !seen:
				codes[p] = r.code
			case c != r.code:
				shared[p] = true
			}
		}
	}
	return shared
}

// countryCode is the first of codes a declaration may name, "" when none is.
func countryCode(codes ...string) string {
	for _, c := range codes {
		if location.ValidCountry(c) {
			return c
		}
	}
	return ""
}

// polygonsOf reads a geometry's coordinates as polygons, each a list of
// rings, each a list of longitude, latitude points: a Polygon is one, a
// MultiPolygon several.
func polygonsOf(kind string, coordinates json.RawMessage) ([][][][2]float64, error) {
	switch kind {
	case "Polygon":
		var p [][][2]float64
		if err := json.Unmarshal(coordinates, &p); err != nil {
			return nil, err
		}
		return [][][][2]float64{p}, nil
	case "MultiPolygon":
		var ps [][][][2]float64
		if err := json.Unmarshal(coordinates, &ps); err != nil {
			return nil, err
		}
		return ps, nil
	default:
		return nil, fmt.Errorf("a %s geometry, not a Polygon or a MultiPolygon", kind)
	}
}

// simplifyTolerance is how far, in tenths of a degree, a point of a ring may
// lie from the edge that replaces it: half a grid step. Within it, every
// ring keeps its place on the grid, and the asset holds about 60 % of the
// bytes it would unsimplified.
const simplifyTolerance = 0.5

// rounded is ring on the 0.1° grid, in tenths of a degree: each point
// rounded, a point equal to the one before it dropped, and the closing
// point too.
func rounded(ring [][2]float64) [][2]int {
	var pts [][2]int
	for _, p := range ring {
		q := [2]int{int(math.Round(p[0] * 10)), int(math.Round(p[1] * 10))}
		if len(pts) == 0 || pts[len(pts)-1] != q {
			pts = append(pts, q)
		}
	}
	if len(pts) > 1 && pts[0] == pts[len(pts)-1] {
		pts = pts[:len(pts)-1]
	}
	return pts
}

// onGrid is the ring pts, on the grid (see rounded), simplified with the
// points of fixed kept (see simplify), as a flat list of longitude,
// latitude pairs in tenths of a degree. It is nil for a ring that encloses
// nothing at the grid: fewer than three points, or no area.
func onGrid(pts [][2]int, fixed map[[2]int]bool) []int {
	if len(pts) < 3 || twiceArea(pts) == 0 {
		return nil
	}
	// A ring that encloses area stays as rounded where its simplification
	// would enclose none.
	if simple := simplify(pts, fixed); len(simple) >= 3 && twiceArea(simple) != 0 {
		pts = simple
	}
	flat := make([]int, 0, 2*len(pts))
	for _, p := range pts {
		flat = append(flat, p[0], p[1])
	}
	return flat
}

// simplify drops from the closed ring pts each point between its neighbours
// on the line through them, which changes no line, then each point within
// simplifyTolerance of the edge that replaces it (Douglas–Peucker), keeping
// the points of fixed. The ring is split at the points of fixed it holds,
// each arc between two simplified alone, so that two countries' rings keep
// the border they share alike; a ring holding none is split at its first
// point and the point farthest from it, so that it keeps both.
func simplify(pts [][2]int, fixed map[[2]int]bool) [][2]int {
	pts = withoutStraightPoints(pts)
	if len(pts) < 4 {
		return pts
	}
	var anchors []int
	for i, p := range pts {
		if fixed[p] {
			anchors = append(anchors, i)
		}
	}
	if len(anchors) == 0 {
		far := 0
		for i, p := range pts {
			if distance(p, pts[0]) > distance(pts[far], pts[0]) {
				far = i
			}
		}
		anchors = []int{0, far}
	}
	var out [][2]int
	for j, a := range anchors {
		b := anchors[(j+1)%len(anchors)]
		kept := douglasPeucker(arc(pts, a, b))
		out = append(out, kept[:len(kept)-1]...)
	}
	return out
}

// arc is the closed ring pts from its point a to its point b, both
// included, going round past its end when b is not after a; the whole ring
// back to a when b is a.
func arc(pts [][2]int, a, b int) [][2]int {
	if b > a {
		return slices.Clone(pts[a : b+1])
	}
	return append(slices.Clone(pts[a:]), pts[:b+1]...)
}

// withoutStraightPoints is the closed ring pts without the points lying
// between the points before and after them, on the line through them: the
// tip of a spike of no width, on that line but not between them, stays.
func withoutStraightPoints(pts [][2]int) [][2]int {
	if len(pts) < 3 {
		return pts
	}
	var out [][2]int
	for i, p := range pts {
		prev := pts[(i+len(pts)-1)%len(pts)]
		next := pts[(i+1)%len(pts)]
		if cross(prev, p, next) != 0 || !between(prev, p, next) {
			out = append(out, p)
		}
	}
	return out
}

// between tells whether b, on the line through a and c, lies between them:
// the edges a to b and b to c run the same way.
func between(a, b, c [2]int) bool {
	return int64(b[0]-a[0])*int64(c[0]-b[0])+int64(b[1]-a[1])*int64(c[1]-b[1]) > 0
}

// cross is the cross product of the edges a to b and b to c: 0 when the
// three points lie on one line.
func cross(a, b, c [2]int) int64 {
	return int64(b[0]-a[0])*int64(c[1]-b[1]) - int64(b[1]-a[1])*int64(c[0]-b[0])
}

// douglasPeucker is the open line pts, its ends kept, without the points
// within simplifyTolerance of the edge that replaces them: the point
// farthest from the edge joining the ends stays when it lies beyond the
// tolerance, and each side of it is simplified the same way.
func douglasPeucker(pts [][2]int) [][2]int {
	if len(pts) < 3 {
		return slices.Clone(pts)
	}
	a, b := pts[0], pts[len(pts)-1]
	far, farOff := 0, 0.0
	for i := 1; i < len(pts)-1; i++ {
		if off := offEdge(pts[i], a, b); off > farOff {
			far, farOff = i, off
		}
	}
	if farOff <= simplifyTolerance {
		return [][2]int{a, b}
	}
	left := douglasPeucker(pts[:far+1])
	right := douglasPeucker(pts[far:])
	return append(left, right[1:]...)
}

// offEdge is the distance from p to the edge from a to b: to the nearest
// of its points, an end when p lies beyond it.
func offEdge(p, a, b [2]int) float64 {
	dx, dy := float64(b[0]-a[0]), float64(b[1]-a[1])
	length2 := dx*dx + dy*dy
	if length2 == 0 {
		return distance(p, a)
	}
	t := (float64(p[0]-a[0])*dx + float64(p[1]-a[1])*dy) / length2
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(float64(p[0])-(float64(a[0])+t*dx), float64(p[1])-(float64(a[1])+t*dy))
}

// distance is the distance between p and q.
func distance(p, q [2]int) float64 {
	return math.Hypot(float64(p[0]-q[0]), float64(p[1]-q[1]))
}

// twiceArea is twice the signed area of the ring pts (the shoelace
// formula), exact on the grid's integers.
func twiceArea(pts [][2]int) int64 {
	var a int64
	for i, p := range pts {
		q := pts[(i+1)%len(pts)]
		a += int64(p[0])*int64(q[1]) - int64(q[0])*int64(p[1])
	}
	return a
}

// encode writes b as the asset: compact JSON, its keys sorted (as
// encoding/json writes a map's), then a newline.
func encode(b countryBorders) ([]byte, error) {
	out, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
