package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/location"
	"github.com/gnoverse/gnockpit/node"
)

func TestNakamotoCoefficient(t *testing.T) {
	vals := func(powers ...string) []node.Validator {
		out := make([]node.Validator, len(powers))
		for i, p := range powers {
			out[i] = node.Validator{VotingPower: p}
		}
		return out
	}
	tests := []struct {
		name string
		vals []node.Validator
		want int
	}{
		{"one dominant", vals("10", "1", "1", "1"), 1}, // 10/13 > 1/3
		{"four equal", vals("1", "1", "1", "1"), 2},    // need 2 of 4 for >1/3
		{"three equal", vals("1", "1", "1"), 2},        // 2/3 > 1/3
		{"all zero", vals("0", "0"), 0},
		{"none above zero", vals("0", "-3"), 0},
	}
	for _, tc := range tests {
		if got := nakamotoCoefficient(tc.vals); got == nil || *got != tc.want {
			t.Errorf("%s: nakamotoCoefficient = %s, want %d", tc.name, statsCount(got), tc.want)
		}
	}
	// Unknown: no set, or a power that reads as no number, even beside powers
	// none of which is above 0.
	for name, vs := range map[string][]node.Validator{
		"no set":                 nil,
		"unreadable power":       vals("10", "lots"),
		"unreadable beside zero": vals("0", "lots"),
	} {
		if got := nakamotoCoefficient(vs); got != nil {
			t.Errorf("%s: nakamotoCoefficient = %d, want nil", name, *got)
		}
	}
}

// statsCount writes a count that may be unknown: its value, or <nil>.
func statsCount(n *int) string {
	if n == nil {
		return "<nil>"
	}
	return strconv.Itoa(*n)
}

// TestStatsNakamotoUnknown checks that /api/stats' Nakamoto coefficient is
// null while it cannot be known: before the first snapshot, while the
// snapshot has no validator set (its read failed, or every source is down),
// and while a validator's voting power reads as no number, which leaves the
// set's total unknown.
func TestStatsNakamotoUnknown(t *testing.T) {
	for _, tc := range []struct {
		name string
		snap *node.Snapshot
	}{
		{"no snapshot", nil},
		{"no set", &node.Snapshot{Error: "all sources unreachable"}},
		{"unreadable power", &node.Snapshot{Validators: []node.Validator{{Address: "g1a", VotingPower: "10"}, {Address: "g1b", VotingPower: "lots"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(buildStats(t.Context(), tc.snap, nil, nil, time.Now()))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got struct {
				Validators map[string]json.RawMessage `json:"validators"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("unmarshal %s: %v", body, err)
			}
			if v, ok := got.Validators["nakamoto_coefficient"]; !ok || string(v) != "null" {
				t.Errorf("nakamoto_coefficient = %s (present %v), want null", v, ok)
			}
		})
	}
}

// TestStatsBeforeTheFirstSnapshot checks /api/stats' answer before the
// server has a snapshot, as the README states it: the time; the validators'
// counts 0, their coefficient, windows and misses null; no providers or
// countries; the peers' counts 0.
func TestStatsBeforeTheFirstSnapshot(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	body, err := json.Marshal(buildStats(t.Context(), nil, nil, nil, now))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"time":"2026-11-18T12:00:00Z",` +
		`"validators":{"total":0,"active":0,"bft_threshold":0,"margin":0,"nakamoto_coefficient":null,"window_blocks":null,"missed":null},` +
		`"providers":null,"countries":null,"peers":{"total":0,"reachable":0}}`
	if string(body) != want {
		t.Errorf("/api/stats = %s, want %s", body, want)
	}
}

// TestStatsCountsDeclaredValidators checks that a validator with a shown
// declaration counts in its declared country and provider (none when it
// names none), with its misses of the last 24 h, in place of its matched
// peer's, the peers' counts as they are; a stored declaration of a validator
// located by its IP counts nowhere.
func TestStatsCountsDeclaredValidators(t *testing.T) {
	k1, k2 := locationAddr("K1"), locationAddr("K2")
	hetzner := ProviderStat{Provider: "Hetzner Online GmbH", Peers: 1, Validators: 1, Missed24h: 0}
	ovh := ProviderStat{Provider: "OVH SAS", Peers: 1, Validators: 0, Missed24h: 0} // K1's peer's, K1 counted elsewhere
	for _, c := range []struct {
		name      string
		provider  string // the provider A declares
		providers []ProviderStat
	}{
		{"a provider", "Equinix", []ProviderStat{hetzner, ovh, {Provider: "Equinix", Peers: 0, Validators: 1, Missed24h: 2}}},
		{"no provider", "", []ProviderStat{hetzner, ovh}},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := locationServer(t)
			declareOK(t, srv, signedLocation(t, "A", locationWith("A", c.provider), location.Legacy))
			r := locationAt("B", locationT)
			r.Country, r.Provider = "FR", "Scaleway"
			storeDeclaration(t, srv, r)
			srv.Blocks = openBlocksStore(t)
			// K1 misses 2 of the last 3 blocks, all within 24 h.
			at := func(h int64) time.Time { return seamNow.Add(time.Duration(h-4) * time.Minute) }
			putBlocks(t, srv.Blocks, at, "S", []blocks.Member{{Address: k1, Power: 10}, {Address: k2, Power: 10}}, 1, 3,
				map[int64][]string{1: {k1}, 2: {k1}})

			rec := httptest.NewRecorder()
			srv.handleStats(rec, httptest.NewRequest(http.MethodGet, "/api/stats", nil))
			var st Stats
			if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
				t.Fatalf("decode %s: %v", rec.Body, err)
			}
			wantCountries := []CountryStat{{Country: "DE", Peers: 1, Validators: 1}, {Country: "SG", Peers: 0, Validators: 1}}
			if !reflect.DeepEqual(st.Countries, wantCountries) {
				t.Errorf("countries = %+v, want %+v", st.Countries, wantCountries)
			}
			if !reflect.DeepEqual(st.Providers, c.providers) {
				t.Errorf("providers = %+v, want %+v", st.Providers, c.providers)
			}
		})
	}
}
