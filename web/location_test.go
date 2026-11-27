package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/gnolang/gno/tm2/pkg/bech32"
	"github.com/gnolang/gno/tm2/pkg/crypto"
	"github.com/gnolang/gno/tm2/pkg/crypto/secp256k1"

	"github.com/gnoverse/gnockpit/location"
	"github.com/gnoverse/gnockpit/location/locationtest"
	"github.com/gnoverse/gnockpit/node"
)

// locationChainID is the chain locationServer serves.
const locationChainID = "test-chain"

// locationT is the time of the records the tests declare first: an hour
// before the servers' clock.
var locationT = seamNow.Add(-time.Hour)

// locationKey is the test key named name: an operator's (A, B, C, D) or a
// validator's signing key (K1, K2, K3).
func locationKey(name string) crypto.PrivKey { return secp256k1.GenPrivKeySecp256k1([]byte(name)) }

// locationAddr is the address of the test key named name.
func locationAddr(name string) string { return locationKey(name).PubKey().Address().String() }

// locationServer is a server on chain test-chain, its clock at seamNow, with a
// location store on an in-memory database. Its set holds K1 and K2: K2's peer
// is located by its IP, K1's is not and its provider is OVH SAS. Operator A's
// validator signs with K1, B's with K2, C's with K3, in no set; D has no
// valopers profile.
func locationServer(t *testing.T) *Server {
	t.Helper()
	return locationServerFor(t, locationAddr("A"))
}

// locationServerFor is locationServer with operatorA as operator A's address,
// for a document a key the test does not hold signs, such as gnokey's.
func locationServerFor(t *testing.T, operatorA string) *Server {
	t.Helper()
	srv := newSeamServer(t)
	srv.clock = func() time.Time { return seamNow }
	srv.BlockFill = fakeFill{ChainID: locationChainID}
	store, err := location.NewStore(t.Context(), openTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	srv.Locations = store
	k1, k2 := locationAddr("K1"), locationAddr("K2")
	lat, lon := 50.1, 8.7
	srv.setSnapshot(&node.Snapshot{
		Validators: []node.Validator{{Address: k1, VotingPower: "10"}, {Address: k2, VotingPower: "10"}},
		Peers: []node.Peer{
			{NodeID: "k1-node", ValAddress: k1, Provider: "OVH SAS"},
			{NodeID: "k2-node", ValAddress: k2, Lat: &lat, Lon: &lon, Country: "DE", Provider: "Hetzner Online GmbH"},
		},
	})
	b, c := locationAddr("B"), locationAddr("C")
	srv.valopers = map[string]node.Valoper{
		operatorA: {Operator: operatorA, SigningAddress: k1},
		b:         {Operator: b, SigningAddress: k2},
		c:         {Operator: c, SigningAddress: locationAddr("K3")},
	}
	srv.valopersWhole = true
	return srv
}

// locationAt is the location record of the operator named name at at: SG,
// 1.3, 103.8, OVH SAS.
func locationAt(name string, at time.Time) location.Record {
	return location.Record{
		Chain: locationChainID, Operator: locationAddr(name), Lat: 1.3, Lon: 103.8, Country: "SG", Provider: "OVH SAS", At: at,
	}
}

// locationWith is locationAt(name, locationT) naming provider: the display
// tests declare a provider no peer of locationServer has, or none, so the
// provider shown tells the declaration's from the peer's.
func locationWith(name, provider string) location.Record {
	r := locationAt(name, locationT)
	r.Provider = provider
	return r
}

// removalAt is the removal record of the operator named name at at.
func removalAt(name string, at time.Time) location.Record {
	return location.Record{Chain: locationChainID, Operator: locationAddr(name), Clear: true, At: at}
}

// storeDeclaration puts r in srv's store directly, its document "doc": a
// declaration stored before the state the test sets up.
func storeDeclaration(t *testing.T, srv *Server, r location.Record) {
	t.Helper()
	if err := srv.Locations.Put(t.Context(), location.Declaration{Record: r, Signed: "doc"}); err != nil {
		t.Fatal(err)
	}
}

// signedLocation is the document carrying r, signed with the key named name in
// rendering rd.
func signedLocation(t *testing.T, name string, r location.Record, rd location.Rendering) []byte {
	t.Helper()
	return locationtest.Sign(t, locationtest.Document(r.Operator, locationtest.Memo(r)), locationKey(name), location.SignChainID, rd)
}

// padded is body followed by spaces up to size bytes.
func padded(body []byte, size int) []byte {
	return append(bytes.Clone(body), bytes.Repeat([]byte(" "), size-len(body))...)
}

// serveLocation serves req with srv's /api/location handler and returns the
// answer and its JSON.
func serveLocation(t *testing.T, srv *Server, req *http.Request) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleLocation(rec, req)
	var answer map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return rec, answer
}

