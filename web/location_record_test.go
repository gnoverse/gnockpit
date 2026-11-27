package web

import (
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/location"
	"github.com/gnoverse/gnockpit/location/locationtest"
)

// runRecord runs script in node with location-record.js's exports imported
// as rec, and returns what it printed; anything it writes to stderr fails
// the test.
func runRecord(t *testing.T, script string) string {
	t.Helper()
	stdout, stderr := runNode(t, "import * as rec from "+moduleURL(t, "location-record.js")+"\n"+script)
	if stderr != "" {
		t.Errorf("node wrote to stderr: %s", stderr)
	}
	return stdout
}

// decodeNode reads the JSON node printed into v.
func decodeNode(t *testing.T, stdout string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(stdout), v); err != nil {
		t.Fatalf("read node's output %q: %v", stdout, err)
	}
}

// TestLocationMemoKeyOrder checks the memo's keys in the spec's order,
// whatever the order of the record's: a location's and a removal's.
func TestLocationMemoKeyOrder(t *testing.T) {
	op := locationAddr("A")
	stdout := runRecord(t, `const op = `+strconv.Quote(op)+`
console.log(JSON.stringify([
    rec.locationMemo({ at: '2026-11-26T12:00:00Z', provider: 'OVH', country: 'SG', lon: 103.8, lat: 1.3, operator: op, chain: 'c' }),
    rec.locationMemo({ at: '2026-11-26T12:00:00Z', clear: true, operator: op, chain: 'c' }),
]))
`)
	var got []string
	decodeNode(t, stdout, &got)
	want := []string{
		`gnockpit-location/v1 {"chain":"c","operator":"` + op + `","lat":1.3,"lon":103.8,"country":"SG","provider":"OVH","at":"2026-11-26T12:00:00Z"}`,
		`gnockpit-location/v1 {"chain":"c","operator":"` + op + `","clear":true,"at":"2026-11-26T12:00:00Z"}`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("locationMemo of a location and a removal =\n%q\nwant\n%q", got, want)
	}
}

// TestLocationRecordRounding checks a coordinate rounded to the 0.1° grid,
// its float64 error gone and -0 written 0, which the server reads alike:
// the memo holds the rounded coordinate.
func TestLocationRecordRounding(t *testing.T) {
	stdout := runRecord(t, `console.log(JSON.stringify({
    tenths: [48.86, 48.84, -0.04, 0.1 + 0.2, 179.96, -89.95].map(rec.tenth),
    negativeZero: Object.is(rec.tenth(-0.04), -0),
    memo: rec.locationMemo({ chain: 'c', operator: 'g1op', lat: -0.04, lon: 103.84, country: 'SG', provider: '', at: '2026-11-26T12:00:00Z' }),
}))
`)
	var got struct {
		Tenths       json.RawMessage
		NegativeZero bool
		Memo         string
	}
	decodeNode(t, stdout, &got)
	if want := `[48.9,48.8,0,0.3,180,-89.9]`; string(got.Tenths) != want {
		t.Errorf("tenth of 48.86, 48.84, -0.04, 0.1 + 0.2, 179.96, -89.95 = %s, want %s", got.Tenths, want)
	}
	if got.NegativeZero {
		t.Error("tenth(-0.04) is -0, want 0")
	}
	if want := `"lat":0,"lon":103.8,`; !strings.Contains(got.Memo, want) {
		t.Errorf("locationMemo with lat -0.04 and lon 103.84 = %s, want it to hold %s", got.Memo, want)
	}
}

