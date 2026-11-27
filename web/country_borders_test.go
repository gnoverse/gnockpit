package web

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gnoverse/gnockpit/location"
)

// countryBordersFile is the asset the location form reads, as it lies in the
// repository: the binary embeds it.
const countryBordersFile = "data/country-borders.json"

// runBorders runs script in node with country-borders.js's exports imported
// as cb, and returns what it printed; anything it writes to stderr fails the
// test.
func runBorders(t *testing.T, script string) string {
	t.Helper()
	stdout, stderr := runNode(t, "import * as cb from "+moduleURL(t, "country-borders.js")+"\n"+script)
	if stderr != "" {
		t.Errorf("node wrote to stderr: %s", stderr)
	}
	return stdout
}

// lookup is a point the lookup is asked for, and the country it must find,
// "" for none.
type lookup struct {
	name     string
	lat, lon float64
	want     string
}

// countriesOf asks node's countryAt for each point of lookups over borders,
// a JavaScript expression of a Map, and checks what it finds.
func countriesOf(t *testing.T, borders string, lookups []lookup) {
	t.Helper()
	points := make([][2]float64, len(lookups))
	for i, l := range lookups {
		points[i] = [2]float64{l.lat, l.lon}
	}
	in, err := json.Marshal(points)
	if err != nil {
		t.Fatal(err)
	}
	stdout := runBorders(t, `const borders = `+borders+`
console.log(JSON.stringify(`+string(in)+`.map(([lat, lon]) => cb.countryAt(borders, lat, lon) ?? '')))
`)
	var got []string
	decodeNode(t, stdout, &got)
	if len(got) != len(lookups) {
		t.Fatalf("node gave %d countries for %d points", len(got), len(lookups))
	}
	for i, l := range lookups {
		t.Run(l.name, func(t *testing.T) {
			if got[i] != l.want {
				t.Errorf("countryAt(%v, %v) = %q, want %q", l.lat, l.lon, got[i], l.want)
			}
		})
	}
}

// TestCountryAt checks the lookup on a map of its own, in tenths of a
// degree: ZA, lon 0 to 10 and lat 0 to 10, holding LS (lon and lat 4 to 6)
// in a hole; NA, sharing ZA's western border; and AA and BB, which overlap.
// A point lies in the one country whose rings hold it: inside, not in the
// sea, not in a hole, and on a shared border in one country alone. It is
// rounded to the 0.1° grid first. A point two countries hold, as borders
// rounded to the grid may, lies in none. A point no country holds lies in
// the one country whose edge it lies on, as on a coast; on the edges of two
// countries and in neither, in none.
func TestCountryAt(t *testing.T) {
	const borders = `new Map([
    ['ZA', [[0, 0, 100, 0, 100, 100, 0, 100], [40, 40, 40, 60, 60, 60, 60, 40]]],
    ['LS', [[40, 40, 60, 40, 60, 60, 40, 60]]],
    ['NA', [[-100, 0, 0, 0, 0, 100, -100, 100]]],
    ['AA', [[200, 200, 300, 200, 300, 300, 200, 300]]],
    ['BB', [[250, 250, 350, 250, 350, 350, 250, 350]]],
])`
	countriesOf(t, borders, []lookup{
		{"inside", 2, 2, "ZA"},
		{"the sea", -20, 20, ""},
		{"the hole", 5, 5, "LS"},
		{"beside the hole", 5, 3.9, "ZA"},
		{"beside the shared border, east", 5, 0.1, "ZA"},
		{"beside the shared border, west", 5, -0.1, "NA"},
		{"on the shared border", 5, 0, "ZA"},
		{"off the grid, rounded onto the hole's edge", 3.96, 5, "LS"},
		{"in one of two overlapping countries", 22, 22, "AA"},
		{"in two overlapping countries", 27, 27, ""},
		{"on the northern coast", 10, 5, "ZA"},
		{"on the eastern coast", 5, 10, "ZA"},
		{"off the grid, rounded onto the northern coast", 10.04, 5, "ZA"},
		{"on the coast where two countries' borders end", 10, 0, ""},
	})
}