// postLocation POSTs body to srv's /api/location and returns the answer's
// status and JSON.
func postLocation(t *testing.T, srv *Server, body []byte) (int, map[string]any) {
	t.Helper()
	rec, answer := serveLocation(t, srv, httptest.NewRequest(http.MethodPost, "/api/location", bytes.NewReader(body)))
	return rec.Code, answer
}

// declareOK POSTs body to srv's /api/location and fails the test unless it
// answers 200.
func declareOK(t *testing.T, srv *Server, body []byte) map[string]any {
	t.Helper()
	code, answer := postLocation(t, srv, body)
	if code != http.StatusOK {
		t.Fatalf("POST /api/location = %d %v, want 200", code, answer)
	}
	return answer
}

// storesDeclaration checks that srv's store holds r as its operator's
// declaration, signed is its document.
func storesDeclaration(t *testing.T, srv *Server, r location.Record, signed []byte) {
	t.Helper()
	want := location.Declaration{Record: r, Signed: string(signed)}
	if got, ok := srv.Locations.Get(r.Chain, r.Operator); !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("Get(%s, %s) = %+v, %t; want %+v, true", r.Chain, r.Operator, got, ok, want)
	}
}

func TestLocationAPIStoresADeclaration(t *testing.T) {
	srv := locationServer(t)
	logs := captureLog(t)
	r := locationAt("A", locationT)
	body := signedLocation(t, "A", r, location.Legacy)

	code, answer := postLocation(t, srv, body)
	want := map[string]any{
		"operator": locationAddr("A"), "country": "SG", "lat": 1.3, "lon": 103.8, "provider": "OVH SAS",
		"at": "2026-11-18T11:00:00Z",
	}
	if code != http.StatusOK || !reflect.DeepEqual(answer, want) {
		t.Errorf("POST /api/location = %d %v, want 200 %v", code, answer, want)
	}
	storesDeclaration(t, srv, r, body)
	if logs.Len() != 0 {
		t.Errorf("log = %q, want nothing", logs)
	}
}

func TestLocationAPIAcceptsTheCurrentRendering(t *testing.T) {
	srv := locationServer(t)
	declareOK(t, srv, signedLocation(t, "A", locationAt("A", locationT), location.Legacy))
	r := locationAt("A", locationT.Add(time.Second))
	body := signedLocation(t, "A", r, location.Current)

	if answer := declareOK(t, srv, body); answer["at"] != "2026-11-18T11:00:01Z" {
		t.Errorf("at = %v, want 2026-11-18T11:00:01Z", answer["at"])
	}
	storesDeclaration(t, srv, r, body)
}

func TestLocationAPIAcceptsSixteenKB(t *testing.T) {
	srv := locationServer(t)
	r := locationAt("A", locationT)
	body := signedLocation(t, "A", r, location.Legacy)

	declareOK(t, srv, padded(body, location.MaxDocument))
	// The store keeps the document's canonical form, without the padding.
	storesDeclaration(t, srv, r, body)
}

