// Package location reads the record an operator declares for its validator's
// location, carried in the memo of a document signed offline.
package location

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gnolang/gno/tm2/pkg/crypto"
)

// MemoPrefix opens a memo that carries a declared location's record.
const MemoPrefix = "gnockpit-location/v1 "

// Record is a declared location, or its removal when Clear is set (a removal
// has no coordinates, country or provider).
type Record struct {
	Chain, Operator string
	Clear           bool
	Lat, Lon        float64
	Country         string
	Provider        string
	At              time.Time
}

// fields are the keys a record may hold.
var fields = map[string]struct{}{
	"chain": {}, "operator": {}, "clear": {}, "lat": {}, "lon": {}, "country": {}, "provider": {}, "at": {},
}

// coordinate is a signed integer with at most one decimal, the 0.1 degree grid.
var coordinate = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9])?$`)

// maxEcho bounds how much of the input an error message quotes.
const maxEcho = 32

func echo(s string) string { return strconv.Quote(cut(s, maxEcho)) }

// cut returns s cut to its first n runes, "..." marking the cut.
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

// ParseMemo reads the record a memo carries: MemoPrefix, then one JSON object.
// The object is read strictly: keys are spelled exactly, appear once, and no
// value is null. It checks the record's form; whether the chain, operator and
// time suit the request is for the caller.
func ParseMemo(memo string) (Record, error) {
	body, ok := strings.CutPrefix(memo, MemoPrefix)
	if !ok {
		return Record{}, errors.New("no gnockpit-location/v1 record in the memo")
	}
	raw, err := readObject(body)
	if err != nil {
		return Record{}, err
	}
	return recordOf(raw)
}

// readObject returns the values of the one JSON object body holds, by key.
func readObject(body string) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(strings.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("the record must be a JSON object")
	}
	raw := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("record is not a valid JSON object: %w", err)
		}
		// Inside an object the decoder yields a key as a string, never another token.
		key, _ := tok.(string)
		if _, known := fields[key]; !known {
			return nil, fmt.Errorf("unknown field %s", echo(key))
		}
		if _, dup := raw[key]; dup {
			return nil, fmt.Errorf("duplicate key %s", key)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("record is not a valid JSON object: %w", err)
		}
		if string(v) == "null" {
			return nil, fmt.Errorf("%s must not be null", key)
		}
		raw[key] = v
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("record is not a valid JSON object: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("the memo must hold one JSON object and nothing after it")
	}
	return raw, nil
}

// text reads the string value of key; present is false when the key is absent.
func text(raw map[string]json.RawMessage, key string) (s string, present bool, err error) {
	v, present := raw[key]
	if !present {
		return "", false, nil
	}
	if err := json.Unmarshal(v, &s); err != nil {
		return "", true, fmt.Errorf("%s must be a string", key)
	}
	return s, true, nil
}

func recordOf(raw map[string]json.RawMessage) (Record, error) {
	chain, _, err := text(raw, "chain")
	if err != nil {
		return Record{}, err
	}
	if chain == "" {
		return Record{}, errors.New("chain is required")
	}
	operator, _, err := text(raw, "operator")
	if err != nil {
		return Record{}, err
	}
	addr, err := crypto.AddressFromString(operator)
	if err != nil {
		return Record{}, errors.New("operator must be a g1 address")
	}
	atText, _, err := text(raw, "at")
	if err != nil {
		return Record{}, err
	}
	at, err := parseAt(atText)
	if err != nil {
		return Record{}, err
	}
	r := Record{Chain: chain, Operator: addr.String(), At: at}
	if v, present := raw["clear"]; present {
		if string(v) != "true" {
			return Record{}, errors.New("clear must be true or absent")
		}
		for _, k := range []string{"lat", "lon", "country", "provider"} {
			if _, held := raw[k]; held {
				return Record{}, errors.New("a removal holds no location")
			}
		}
		r.Clear = true
		return r, nil
	}
	if r.Lat, err = parseCoordinate("lat", raw["lat"], 90); err != nil {
		return Record{}, err
	}
	if r.Lon, err = parseCoordinate("lon", raw["lon"], 180); err != nil {
		return Record{}, err
	}
	country, present, err := text(raw, "country")
	if err != nil {
		return Record{}, err
	}
	if !present {
		return Record{}, errors.New("country is required")
	}
	if !ValidCountry(country) {
		return Record{}, errors.New("country must be an ISO 3166-1 alpha-2 code")
	}
	r.Country = country
	if r.Provider, _, err = text(raw, "provider"); err != nil {
		return Record{}, err
	}
	if err := checkProvider(r.Provider); err != nil {
		return Record{}, err
	}
	return r, nil
}

// The times a record's at may hold, so a stored time reads back as itself:
// from the first whole year an int64 of nanoseconds since 1970 covers, 1678,
// up to its last whole second.
var (
	firstAt = time.Date(1678, 1, 1, 0, 0, 0, 0, time.UTC)
	lastAt  = time.Date(2262, 4, 11, 23, 47, 16, 0, time.UTC)
)

func parseAt(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil || !strings.HasSuffix(s, "Z") {
		return time.Time{}, errors.New("at must be an RFC 3339 time in UTC, ending in Z")
	}
	if t.Before(firstAt) || t.After(lastAt) {
		return time.Time{}, errors.New("at must be a time between 1678 and 2262")
	}
	return t.UTC(), nil
}

func parseCoordinate(name string, v json.RawMessage, limit float64) (float64, error) {
	if v == nil {
		return 0, fmt.Errorf("%s is required", name)
	}
	if v[0] == '"' {
		return 0, fmt.Errorf("%s must be a JSON number, not a string", name)
	}
	f, err := strconv.ParseFloat(string(v), 64)
	var numErr *strconv.NumError
	if err != nil && (!errors.As(err, &numErr) || !errors.Is(numErr.Err, strconv.ErrRange)) {
		return 0, fmt.Errorf("%s must be a JSON number", name)
	}
	if err != nil || f < -limit || f > limit {
		return 0, fmt.Errorf("%s must be within -%g and %g", name, limit, limit)
	}
	if !coordinate.Match(v) {
		return 0, fmt.Errorf("%s must have at most one decimal", name)
	}
	if f == 0 {
		f = 0 // reads -0 as 0
	}
	return f, nil
}

func checkProvider(p string) error {
	if utf8.RuneCountInString(p) > MaxProvider {
		return fmt.Errorf("provider must be at most %d characters", MaxProvider)
	}
	if strings.IndexFunc(p, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0 {
		return errors.New("provider must hold no control character or format character")
	}
	// The memo is stored as signed: a provider is refused, never trimmed.
	if p != strings.TrimSpace(p) {
		return errors.New("provider must not start or end with white space")
	}
	if p != "" && ProviderKey(p) == "" {
		return errors.New("provider must hold a letter or a digit")
	}
	return nil
}

// ValidProvider reports whether a record may hold provider p, as ParseMemo
// checks it.
func ValidProvider(p string) bool { return checkProvider(p) == nil }

// ValidCountry reports whether code is one of CountryCodes().
func ValidCountry(code string) bool { return isCountryCode(code) }