// TestLocationMemoRefusesACoordinateOutOfRange checks that locationMemo
// throws, naming the coordinate, on one the server refuses: not a finite
// number, or past ±90 (lat) or ±180 (lon) once on the 0.1° grid. One that
// rounds onto the limit is written as it.
func TestLocationMemoRefusesACoordinateOutOfRange(t *testing.T) {
	stdout := runRecord(t, `const memo = (lat, lon) => {
    try {
        return 'returned ' + rec.locationMemo({ chain: 'c', operator: 'g1op', lat, lon, country: 'SG', provider: '', at: '2026-11-26T12:00:00Z' })
    } catch (err) {
        return (err instanceof Error ? 'an Error: ' : 'a non-Error: ') + err.message
    }
}
console.log(JSON.stringify([
    memo(NaN, 0), memo(0, Infinity), memo(0, -Infinity),
    memo(90.05, 0), memo(-90.06, 0), memo(0, 180.05), memo(0, -180.06),
    memo(90.04, -180.04),
]))
`)
	var got []string
	decodeNode(t, stdout, &got)
	want := []string{
		"an Error: lat must be a finite number, not NaN",
		"an Error: lon must be a finite number, not Infinity",
		"an Error: lon must be a finite number, not -Infinity",
		"an Error: lat must be within -90 and 90, not 90.05",
		"an Error: lat must be within -90 and 90, not -90.06",
		"an Error: lon must be within -180 and 180, not 180.05",
		"an Error: lon must be within -180 and 180, not -180.06",
		`returned gnockpit-location/v1 {"chain":"c","operator":"g1op","lat":90,"lon":-180,"country":"SG","provider":"","at":"2026-11-26T12:00:00Z"}`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("locationMemo of NaN, ±Infinity, 90.05, -90.06, 180.05, -180.06, and 90.04, -180.04 =\n%q\nwant\n%q", got, want)
	}
}

// TestRecordTime checks a record's time written to the second, in UTC,
// ending in Z: what the server reads.
func TestRecordTime(t *testing.T) {
	stdout := runRecord(t, `console.log(JSON.stringify(rec.recordTime(Date.UTC(2026, 10, 26, 12, 0, 0, 999))))
`)
	var got string
	decodeNode(t, stdout, &got)
	if want := "2026-11-26T12:00:00Z"; got != want {
		t.Errorf("recordTime(2026-11-26 12:00:00.999 UTC) = %q, want %q", got, want)
	}
}

// TestLocationDocumentVerifies checks the document the page writes, signed
// as gnokey signs it in either rendering: location.Verify accepts it and
// reads the record the page built.
func TestLocationDocumentVerifies(t *testing.T) {
	op := locationAddr("A")
	at := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name   string
		record string // the record, as a JavaScript object
		want   location.Record
	}{{
		name:   "a location",
		record: `{ chain: 'c', operator: op, lat: 1.3, lon: 103.8, country: 'SG', provider: 'Ünïcode & <Co>', at: '2026-11-26T12:00:00Z' }`,
		want:   location.Record{Chain: "c", Operator: op, Lat: 1.3, Lon: 103.8, Country: "SG", Provider: "Ünïcode & <Co>", At: at},
	}, {
		name:   "a removal",
		record: `{ chain: 'c', operator: op, clear: true, at: '2026-11-26T12:00:00Z' }`,
		want:   location.Record{Chain: "c", Operator: op, Clear: true, At: at},
	}} {
		t.Run(c.name, func(t *testing.T) {
			doc := runRecord(t, `const op = `+strconv.Quote(op)+`
process.stdout.write(rec.unsignedDocument(rec.locationMemo(`+c.record+`), op))
`)
			for _, rd := range []struct {
				name string
				r    location.Rendering
			}{{"Legacy", location.Legacy}, {"Current", location.Current}} {
				t.Run(rd.name, func(t *testing.T) {
					signed := locationtest.Sign(t, []byte(doc), locationKey("A"), location.SignChainID, rd.r)
					d, err := location.Verify(signed)
					if err != nil {
						t.Fatalf("Verify(%s) = %v, want nil", signed, err)
					}
					if !reflect.DeepEqual(d.Record, c.want) {
						t.Errorf("Verify's record = %+v, want %+v", d.Record, c.want)
					}
				})
			}
		})
	}
}