func TestLocationAPIRefuses(t *testing.T) {
	opA, opB, opC, opD := locationAddr("A"), locationAddr("B"), locationAddr("C"), locationAddr("D")
	locatedByIP := "gnockpit locates " + opB + "'s validator by its IP: a declared location applies only to a validator it cannot locate"
	cases := []struct {
		name string
		body func(t *testing.T, srv *Server) []byte
		want string
		kept int // declarations the store holds after the refusal
	}{{
		name: "a record for another chain",
		body: func(t *testing.T, _ *Server) []byte {
			r := locationAt("A", locationT)
			r.Chain = "gnoland1"
			return signedLocation(t, "A", r, location.Legacy)
		},
		want: "this gnockpit serves chain test-chain, the record is for gnoland1",
	}, {
		name: "an operator without a valopers profile",
		body: func(t *testing.T, _ *Server) []byte {
			return signedLocation(t, "D", locationAt("D", locationT), location.Legacy)
		},
		want: opD + " has no valopers profile",
	}, {
		name: "an operator whose key is in no set",
		body: func(t *testing.T, _ *Server) []byte {
			return signedLocation(t, "C", locationAt("C", locationT), location.Legacy)
		},
		want: opC + "'s validator is not in the current set",
	}, {
		name: "a location of a validator located by its IP",
		body: func(t *testing.T, _ *Server) []byte {
			return signedLocation(t, "B", locationAt("B", locationT), location.Legacy)
		},
		want: locatedByIP,
	}, {
		name: "a removal of a validator located by its IP",
		body: func(t *testing.T, _ *Server) []byte {
			return signedLocation(t, "B", removalAt("B", locationT), location.Legacy)
		},
		want: locatedByIP,
	}, {
		name: "a time more than 5 minutes ahead",
		body: func(t *testing.T, _ *Server) []byte {
			return signedLocation(t, "A", locationAt("A", seamNow.Add(5*time.Minute+time.Second)), location.Legacy)
		},
		want: "the record's time is more than 5 minutes ahead of this gnockpit's clock",
	}, {
		name: "the same document twice",
		body: func(t *testing.T, srv *Server) []byte {
			body := signedLocation(t, "A", locationAt("A", locationT), location.Legacy)
			declareOK(t, srv, body)
			return body
		},
		want: "a declaration as recent or more recent is stored for " + opA + " (at 2026-11-18T11:00:00Z)",
		kept: 1,
	}, {
		name: "a tampered memo",
		body: func(t *testing.T, _ *Server) []byte {
			body := signedLocation(t, "A", locationAt("A", locationT), location.Legacy)
			tampered := bytes.Replace(body, []byte("103.8"), []byte("103.9"), 1)
			if bytes.Equal(tampered, body) {
				t.Fatalf("no 103.8 to tamper with in %s", body)
			}
			return tampered
		},
		want: "the signature does not verify: sign with gnokey sign -chainid gnockpit-location -account-number 0 -account-sequence 0",
	}, {
		name: "a record whose operator is an address of 21 bytes",
		body: func(t *testing.T, _ *Server) []byte {
			operator, err := bech32.ConvertAndEncode(crypto.Bech32AddrPrefix(), make([]byte, 21))
			if err != nil {
				t.Fatal(err)
			}
			r := locationAt("A", locationT)
			r.Operator = operator
			return locationtest.Sign(t, locationtest.Document(opA, locationtest.Memo(r)), locationKey("A"), location.SignChainID, location.Legacy)
		},
		want: "the record: operator must be a g1 address",
	}, {
		name: "a body of 16 KB + 1",
		body: func(t *testing.T, _ *Server) []byte {
			body := signedLocation(t, "A", locationAt("A", locationT), location.Legacy)
			return padded(body, location.MaxDocument+1)
		},
		want: "the document is larger than 16 KB",
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := locationServer(t)
			body := c.body(t, srv)
			logs := captureLog(t)

			code, answer := postLocation(t, srv, body)
			if want := map[string]any{"error": c.want}; code != http.StatusBadRequest || !reflect.DeepEqual(answer, want) {
				t.Errorf("POST /api/location = %d %v, want 400 %v", code, answer, want)
			}
			if got := srv.Locations.All(locationChainID); len(got) != c.kept {
				t.Errorf("the store holds %+v after the refusal, want %d declarations", got, c.kept)
			}
			if logs.Len() != 0 {
				t.Errorf("log = %q, want nothing: a refusal is no internal failure", logs)
			}
		})
	}
}

func TestLocationAPIAcceptsFiveMinutesAhead(t *testing.T) {
	srv := locationServer(t)
	answer := declareOK(t, srv, signedLocation(t, "A", locationAt("A", seamNow.Add(5*time.Minute)), location.Legacy))
	if answer["at"] != "2026-11-18T12:05:00Z" {
		t.Errorf("at = %v, want 2026-11-18T12:05:00Z", answer["at"])
	}
}

func TestLocationAPIAnswersAFractionalAt(t *testing.T) {
	srv := locationServer(t)
	r := locationAt("A", locationT.Add(500*time.Millisecond))
	memo := strings.Replace(locationtest.Memo(r), `"at":"2026-11-18T11:00:00Z"`, `"at":"2026-11-18T11:00:00.5Z"`, 1)
	if !strings.Contains(memo, "11:00:00.5Z") {
		t.Fatalf("no at to make fractional in %s", memo)
	}
	body := locationtest.Sign(t, locationtest.Document(r.Operator, memo), locationKey("A"), location.SignChainID, location.Legacy)

	if answer := declareOK(t, srv, body); answer["at"] != "2026-11-18T11:00:00.5Z" {
		t.Errorf("at = %v, want 2026-11-18T11:00:00.5Z", answer["at"])
	}
	storesDeclaration(t, srv, r, body)
}

func TestLocationAPIRemoval(t *testing.T) {
	srv := locationServer(t)
	declareOK(t, srv, signedLocation(t, "A", locationAt("A", locationT), location.Legacy))
	r := removalAt("A", locationT.Add(time.Second))
	body := signedLocation(t, "A", r, location.Legacy)

	code, answer := postLocation(t, srv, body)
	want := map[string]any{"operator": locationAddr("A"), "removed": true, "at": "2026-11-18T11:00:01Z"}
	if code != http.StatusOK || !reflect.DeepEqual(answer, want) {
		t.Errorf("POST /api/location = %d %v, want 200 %v", code, answer, want)
	}
	storesDeclaration(t, srv, r, body)
}