// TestCountryAtOnTheBorders checks the lookup on the asset: capitals, among
// them those of countries Natural Earth writes -99 in ISO_A2 for (France,
// Norway, Kosovo) and Lesotho, a hole in South Africa; points at sea; and
// Hargeisa, in Somaliland, a feature with no country code, left out.
func TestCountryAtOnTheBorders(t *testing.T) {
	file, err := filepath.Abs(countryBordersFile)
	if err != nil {
		t.Fatal(err)
	}
	borders := `new Map(Object.entries(JSON.parse((await import('node:fs')).readFileSync(` + strconv.Quote(file) + `, 'utf8'))))`
	countriesOf(t, borders, []lookup{
		{"Paris", 48.9, 2.3, "FR"},
		{"Oslo", 59.9, 10.8, "NO"},
		{"Pristina", 42.7, 21.2, "XK"},
		{"Taipei", 25.0, 121.5, "TW"},
		{"Maseru", -29.3, 27.5, "LS"},
		{"Johannesburg", -26.2, 28.0, "ZA"},
		{"Brasília", -15.8, -47.9, "BR"},
		{"Canberra", -35.3, 149.1, "AU"},
		{"Singapore", 1.3, 103.8, "SG"},
		{"Singapore's centre, rounded onto its coast", 1.35, 103.82, "SG"},
		{"the Atlantic", 0, -30, ""},
		{"the Pacific", 0, -150, ""},
		{"Hargeisa", 9.6, 44.1, ""},
	})
}

// TestCountryBordersAreCountries checks the asset the binary embeds: each
// country keyed by a code a declaration may name (location.CountryCodes,
// which COUNTRY_CODES equals), each ring three points or more, in pairs.
func TestCountryBordersAreCountries(t *testing.T) {
	data, err := content.ReadFile(countryBordersFile)
	if err != nil {
		t.Fatal(err)
	}
	var borders map[string][][]int
	if err := json.Unmarshal(data, &borders); err != nil {
		t.Fatalf("decode %s: %v", countryBordersFile, err)
	}
	if len(borders) == 0 {
		t.Fatalf("%s holds no country", countryBordersFile)
	}
	for code, rings := range borders {
		if !location.ValidCountry(code) {
			t.Errorf("%s keys %q, no code a declaration may name", countryBordersFile, code)
		}
		if len(rings) == 0 {
			t.Errorf("%s: %s has no ring", countryBordersFile, code)
		}
		for i, r := range rings {
			if len(r) < 6 || len(r)%2 != 0 {
				t.Errorf("%s: %s's ring %d holds %d numbers, want pairs of three points or more", countryBordersFile, code, i, len(r))
			}
		}
	}
}

// TestReadBordersRefusesOtherAnswers checks the read of the borders when
// gnockpit's answer holds no country borders, as a proxy's error page in
// JSON might: the read warns that it failed and forgets it, so that the next
// read tries again, and the lookup finds no country, as before any read.
func TestReadBordersRefusesOtherAnswers(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{"FR": 5}`, `{"error": "bad gateway"}`, `{"FR": [5]}`} {
		t.Run(body, func(t *testing.T) {
			stdout := runBorders(t, `const warned = []
console.warn = (...args) => warned.push(args.join(' '))
let reads = 0
globalThis.fetch = async () => {
    reads++
    return new Response(`+strconv.Quote(body)+`, { status: 200, headers: { 'Content-Type': 'application/json' } })
}
await cb.readBorders()
const country = cb.countryOf(48.9, 2.3)
await cb.readBorders()
console.log(JSON.stringify({ warned, reads, country }))
`)
			var got struct {
				Warned  []string
				Reads   int
				Country *string
			}
			decodeNode(t, stdout, &got)
			want := "read the country borders: the answer holds no country borders"
			if len(got.Warned) != 2 || got.Warned[0] != want || got.Warned[1] != want {
				t.Errorf("warned %q, want twice %q", got.Warned, want)
			}
			if got.Reads != 2 {
				t.Errorf("read the borders %d times, want 2: a refused answer is forgotten", got.Reads)
			}
			if got.Country != nil {
				t.Errorf("countryOf(48.9, 2.3) = %q, want null", *got.Country)
			}
		})
	}
}