// TestProviderMatchAgreesWithTheServer checks the page's provider key and
// match against the server's (location.ProviderKey, ListedProvider), over
// names that differ in case, spaces, punctuation and letters beyond ASCII.
// The listed set, sorted as the server sorts it, holds two spellings of one
// key (the match is the first in its order) and spellings the names match
// by their key alone. A capital sigma lowers to σ wherever it stands, as
// the server lowers each letter alone: ΛΥΣΕΙΣ is Λυσεισ, not Λυσεις, whose
// final ς is a letter of its own. No provider ("") stays none, though a
// listed spelling (--) has the same empty key.
func TestProviderMatchAgreesWithTheServer(t *testing.T) {
	names := []string{
		"OVH SAS", "ovh sas", "O.V.H. SAS", "on-premise", "On Premise", "Ünïcode Ltd", "İstanbul DC", "AT&T", "--",
		"ΛΥΣΕΙΣ", "Λυσεις", "Λυσεισ", "",
	}
	listed := []string{"Ovh Sas", "OVH SAS", location.OnPremise, "ÜNÏCODE LTD", "ISTANBUL DC", "AT&T", "Λυσεις", "Λυσεισ", "--"}
	slices.Sort(listed)
	in, err := json.Marshal(map[string][]string{"names": names, "listed": listed})
	if err != nil {
		t.Fatal(err)
	}
	stdout := runRecord(t, `const { names, listed } = `+string(in)+`
console.log(JSON.stringify({
    keys: names.map(rec.providerKey),
    matches: names.map((name) => rec.listedProvider(name, listed)),
}))
`)
	var got struct{ Keys, Matches []string }
	decodeNode(t, stdout, &got)
	if len(got.Keys) != len(names) || len(got.Matches) != len(names) {
		t.Fatalf("node gave %d keys and %d matches for %d names", len(got.Keys), len(got.Matches), len(names))
	}
	for i, name := range names {
		if want := location.ProviderKey(name); got.Keys[i] != want {
			t.Errorf("providerKey(%q) = %q, location.ProviderKey = %q", name, got.Keys[i], want)
		}
		if want := location.ListedProvider(name, listed); got.Matches[i] != want {
			t.Errorf("listedProvider(%q, %q) = %q, location.ListedProvider = %q", name, listed, got.Matches[i], want)
		}
	}
}

// TestSignSnippet checks the commands the page gives to sign a document:
// it is written to gnockpit-location.json as it is (a quoted here-document
// expands nothing), over a file already there even under noclobber (>|),
// signed for gnockpit-location, account 0, sequence 0, and printed only
// when the signing succeeds, so a failed sign never prints the unsigned
// document. Both cats are the system's cat, never an alias (bat) that
// would decorate what the operator copies back nor a function: command
// skips functions, and the backslash quotes cat, which an alias never
// matches, even in ksh93, whose command is itself an alias ending in a
// space.
func TestSignSnippet(t *testing.T) {
	op := locationAddr("A")
	const document = "{\"memo\": \"$HOME `id`\",\n\"signatures\": null}"
	stdout := runRecord(t, `console.log(JSON.stringify(rec.signSnippet(`+strconv.Quote(document)+`, `+strconv.Quote(op)+`)))
`)
	var got string
	decodeNode(t, stdout, &got)
	want := "command \\cat >| gnockpit-location.json <<'EOF'\n" +
		document + "\n" +
		"EOF\n" +
		"gnokey sign -tx-path gnockpit-location.json -chainid gnockpit-location -account-number 0 -account-sequence 0 " + op +
		" && command \\cat gnockpit-location.json"
	if got != want {
		t.Errorf("signSnippet =\n%s\nwant\n%s", got, want)
	}
}

// TestSignSnippetRefusesAnOperatorThatIsNoAddress checks that signSnippet
// throws, naming it, on an operator other than g1 and lowercase letters and
// digits: whatever else would run in the operator's shell.
func TestSignSnippetRefusesAnOperatorThatIsNoAddress(t *testing.T) {
	operators := []string{"", "g1", "G1ABC", "g1abc; rm -rf ~", "g1abc $(id)", "g1abc\nid", " g1abc", "g1abc<in&>out"}
	in, err := json.Marshal(operators)
	if err != nil {
		t.Fatal(err)
	}
	stdout := runRecord(t, `console.log(JSON.stringify(`+string(in)+`.map((op) => {
    try {
        rec.signSnippet('{}', op)
        return 'returned'
    } catch (err) {
        return (err instanceof Error ? 'an Error: ' : 'a non-Error: ') + err.message
    }
})))
`)
	var got []string
	decodeNode(t, stdout, &got)
	if len(got) != len(operators) {
		t.Fatalf("node gave %d results for %d operators", len(got), len(operators))
	}
	for i, op := range operators {
		want := "an Error: the operator must be a g1 address, not " + jsonQuote(t, op)
		if got[i] != want {
			t.Errorf("signSnippet for operator %q: %s, want %s", op, got[i], want)
		}
	}
}