// softHyphenProvider and longProvider are peers' providers a record refuses,
// as an AS organization's name may be: one holding a soft hyphen, a format
// character, and one over 64 characters. "Soft Hyphen Net" and "Acme
// Hosting" have their keys.
var (
	softHyphenProvider = "Soft\u00adHyphen Net"
	longProvider       = "Acme " + strings.Repeat("- ", 30) + "Hosting"
)

func TestLocationAPIStoresTheListedProvider(t *testing.T) {
	for _, c := range []struct {
		typed  string
		stored string // another operator's stored declaration's provider, if any
		peer   string // a peer's provider, if any
		want   string
	}{
		{typed: "ovh sas", want: "OVH SAS"},
		{typed: "O.V.H. SAS", want: "OVH SAS"},
		{typed: "On Premise", want: location.OnPremise},
		{typed: "Acme Cloud", want: "Acme Cloud"}, // listed nowhere: kept as written
		{typed: "acme cloud", stored: "Acme Cloud", want: "Acme Cloud"},
		{typed: "", want: ""},
		// A peer's spelling a record refuses is not listed: the provider
		// keeps its own.
		{typed: "Soft Hyphen Net", peer: softHyphenProvider, want: "Soft Hyphen Net"},
		{typed: "Acme Hosting", peer: longProvider, want: "Acme Hosting"},
	} {
		name := strconv.Quote(c.typed)
		if c.stored != "" {
			name += ", " + strconv.Quote(c.stored) + " stored"
		}
		if c.peer != "" {
			name += ", " + strconv.Quote(c.peer) + " a peer's"
		}
		t.Run(name, func(t *testing.T) {
			srv := locationServer(t)
			if c.peer != "" {
				snap := *srv.getSnapshot()
				snap.Peers = append(slices.Clone(snap.Peers), node.Peer{NodeID: "provider-node", Provider: c.peer})
				srv.setSnapshot(&snap)
			}
			if c.stored != "" {
				other := locationAt("E", locationT)
				other.Provider = c.stored
				storeDeclaration(t, srv, other)
			}
			r := locationAt("A", locationT)
			r.Provider = c.typed
			answer := declareOK(t, srv, signedLocation(t, "A", r, location.Legacy))
			if got, _ := answer["provider"].(string); got != c.want {
				t.Errorf("provider = %q, want %q", got, c.want)
			}
			if d, _ := srv.Locations.Get(locationChainID, r.Operator); d.Provider != c.want {
				t.Errorf("the stored provider = %q, want %q", d.Provider, c.want)
			}
		})
	}
}

func TestListedProviders(t *testing.T) {
	srv := locationServer(t)
	put := func(chain, name, provider string) {
		t.Helper()
		r := locationAt(name, locationT)
		r.Chain, r.Provider = chain, provider
		storeDeclaration(t, srv, r)
	}
	put(locationChainID, "E", "Acme Cloud")
	put(locationChainID, "F", "OVH SAS") // a peer's too: listed once
	put(locationChainID, "G", "")        // no provider: nothing listed
	// A removal holds no provider: the provider of the declaration it
	// replaces is not listed.
	put(locationChainID, "H", "Gone Hosting")
	storeDeclaration(t, srv, removalAt("H", locationT.Add(time.Second)))
	put("other-chain", "E", "Other Chain Hosting")
	snap := *srv.getSnapshot()
	snap.Peers = append(slices.Clone(snap.Peers),
		node.Peer{NodeID: "unknown-provider-node"}, // no provider: nothing listed
		// Spellings a record refuses: not listed.
		node.Peer{NodeID: "soft-hyphen-node", Provider: softHyphenProvider},
		node.Peer{NodeID: "long-name-node", Provider: longProvider},
	)

	want := []string{"Acme Cloud", "Hetzner Online GmbH", "OVH SAS", location.OnPremise}
	if got := srv.listedProviders(&snap, locationChainID); !reflect.DeepEqual(got, want) {
		t.Errorf("listedProviders = %q, want %q", got, want)
	}

	srv.Locations = nil
	if got := srv.listedProviders(nil, locationChainID); !reflect.DeepEqual(got, []string{location.OnPremise}) {
		t.Errorf("listedProviders without a snapshot or a store = %q, want [%s]", got, location.OnPremise)
	}
}

