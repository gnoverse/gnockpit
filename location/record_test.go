package location

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/gno/tm2/pkg/bech32"
	"github.com/gnolang/gno/tm2/pkg/crypto"
)

var op = crypto.AddressFromPreimage([]byte("op")).String()

const at = "2026-11-26T12:00:00Z"

func memo(body string) string { return MemoPrefix + body }

// operatorOf is a g1 address of n zero bytes, its checksum valid: an address
// of other than 20 bytes is no account's.
func operatorOf(t *testing.T, n int) string {
	t.Helper()
	s, err := bech32.ConvertAndEncode(crypto.Bech32AddrPrefix(), make([]byte, n))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func full(lat, lon, country, provider, atv string) string {
	return fmt.Sprintf(`{"chain":"gnoland1","operator":%q,"lat":%s,"lon":%s,"country":%q,"provider":%q,"at":%q}`,
		op, lat, lon, country, provider, atv)
}

func TestParseMemoLocation(t *testing.T) {
	got, err := ParseMemo(memo(full("48.9", "2.4", "FR", "OVH SAS", at)))
	if err != nil {
		t.Fatal(err)
	}
	want := Record{
		Chain: "gnoland1", Operator: op, Lat: 48.9, Lon: 2.4, Country: "FR", Provider: "OVH SAS",
		At: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC),
	}
	if got != want {
		t.Fatalf("ParseMemo = %+v, want %+v", got, want)
	}
}

func TestParseMemoAbsentProviderReadsEmpty(t *testing.T) {
	m := fmt.Sprintf(`{"chain":"gnoland1","operator":%q,"lat":1,"lon":2,"country":"FR","at":%q}`, op, at)
	got, err := ParseMemo(memo(m))
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "" {
		t.Fatalf("Provider = %q, want empty", got.Provider)
	}
}

func TestParseMemoRemoval(t *testing.T) {
	m := fmt.Sprintf(`{"chain":"gnoland1","operator":%q,"clear":true,"at":%q}`, op, at)
	got, err := ParseMemo(memo(m))
	if err != nil {
		t.Fatal(err)
	}
	want := Record{
		Chain: "gnoland1", Operator: op, Clear: true,
		At: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC),
	}
	if got != want {
		t.Fatalf("ParseMemo = %+v, want %+v", got, want)
	}
}

func TestParseMemoAcceptsTheGridsEdges(t *testing.T) {
	for _, tc := range []struct {
		lat, lon string
		wantLat  float64
		wantLon  float64
	}{
		{"90", "180", 90, 180},
		{"-90", "-180", -90, -180},
		{"0", "0", 0, 0},
		{"-0.0", "-0", 0, 0},
	} {
		got, err := ParseMemo(memo(full(tc.lat, tc.lon, "FR", "", at)))
		if err != nil {
			t.Fatalf("lat %s lon %s: %v", tc.lat, tc.lon, err)
		}
		if got.Lat != tc.wantLat || got.Lon != tc.wantLon {
			t.Errorf("lat %s lon %s = %v, %v, want %v, %v", tc.lat, tc.lon, got.Lat, got.Lon, tc.wantLat, tc.wantLon)
		}
		if (tc.wantLat == 0 && math.Signbit(got.Lat)) || (tc.wantLon == 0 && math.Signbit(got.Lon)) {
			t.Errorf("lat %s lon %s: a zero keeps its sign bit", tc.lat, tc.lon)
		}
	}
}