// jsonQuote is s as a JSON string literal with <, > and & written as they
// are, which is what JSON.stringify gives for the operators the tests
// quote. U+2028 and U+2029 still differ: the encoder escapes them,
// JSON.stringify does not, and no quoted operator holds either.
func jsonQuote(t *testing.T, s string) string {
	t.Helper()
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// TestCountryCodesAreCountries checks the codes the page offers for a
// declared country: the server's (location.CountryCodes), in its order,
// 250 codes, each once, each one the server accepts.
func TestCountryCodesAreCountries(t *testing.T) {
	stdout, stderr := runNode(t, `import { COUNTRY_CODES } from `+moduleURL(t, "countries.js")+`
console.log(JSON.stringify(COUNTRY_CODES))
`)
	if stderr != "" {
		t.Errorf("node wrote to stderr: %s", stderr)
	}
	var got []string
	decodeNode(t, stdout, &got)
	if len(got) != 250 {
		t.Errorf("COUNTRY_CODES holds %d codes, want 250", len(got))
	}
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c] {
			t.Errorf("COUNTRY_CODES holds %q twice", c)
		}
		seen[c] = true
		if !location.ValidCountry(c) {
			t.Errorf("COUNTRY_CODES holds %q, which location.ValidCountry refuses", c)
		}
	}
	if want := location.CountryCodes(); !slices.Equal(got, want) {
		t.Errorf("COUNTRY_CODES =\n%q\nwant location.CountryCodes()\n%q", got, want)
	}
}

// TestOnPremiseIsTheServers checks the provider the page lists for an
// operator's own hardware: the server's, location.OnPremise.
func TestOnPremiseIsTheServers(t *testing.T) {
	stdout := runRecord(t, `console.log(JSON.stringify(rec.ON_PREMISE))
`)
	var got string
	decodeNode(t, stdout, &got)
	if got != location.OnPremise {
		t.Errorf("ON_PREMISE = %q, want location.OnPremise %q", got, location.OnPremise)
	}
}

// TestProviderCheckAgreesWithTheServer checks the page's check of a
// provider against the server's (ParseMemo), reason for reason: at most 64
// characters, counted as code points; no control or format character; no
// white space at either end; a letter or a digit unless empty.
func TestProviderCheckAgreesWithTheServer(t *testing.T) {
	names := []string{
		"", "OVH", "AT&T", "on-premise", "--", "—", " ",
		strings.Repeat("a", 64), strings.Repeat("a", 65),
		strings.Repeat("é", 64), strings.Repeat("é", 65),
		strings.Repeat("😀", 63) + "a", strings.Repeat("😀", 64) + "a",
		"OVH\n", "OVH\t", "OVH\u0085", "OVH\u007f", "O\u200dVH", "\u00adOVH", "OVH\ufeff",
		" OVH", "OVH ", "OVH SAS", "\u00a0OVH", "OVH\u1680", "\u2000OVH", "OVH\u200a", "\u2028OVH", "OVH\u2029",
		"\u202fOVH", "OVH\u205f", "\u3000OVH", "\ufeffOVH", "\nOVH",
	}
	in, err := json.Marshal(names)
	if err != nil {
		t.Fatal(err)
	}
	stdout := runRecord(t, `console.log(JSON.stringify(`+string(in)+`.map((n) => rec.providerError(n))))
`)
	var got []string
	decodeNode(t, stdout, &got)
	if len(got) != len(names) {
		t.Fatalf("node gave %d reasons for %d names", len(got), len(names))
	}
	op := locationAddr("A")
	for i, name := range names {
		r := location.Record{Chain: "c", Operator: op, Lat: 1.3, Lon: 103.8, Country: "SG", Provider: name, At: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)}
		want := ""
		if _, err := location.ParseMemo(locationtest.Memo(r)); err != nil {
			want = err.Error()
		}
		if got[i] != want {
			t.Errorf("providerError(%q) = %q, ParseMemo refuses it with %q", name, got[i], want)
		}
	}
}