func TestLocatedKeys(t *testing.T) {
	lat, lon := 1.0, 2.0
	located := node.Peer{ValAddress: "k", Lat: &lat, Lon: &lon}
	unlocated := node.Peer{ValAddress: "k", Provider: "OVH SAS"}
	otherLocated := node.Peer{ValAddress: "other", Lat: &lat, Lon: &lon}
	sentryLocated := node.Peer{NodeID: "sentry", Lat: &lat, Lon: &lon} // a peer of no validator
	for _, c := range []struct {
		name  string
		peers []node.Peer
		want  map[string]bool
	}{
		{"no peer", nil, map[string]bool{}},
		{"its peer unlocated", []node.Peer{unlocated}, map[string]bool{}},
		{"another key's peer located", []node.Peer{otherLocated, unlocated}, map[string]bool{"other": true}},
		{"its peer located", []node.Peer{located}, map[string]bool{"k": true}},
		{"one of its peers located", []node.Peer{located, unlocated}, map[string]bool{"k": true}},
		{"its other peer located", []node.Peer{unlocated, located}, map[string]bool{"k": true}},
		{"a located peer of no validator", []node.Peer{sentryLocated}, map[string]bool{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := locatedKeys(c.peers); !reflect.DeepEqual(got, c.want) {
				t.Errorf("locatedKeys = %v, want %v", got, c.want)
			}
		})
	}
}

func TestLocationAPIMethod(t *testing.T) {
	rec, answer := serveLocation(t, locationServer(t), httptest.NewRequest(http.MethodGet, "/api/location", nil))
	if want := map[string]any{"error": "method not allowed"}; rec.Code != http.StatusMethodNotAllowed || !reflect.DeepEqual(answer, want) {
		t.Errorf("GET /api/location = %d %v, want 405 %v", rec.Code, answer, want)
	}
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Errorf("Allow = %q, want POST", got)
	}
}

func TestLocationAPIUnavailable(t *testing.T) {
	body := signedLocation(t, "A", locationAt("A", locationT), location.Legacy)
	for _, c := range []struct {
		name  string
		unset func(srv *Server)
		want  string
	}{
		{"no store", func(srv *Server) { srv.Locations = nil }, "location declarations unavailable"},
		{"no block history", func(srv *Server) { srv.BlockFill = nil }, "chain id unknown"},
		{"no chain id", func(srv *Server) { srv.BlockFill = fakeFill{} }, "chain id unknown"},
		{"no snapshot", func(srv *Server) { srv.setSnapshot(nil) }, "validator set unknown"},
		{"every source down", func(srv *Server) { srv.setSnapshot(&node.Snapshot{Error: "all sources unreachable"}) }, "validator set unknown"},
		{"no valopers profile read", func(srv *Server) { srv.valopers, srv.valopersWhole = nil, false }, "validator profiles not read yet"},
		{"a partial profiles read without the operator's", func(srv *Server) {
			delete(srv.valopers, locationAddr("A"))
			srv.valopersWhole = false
		}, "validator profiles not read yet"},
		{"neither the set nor the profiles read", func(srv *Server) {
			srv.setSnapshot(nil)
			srv.valopers, srv.valopersWhole = nil, false
		}, "validator set unknown"},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := locationServer(t)
			c.unset(srv)
			code, answer := postLocation(t, srv, body)
			if want := map[string]any{"error": c.want}; code != http.StatusServiceUnavailable || !reflect.DeepEqual(answer, want) {
				t.Errorf("POST /api/location = %d %v, want 503 %v", code, answer, want)
			}
		})
	}
}

func TestLocationAPIAcceptsOnAPartialProfilesRead(t *testing.T) {
	srv := locationServer(t)
	srv.valopersWhole = false // A's profile read, some other one missed
	declareOK(t, srv, signedLocation(t, "A", locationAt("A", locationT), location.Legacy))
}

func TestLocationAPIUnreadableBody(t *testing.T) {
	srv := locationServer(t)
	logs := captureLog(t)
	rec, answer := serveLocation(t, srv, httptest.NewRequest(http.MethodPost, "/api/location", iotest.ErrReader(errors.New("connection reset"))))
	if want := map[string]any{"error": "could not read the document"}; rec.Code != http.StatusBadRequest || !reflect.DeepEqual(answer, want) {
		t.Errorf("POST /api/location = %d %v, want 400 %v", rec.Code, answer, want)
	}
	if logs.Len() != 0 {
		t.Errorf("log = %q, want nothing: a refusal is no internal failure", logs)
	}
}