func TestParseMemoRefuses(t *testing.T) {
	removal := func(extra string) string {
		return fmt.Sprintf(`{"chain":"gnoland1","operator":%q,%s"at":%q}`, op, extra, at)
	}
	for _, tc := range []struct {
		name, memo, want string
	}{
		{"no prefix", full("48.9", "2.4", "FR", "", at), "no gnockpit-location/v1 record"},
		{"other version", "gnockpit-location/v2 " + full("48.9", "2.4", "FR", "", at), "no gnockpit-location/v1 record"},
		{"two decimals", memo(full("48.95", "2.4", "FR", "", at)), "lat must have at most one decimal"},
		{"exponent", memo(full("4.89e1", "2.4", "FR", "", at)), "lat must have at most one decimal"},
		{"trailing zero", memo(full("48.90", "2.4", "FR", "", at)), "lat must have at most one decimal"},
		{"lat too high", memo(full("90.1", "2.4", "FR", "", at)), "lat must be within -90 and 90"},
		{"lon too low", memo(full("48.9", "-180.1", "FR", "", at)), "lon must be within -180 and 180"},
		{"lowercase country", memo(full("48.9", "2.4", "fr", "", at)), "country must be an ISO 3166-1 alpha-2 code"},
		{"EU", memo(full("48.9", "2.4", "EU", "", at)), "country must be an ISO 3166-1 alpha-2 code"},
		{"ZZ", memo(full("48.9", "2.4", "ZZ", "", at)), "country must be an ISO 3166-1 alpha-2 code"},
		{"alpha-3", memo(full("48.9", "2.4", "FRA", "", at)), "country must be an ISO 3166-1 alpha-2 code"},
		{"long provider", memo(full("48.9", "2.4", "FR", strings.Repeat("é", 65), at)), "provider must be at most 64 characters"},
		{"control character", memo(full("48.9", "2.4", "FR", "OVH\n", at)), "provider must hold no control character"},
		{"no letter", memo(full("48.9", "2.4", "FR", "--", at)), "provider must hold a letter or a digit"},
		{"at without zone", memo(full("48.9", "2.4", "FR", "", "2026-11-26 12:00:00")), "at must be an RFC 3339 time in UTC"},
		{"at with offset", memo(full("48.9", "2.4", "FR", "", "2026-11-26T13:00:00+01:00")), "at must be an RFC 3339 time in UTC"},
		{"bad operator", memo(strings.Replace(full("48.9", "2.4", "FR", "", at), op, "g1nope", 1)), "operator must be a g1 address"},
		{"operator of 0 bytes", memo(strings.Replace(full("48.9", "2.4", "FR", "", at), op, operatorOf(t, 0), 1)), "operator must be a g1 address"},
		{"operator of 19 bytes", memo(strings.Replace(full("48.9", "2.4", "FR", "", at), op, operatorOf(t, 19), 1)), "operator must be a g1 address"},
		{"operator of 21 bytes", memo(strings.Replace(full("48.9", "2.4", "FR", "", at), op, operatorOf(t, 21), 1)), "operator must be a g1 address"},
		{"no chain", memo(fmt.Sprintf(`{"operator":%q,"lat":1,"lon":2,"country":"FR","at":%q}`, op, at)), "chain is required"},
		{"no lat", memo(fmt.Sprintf(`{"chain":"gnoland1","operator":%q,"lon":2,"country":"FR","at":%q}`, op, at)), "lat is required"},
		{"removal with lat", memo(removal(`"clear":true,"lat":1,`)), "a removal holds no location"},
		{"clear false", memo(removal(`"clear":false,`)), "clear must be true or absent"},
		{"unknown field", memo(strings.Replace(full("48.9", "2.4", "FR", "", at), `"at"`, `"city":"Paris","at"`, 1)), "unknown field"},
		{"no lon", memo(fmt.Sprintf(`{"chain":"gnoland1","operator":%q,"lat":1,"country":"FR","at":%q}`, op, at)), "lon is required"},
		{"no country", memo(fmt.Sprintf(`{"chain":"gnoland1","operator":%q,"lat":1,"lon":2,"at":%q}`, op, at)), "country is required"},
		{"removal with lon", memo(removal(`"clear":true,"lon":1,`)), "a removal holds no location"},
		{"removal with country", memo(removal(`"clear":true,"country":"FR",`)), "a removal holds no location"},
		{"removal with provider", memo(removal(`"clear":true,"provider":"OVH",`)), "a removal holds no location"},
		{"misspelled key", memo(strings.Replace(full("48.9", "2.4", "FR", "", at), `"lat"`, `"LAT"`, 1)), "unknown field"},
		{"duplicate key", memo(strings.Replace(full("48.9", "2.4", "FR", "", at), `"lon":2.4`, `"lon":2.4,"lon":2.5`, 1)), "duplicate key"},
		{"quoted lat", memo(full(`"1.5"`, "2.4", "FR", "", at)), "lat must be a JSON number"},
		{"null provider", memo(strings.Replace(full("48.9", "2.4", "FR", "", at), `"provider":""`, `"provider":null`, 1)), "provider must not be null"},
		{"null clear", memo(removal(`"clear":null,`)), "clear must not be null"},
		{"null lat", memo(full("null", "2.4", "FR", "", at)), "lat must not be null"},
		{"huge lat", memo(full(strings.Repeat("9", 300), "2.4", "FR", "", at)), "lat must be within -90 and 90"},
		{"format character", memo(full("48.9", "2.4", "FR", "OVH\u202e", at)), "provider must hold no control character or format character"},
		{"zero-width space", memo(full("48.9", "2.4", "FR", "OVH\u200b", at)), "provider must hold no control character"},
		{"provider after a space", memo(full("48.9", "2.4", "FR", " OVH", at)), "provider must not start or end with white space"},
		{"provider before a space", memo(full("48.9", "2.4", "FR", "OVH ", at)), "provider must not start or end with white space"},
		{"provider before a no-break space", memo(full("48.9", "2.4", "FR", "OVH\u00a0", at)), "provider must not start or end with white space"},
		{"provider after an ideographic space", memo(full("48.9", "2.4", "FR", "\u3000OVH", at)), "provider must not start or end with white space"},
		{"provider of a space", memo(full("48.9", "2.4", "FR", " ", at)), "provider must not start or end with white space"},
		{"country of a withdrawn code", memo(full("48.9", "2.4", "UK", "", at)), "country must be an ISO 3166-1 alpha-2 code"},
		{"chain not a string", memo(strings.Replace(full("48.9", "2.4", "FR", "", at), `"gnoland1"`, `1`, 1)), "chain must be a string"},
		{"operator not a string", memo(strings.Replace(full("48.9", "2.4", "FR", "", at), `"`+op+`"`, `1`, 1)), "operator must be a string"},
		{"at not a string", memo(strings.Replace(full("48.9", "2.4", "FR", "", at), `"`+at+`"`, `1`, 1)), "at must be a string"},
		{"country not a string", memo(strings.Replace(full("48.9", "2.4", "FR", "", at), `"FR"`, `true`, 1)), "country must be a string"},
		{"provider not a string", memo(strings.Replace(full("48.9", "2.4", "FR", "", at), `"provider":""`, `"provider":true`, 1)), "provider must be a string"},
		{"trailing text", memo(full("48.9", "2.4", "FR", "", at) + " x"), "one JSON object"},
		{"at before 1678", memo(full("48.9", "2.4", "FR", "", "1677-12-31T23:59:59Z")), "at must be a time between 1678 and 2262"},
		{"at in year 1", memo(full("48.9", "2.4", "FR", "", "0001-01-01T00:00:00Z")), "at must be a time between 1678 and 2262"},
		{"at after 2262-04-11T23:47:16Z", memo(full("48.9", "2.4", "FR", "", "2262-04-11T23:47:17Z")), "at must be a time between 1678 and 2262"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseMemo(tc.memo)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParseMemo(%q) error = %v, want one containing %q", tc.memo, err, tc.want)
			}
		})
	}
}