// TestLocationAPIBoundsTheDocumentRead checks that a client sending the
// headers and half the document, then nothing, gets its answer once the
// server gives up the read, the server's deadline shortened: a refusal,
// nothing logged and nothing stored.
func TestLocationAPIBoundsTheDocumentRead(t *testing.T) {
	srv := locationServer(t)
	srv.documentTimeout = 100 * time.Millisecond
	logs := captureLog(t)
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)
	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	body := signedLocation(t, "A", locationAt("A", locationT), location.Legacy)
	head := fmt.Sprintf("POST /api/location HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\n\r\n", ts.Listener.Addr(), len(body))
	if _, err := io.WriteString(conn, head); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(body[:len(body)/2]); err != nil {
		t.Fatal(err)
	}
	// The client's own bound, far past the server's, fails the test rather
	// than hold it when the server waits for the rest.
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read the answer to a document sent in half: %v", err)
	}
	defer res.Body.Close()
	var answer map[string]any
	if err := json.NewDecoder(res.Body).Decode(&answer); err != nil {
		t.Fatalf("decode the answer: %v", err)
	}
	if want := map[string]any{"error": "could not read the document"}; res.StatusCode != http.StatusBadRequest || !reflect.DeepEqual(answer, want) {
		t.Errorf("POST /api/location of half a document = %d %v, want 400 %v", res.StatusCode, answer, want)
	}
	if got := srv.Locations.All(locationChainID); len(got) != 0 {
		t.Errorf("the store holds %+v, want nothing", got)
	}
	if logs.Len() != 0 {
		t.Errorf("log = %q, want nothing: a client too slow is no internal failure", logs)
	}
}

// TestLocationAPIBoundsTheBodyOfAnEarlyAnswer checks a client that sends the
// headers and half a body, then nothing, to a request /api/location answers
// without reading it: another method (405), or a POST while the chain id is
// unknown (503). net/http reads up to 256 KB of a body the handler left
// unread before it answers; the read deadline, the handler's first step,
// bounds that read too, the server's deadline shortened: the answer comes,
// then the connection closes, the rest of the body never read.
func TestLocationAPIBoundsTheBodyOfAnEarlyAnswer(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		unready      func(srv *Server)
		want         int
	}{
		{"another method", http.MethodPut, func(*Server) {}, http.StatusMethodNotAllowed},
		{"chain id unknown", http.MethodPost, func(srv *Server) { srv.BlockFill = fakeFill{} }, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := locationServer(t)
			srv.documentTimeout = 100 * time.Millisecond
			tc.unready(srv)
			ts := httptest.NewServer(srv.routes())
			t.Cleanup(ts.Close)
			conn, err := net.Dial("tcp", ts.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()

			body := signedLocation(t, "A", locationAt("A", locationT), location.Legacy)
			head := fmt.Sprintf("%s /api/location HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\n\r\n", tc.method, ts.Listener.Addr(), len(body))
			if _, err := io.WriteString(conn, head); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Write(body[:len(body)/2]); err != nil {
				t.Fatal(err)
			}
			// The client's own bound, far past the server's, fails the test
			// rather than hold it when the server waits for the rest.
			if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			r := bufio.NewReader(conn)
			res, err := http.ReadResponse(r, nil)
			if err != nil {
				t.Fatalf("read the answer to %s with half a body: %v", tc.method, err)
			}
			if _, err := io.Copy(io.Discard, res.Body); err != nil {
				t.Fatalf("read the answer's body: %v", err)
			}
			if err := res.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tc.want {
				t.Errorf("%s /api/location with half a body: status %d, want %d", tc.method, res.StatusCode, tc.want)
			}
			if _, err := r.ReadByte(); !errors.Is(err, io.EOF) {
				t.Errorf("the connection after the answer: read %v, want it closed (EOF)", err)
			}
		})
	}
}

// TestDocumentWait checks how long /api/location gives a client to send its
// body: documentReadTimeout, unless the server sets its own.
func TestDocumentWait(t *testing.T) {
	srv := newSeamServer(t)
	if got := srv.documentWait(); got != documentReadTimeout {
		t.Errorf("documentWait() = %s, want %s", got, documentReadTimeout)
	}
	srv.documentTimeout = time.Second
	if got := srv.documentWait(); got != time.Second {
		t.Errorf("documentWait() with the server's own = %s, want 1s", got)
	}
}

// TestLocationAPIStoresForAClientThatLeft checks that a client hanging up once
// its document is read neither stops the store's write nor counts as an
// internal failure.
func TestLocationAPIStoresForAClientThatLeft(t *testing.T) {
	srv := locationServer(t)
	logs := captureLog(t)
	r := locationAt("A", locationT)
	body := signedLocation(t, "A", r, location.Legacy)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rec, answer := serveLocation(t, srv, httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/location", bytes.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Errorf("POST /api/location = %d %v, want 200", rec.Code, answer)
	}
	storesDeclaration(t, srv, r, body)
	if logs.Len() != 0 {
		t.Errorf("log = %q, want nothing", logs)
	}
}

func TestLocationAPIInternalError(t *testing.T) {
	srv := locationServer(t)
	db := openTestDB(t)
	store, err := location.NewStore(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	srv.Locations = store
	if _, err := db.Exec(`DROP TABLE location_declarations`); err != nil {
		t.Fatal(err)
	}
	logs := captureLog(t)

	code, answer := postLocation(t, srv, signedLocation(t, "A", locationAt("A", locationT), location.Legacy))
	if want := map[string]any{"error": "internal error"}; code != http.StatusInternalServerError || !reflect.DeepEqual(answer, want) {
		t.Errorf("POST /api/location = %d %v, want 500 %v", code, answer, want)
	}
	if got := logs.String(); !strings.Contains(got, locationAddr("A")) || !strings.Contains(got, "no such table: location_declarations") {
		t.Errorf("log = %q, want the operator and the store's failure", got)
	}
}

func TestLiveDataDeclared(t *testing.T) {
	srv := locationServer(t)
	k1 := locationAddr("K1")
	srv.Client.Names.Register(k1, "alpha")
	declared := func() string {
		t.Helper()
		return string(wsMsgParts(t, srv.buildUpdateMsg(srv.getSnapshot()))["declared"])
	}
	if got := declared(); got != "{}" {
		t.Errorf("declared without a declaration = %s, want {}", got)
	}

	declareOK(t, srv, signedLocation(t, "A", locationWith("A", "Equinix"), location.Legacy))
	storeDeclaration(t, srv, locationAt("B", locationT)) // B's validator is located by its IP
	want := map[string]declaredNode{k1: {
		Name: "alpha", Operator: locationAddr("A"), Country: "SG", Lat: 1.3, Lon: 103.8, Provider: "Equinix",
		At: "2026-11-18T11:00:00Z",
	}}
	if got := srv.buildLiveData(srv.getSnapshot()).Declared; !reflect.DeepEqual(got, want) {
		t.Errorf("declared = %+v, want %+v", got, want)
	}

	declareOK(t, srv, signedLocation(t, "A", removalAt("A", locationT.Add(time.Second)), location.Legacy))
	if got := declared(); got != "{}" {
		t.Errorf("declared after A's removal = %s, want {}", got)
	}
}

// TestLiveDataDeclaredWithoutAProvider checks that a declaration naming no
// provider shows with none, its peer's provider aside: /ws's entry has no
// provider key.
func TestLiveDataDeclaredWithoutAProvider(t *testing.T) {
	srv := locationServer(t)
	k1, opA := locationAddr("K1"), locationAddr("A")
	srv.Client.Names.Register(k1, "alpha")
	declareOK(t, srv, signedLocation(t, "A", locationWith("A", ""), location.Legacy))

	want := `{"` + k1 + `":{"name":"alpha","operator":"` + opA + `","country":"SG","lat":1.3,"lon":103.8,"at":"2026-11-18T11:00:00Z"}}`
	if got := string(wsMsgParts(t, srv.buildUpdateMsg(srv.getSnapshot()))["declared"]); got != want {
		t.Errorf("declared = %s, want %s", got, want)
	}
}

// TestDeclaredShowsNothingUnread checks that a declaration shows nowhere while
// what declarableKey reads is unknown, and without a store or a chain id.
func TestDeclaredShowsNothingUnread(t *testing.T) {
	for _, c := range []struct {
		name  string
		unset func(srv *Server)
	}{
		{"no store", func(srv *Server) { srv.Locations = nil }},
		{"no block history", func(srv *Server) { srv.BlockFill = nil }},
		{"no chain id", func(srv *Server) { srv.BlockFill = fakeFill{} }},
		{"no snapshot", func(srv *Server) { srv.setSnapshot(nil) }},
		{"every source down", func(srv *Server) { srv.setSnapshot(&node.Snapshot{Error: "all sources unreachable"}) }},
		{"no valopers profile read", func(srv *Server) { srv.valopers, srv.valopersWhole = nil, false }},
		{"a partial profiles read without the operator's", func(srv *Server) {
			delete(srv.valopers, locationAddr("A"))
			srv.valopersWhole = false
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := locationServer(t)
			declareOK(t, srv, signedLocation(t, "A", locationAt("A", locationT), location.Legacy))
			c.unset(srv)
			if got := srv.shownDeclarations(srv.getSnapshot(), srv.valopers); len(got) != 0 {
				t.Errorf("shownDeclarations = %+v, want none", got)
			}
		})
	}
}

// TestDeclaredTakesTheProviderListedSince checks that a declaration shows the
// listed spelling of its provider, which a peer may list only after the
// declaration is stored: "ovh sas", declared while no peer lists OVH SAS,
// shows as K1's peer's OVH SAS once listed, in /ws, /api/status,
// /api/stats (one provider, its peer and its validator) and /api/validator.
func TestDeclaredTakesTheProviderListedSince(t *testing.T) {
	srv := locationReportServer(t)
	k1, k2 := locationAddr("K1"), locationAddr("K2")
	listed := *srv.getSnapshot()
	listed.Consensus = &node.ConsensusState{Votes: []node.VoteInfo{{Address: k1}, {Address: k2}}}
	unlisted := listed
	unlisted.Peers = slices.Clone(listed.Peers)
	unlisted.Peers[0].Provider = "" // K1's peer, its provider unread
	srv.setSnapshot(&unlisted)
	if answer := declareOK(t, srv, signedLocation(t, "A", locationWith("A", "ovh sas"), location.Legacy)); answer["provider"] != "ovh sas" {
		t.Fatalf("provider stored = %v, want ovh sas: no peer lists OVH SAS", answer["provider"])
	}
	srv.setSnapshot(&listed)

	if got := srv.buildLiveData(&listed).Declared[k1].Provider; got != "OVH SAS" {
		t.Errorf("/ws declared[K1].provider = %q, want OVH SAS", got)
	}

	var status string
	for _, v := range srv.buildStatusReport(&listed, seamNow).Validators {
		if v.Address == k1 {
			status = v.Provider
		}
	}
	if status != "OVH SAS" {
		t.Errorf("/api/status K1's provider = %q, want OVH SAS", status)
	}

	rec := httptest.NewRecorder()
	srv.handleStats(rec, httptest.NewRequest(http.MethodGet, "/api/stats", nil))
	var st Stats
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	var ovh []ProviderStat
	for _, p := range st.Providers {
		if location.ProviderKey(p.Provider) == "ovhsas" {
			ovh = append(ovh, p)
		}
	}
	if len(ovh) != 1 || ovh[0].Provider != "OVH SAS" || ovh[0].Peers != 1 || ovh[0].Validators != 1 {
		t.Errorf("/api/stats OVH providers = %+v, want one OVH SAS of 1 peer and 1 validator", ovh)
	}

	_, body := getValidator(t, srv, "?val="+locationAddr("A"))
	if loc, _ := body["location"].(map[string]any); loc["provider"] != "OVH SAS" || loc["source"] != "declared" {
		t.Errorf("/api/validator A's location = %v, want the declared one at OVH SAS", body["location"])
	}
}

func TestDeclaredShowsAgainWithoutTheIPLocation(t *testing.T) {
	srv := locationServer(t)
	k2 := locationAddr("K2")
	storeDeclaration(t, srv, locationAt("B", locationT))
	if got, ok := srv.buildLiveData(srv.getSnapshot()).Declared[k2]; ok {
		t.Errorf("declared[K2] = %+v while its peer is located by its IP, want none", got)
	}

	// The GeoIP database not loaded yet: the same peer, unlocated.
	snap := *srv.getSnapshot()
	snap.Peers = slices.Clone(snap.Peers)
	snap.Peers[1].Lat, snap.Peers[1].Lon, snap.Peers[1].Country = nil, nil, ""
	if got, ok := srv.buildLiveData(&snap).Declared[k2]; !ok || got.Operator != locationAddr("B") {
		t.Errorf("declared[K2] = %+v, %t without its IP location, want B's", got, ok)
	}
}

func TestDeclaredFollowsTheProfileKey(t *testing.T) {
	srv := locationServer(t)
	declareOK(t, srv, signedLocation(t, "A", locationAt("A", locationT), location.Legacy))
	k1, k3 := locationAddr("K1"), locationAddr("K3")
	// A rotates to K3, which joins the set; C's profile goes, a signing key
	// being one operator's.
	opA := locationAddr("A")
	srv.valopers[opA] = node.Valoper{Operator: opA, SigningAddress: k3}
	delete(srv.valopers, locationAddr("C"))
	snap := *srv.getSnapshot()
	snap.Validators = append(slices.Clone(snap.Validators), node.Validator{Address: k3, VotingPower: "10"})

	declared := srv.buildLiveData(&snap).Declared
	if got, ok := declared[k3]; !ok || got.Operator != opA {
		t.Errorf("declared[K3] = %+v, %t, want A's", got, ok)
	}
	if got, ok := declared[k1]; ok {
		t.Errorf("declared[K1] = %+v, want none: A signs with K3", got)
	}
}