func TestValidCountry(t *testing.T) {
	for _, tc := range []struct {
		code string
		want bool
	}{
		{"FR", true},
		{"SG", true},
		{"XK", true},
		{"NA", true},
		{"fr", false},
		{"EU", false},
		{"ZZ", false},
		{"", false},
		{"FRA", false},
		{"AN", false},
		{"UK", false},
		{"SU", false},
		{"AC", false},
	} {
		if got := ValidCountry(tc.code); got != tc.want {
			t.Errorf("ValidCountry(%q) = %v, want %v", tc.code, got, tc.want)
		}
	}
}

// TestValidProvider checks the providers a record may hold, as ParseMemo
// checks them: none, or a name of at most 64 characters, with no control or
// format character, no white space at either end, and a letter or a digit.
func TestValidProvider(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"OVH SAS", true},
		{OnPremise, true},
		{"", true},
		{strings.Repeat("a", MaxProvider), true},
		{strings.Repeat("a", MaxProvider+1), false},
		{"FRIT ", false},
		{" FRIT", false},
		{"Soft\u00adHyphen Net", false},
		{"OVH\n", false},
		{"--", false},
	} {
		if got := ValidProvider(tc.name); got != tc.want {
			t.Errorf("ValidProvider(%q) = %v, want %v", tc.name, got, tc.want)
		}
		_, err := ParseMemo(memo(full("48.9", "2.4", "FR", tc.name, at)))
		if got := err == nil; got != tc.want {
			t.Errorf("ParseMemo of provider %q: %v, want it accepted %v", tc.name, err, tc.want)
		}
	}
}

func TestParseMemoReadsTheOperatorCanonically(t *testing.T) {
	upper := strings.ToUpper(op)
	got, err := ParseMemo(memo(strings.Replace(full("48.9", "2.4", "FR", "", at), op, upper, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Operator != op {
		t.Fatalf("Operator = %q, want %q", got.Operator, op)
	}
}

func TestParseMemoAcceptsAFractionalAt(t *testing.T) {
	got, err := ParseMemo(memo(full("48.9", "2.4", "FR", "", "2026-11-26T12:00:00.5Z")))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 26, 12, 0, 0, 500_000_000, time.UTC); !got.At.Equal(want) {
		t.Fatalf("At = %v, want %v", got.At, want)
	}
}

// TestParseMemoAcceptsTheTimeRangesEdges checks the start of 1678, the first
// whole year an int64 of nanoseconds since 1970 covers, and that int64's last
// whole second.
func TestParseMemoAcceptsTheTimeRangesEdges(t *testing.T) {
	for _, tc := range []struct {
		at   string
		want time.Time
	}{
		{"1678-01-01T00:00:00Z", time.Date(1678, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"2262-04-11T23:47:16Z", time.Date(2262, 4, 11, 23, 47, 16, 0, time.UTC)},
	} {
		t.Run(tc.at, func(t *testing.T) {
			got, err := ParseMemo(memo(full("48.9", "2.4", "FR", "", tc.at)))
			if err != nil {
				t.Fatal(err)
			}
			if !got.At.Equal(tc.want) {
				t.Fatalf("At = %v, want %v", got.At, tc.want)
			}
		})
	}
}

func TestParseMemoErrorsEchoLittle(t *testing.T) {
	long := strings.Repeat("k", 500)
	for _, m := range []string{
		memo(strings.Replace(full("48.9", "2.4", "FR", "", at), `"lat"`, `"`+long+`"`, 1)),
		memo(full(strings.Repeat("9", 300), "2.4", "FR", "", at)),
		memo(full("48.9", "2.4", "FR", "", long)),
	} {
		_, err := ParseMemo(m)
		if err == nil || len(err.Error()) > 200 {
			t.Errorf("ParseMemo error = %v, want one under 200 bytes", err)
		}
	}
}
