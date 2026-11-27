package web

import (
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/gnoverse/gnockpit/location"
	"github.com/gnoverse/gnockpit/node"
)

// maxRecordAhead is how far ahead of the server's clock a record's time may be.
const maxRecordAhead = 5 * time.Minute

// documentReadTimeout is how long /api/location gives a client to send its
// body, a signed document of at most 16 KB, from the start of the handler:
// ReadHeaderTimeout bounds the headers only.
const documentReadTimeout = 10 * time.Second

// documentWait returns how long /api/location gives a client to send its
// body: the server's documentTimeout, else documentReadTimeout.
func (s *Server) documentWait() time.Duration {
	if s.documentTimeout == 0 {
		return documentReadTimeout
	}
	return s.documentTimeout
}

// storedLocation is POST /api/location's answer: the declaration stored, a
// location or a removal.
type storedLocation struct {
	Operator string   `json:"operator"`
	Removed  bool     `json:"removed,omitempty"`
	Country  string   `json:"country,omitempty"`
	Lat      *float64 `json:"lat,omitempty"`
	Lon      *float64 `json:"lon,omitempty"`
	Provider string   `json:"provider,omitempty"`
	At       string   `json:"at"`
}

// declaredAt is d's time as gnockpit's answers write it: RFC 3339 in UTC, to
// the nanosecond, trailing zeros dropped.
func declaredAt(d location.Declaration) string {
	return d.At.UTC().Format(time.RFC3339Nano)
}

// storedLocationOf is d as POST /api/location answers it.
func storedLocationOf(d location.Declaration) storedLocation {
	out := storedLocation{Operator: d.Operator, At: declaredAt(d)}
	if d.Clear {
		out.Removed = true
		return out
	}
	lat, lon := d.Lat, d.Lon
	out.Country, out.Lat, out.Lon, out.Provider = d.Country, &lat, &lon, d.Provider
	return out
}

// locationChain is the chain a declaration's record must name: the block
// history's chain id, "" while it is unknown.
func (s *Server) locationChain() string {
	if s.BlockFill == nil {
		return ""
	}
	return s.BlockFill.Progress().ChainID
}

// locatesValidator reports whether peer p locates a validator by its IP: p
// has coordinates and carries a signing key (ValAddress). That key may be no
// validator's, as a reachable sentry carries its own node's: every caller
// asks it of a key in the current set, or of the key of the validator a
// report is for.
func locatesValidator(p node.Peer) bool {
	return p.ValAddress != "" && p.Lat != nil
}

// locatedPeer returns the first peer that locates the validator signing with
// key by its IP; a key "" names no validator, so none.
func locatedPeer(peers []node.Peer, key string) (node.Peer, bool) {
	i := slices.IndexFunc(peers, func(p node.Peer) bool { return p.ValAddress == key && locatesValidator(p) })
	if i < 0 {
		return node.Peer{}, false
	}
	return peers[i], true
}

// locatedKeys returns the signing keys of the validators gnockpit locates by
// their IP: those some peer with coordinates belongs to.
func locatedKeys(peers []node.Peer) map[string]bool {
	keys := map[string]bool{}
	for _, p := range peers {
		if locatesValidator(p) {
			keys[p.ValAddress] = true
		}
	}
	return keys
}

// declarability is what declarableKey reads, read once from a snapshot and
// the valopers profiles, so that one answer asks it of every operator at the
// cost of one read.
type declarability struct {
	valopers map[string]node.Valoper
	set      map[string]*int64 // currentSet: nil while the set is unknown
	located  map[string]bool   // locatedKeys
}

// newDeclarability reads once what declarableKey asks of snap and valopers:
// the current set and the signing keys gnockpit locates by IP, each built
// from the whole snapshot, so that asking it of every stored declaration
// costs one map lookup each rather than a walk of the set and the peers.
func newDeclarability(snap *node.Snapshot, valopers map[string]node.Valoper) declarability {
	d := declarability{valopers: valopers, set: currentSet(snap)}
	if snap != nil {
		d.located = locatedKeys(snap.Peers)
	}
	return d
}

// key is declarableKey on what d read.
func (d declarability) key(operator string) (string, error) {
	profile, ok := d.valopers[operator]
	if !ok {
		return "", fmt.Errorf("%s has no valopers profile", operator)
	}
	key := profile.SigningAddress
	if _, inSet := d.set[key]; !inSet {
		return "", fmt.Errorf("%s's validator is not in the current set", operator)
	}
	if d.located[key] {
		return "", fmt.Errorf("gnockpit locates %s's validator by its IP: a declared location applies only to a validator it cannot locate", operator)
	}
	return key, nil
}

// declarableKey returns the signing key of operator's validator when the
// operator may declare its location: its valopers profile's key is in the
// current set, and gnockpit does not locate that key's validator by its IP.
// Otherwise the error says why not.
func declarableKey(snap *node.Snapshot, valopers map[string]node.Valoper, operator string) (string, error) {
	return newDeclarability(snap, valopers).key(operator)
}

// shownDeclarations returns, by signing key, the declarations that show:
// each location stored on the chain, not a removal, whose operator
// declarableKey passes for in snap, its provider the listed spelling (see
// listedProviders), which a peer may list only after the declaration is
// stored. It is empty without a store or a chain id. Read on every
// snapshot, a declaration hides while gnockpit locates its validator by its
// IP and shows again once it does not.
func (s *Server) shownDeclarations(snap *node.Snapshot, valopers map[string]node.Valoper) map[string]location.Declaration {
	shown := map[string]location.Declaration{}
	chain := s.locationChain()
	if s.Locations == nil || chain == "" {
		return shown
	}
	rule := newDeclarability(snap, valopers)
	listed := s.listedProviders(snap, chain)
	for _, d := range s.Locations.All(chain) {
		if d.Clear {
			continue
		}
		if key, err := rule.key(d.Operator); err == nil {
			d.Provider = location.ListedProvider(d.Provider, listed)
			shown[key] = d
		}
	}
	return shown
}

// operatorDeclaration returns operator's declaration when it shows (nil
// otherwise), its provider the listed spelling as in shownDeclarations, and
// whether the operator may declare: declarableKey passes, with a store and a
// chain id.
func (s *Server) operatorDeclaration(snap *node.Snapshot, valopers map[string]node.Valoper, operator string) (shown *location.Declaration, declarable bool) {
	chain := s.locationChain()
	if s.Locations == nil || chain == "" {
		return nil, false
	}
	if _, err := declarableKey(snap, valopers, operator); err != nil {
		return nil, false
	}
	if d, ok := s.Locations.Get(chain, operator); ok && !d.Clear {
		d.Provider = location.ListedProvider(d.Provider, s.listedProviders(snap, chain))
		return &d, true
	}
	return nil, true
}

// listedProviders returns the providers a declared one is matched against:
// the snapshot's peers', those of the declarations stored on chain (a removal
// holds none) and OnPremise, each once, sorted. A peer's spelling a record
// refuses (an AS organization's name over MaxProvider characters, or
// holding a format character) is left out, so that a declared provider of
// its key keeps its own spelling, which the record accepted.
func (s *Server) listedProviders(snap *node.Snapshot, chain string) []string {
	listed := map[string]struct{}{location.OnPremise: {}}
	if snap != nil {
		for _, p := range snap.Peers {
			if p.Provider != "" && location.ValidProvider(p.Provider) {
				listed[p.Provider] = struct{}{}
			}
		}
	}
	if s.Locations != nil {
		for _, d := range s.Locations.All(chain) {
			if d.Provider != "" {
				listed[d.Provider] = struct{}{}
			}
		}
	}
	return slices.Sorted(maps.Keys(listed))
}

// handleLocation stores the declaration of an operator's validator location
// that the POSTed signed document carries, and answers it; or says why it
// refuses it.
func (s *Server) handleLocation(w http.ResponseWriter, r *http.Request) {
	// The body's read deadline comes first: net/http reads up to 256 KB of
	// a body the handler leaves unread before it answers, which a 405 or an
	// early 503 leave, and that read takes this deadline. A writer that
	// sets no deadline, a test's recorder, reads the body without one; on
	// a connection gone, the read fails on its own.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(s.documentWait()))
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		serveJSON(w, "/api/location", http.StatusMethodNotAllowed, errorBody{Error: "method not allowed"})
		return
	}
	stored, status, err := s.declare(w, r)
	if err != nil {
		serveJSON(w, "/api/location", status, errorBody{Error: err.Error()})
		return
	}
	serveJSON(w, "/api/location", http.StatusOK, stored)
}

// declare verifies the declaration r's body carries, checks it suits this
// gnockpit and stores it. A refusal returns its status and reason; only an
// internal failure is logged.
func (s *Server) declare(w http.ResponseWriter, r *http.Request) (storedLocation, int, error) {
	if s.Locations == nil {
		return storedLocation{}, http.StatusServiceUnavailable, errors.New("location declarations unavailable")
	}
	chain := s.locationChain()
	if chain == "" {
		return storedLocation{}, http.StatusServiceUnavailable, errors.New("chain id unknown")
	}
	doc, err := io.ReadAll(http.MaxBytesReader(w, r.Body, location.MaxDocument))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return storedLocation{}, http.StatusBadRequest, fmt.Errorf("the document is larger than %d KB", location.MaxDocument>>10)
		}
		return storedLocation{}, http.StatusBadRequest, errors.New("could not read the document")
	}
	d, err := location.Verify(doc)
	if err != nil {
		return storedLocation{}, http.StatusBadRequest, err
	}
	if d.Chain != chain {
		return storedLocation{}, http.StatusBadRequest, fmt.Errorf("this gnockpit serves chain %s, the record is for %s", chain, d.Chain)
	}
	s.mu.RLock()
	snap, valopers, valopersWhole := s.snapshot, s.valopers, s.valopersWhole
	s.mu.RUnlock()
	// What gnockpit has not read yet refuses nothing: the operator may
	// declare once it is read. An operator without a profile has none only
	// when the profiles were read whole.
	if currentSet(snap) == nil {
		return storedLocation{}, http.StatusServiceUnavailable, errors.New("validator set unknown")
	}
	if _, read := valopers[d.Operator]; !read && !valopersWhole {
		return storedLocation{}, http.StatusServiceUnavailable, errors.New("validator profiles not read yet")
	}
	if _, err := declarableKey(snap, valopers, d.Operator); err != nil {
		return storedLocation{}, http.StatusBadRequest, err
	}
	if d.At.After(s.now().Add(maxRecordAhead)) {
		return storedLocation{}, http.StatusBadRequest,
			fmt.Errorf("the record's time is more than %d minutes ahead of this gnockpit's clock", int(maxRecordAhead/time.Minute))
	}
	d.Provider = location.ListedProvider(d.Provider, s.listedProviders(snap, chain))
	if err := s.Locations.Put(r.Context(), d); err != nil {
		if errors.Is(err, location.ErrNotNewer) {
			return storedLocation{}, http.StatusBadRequest, err
		}
		log.Printf("/api/location: %v", err)
		return storedLocation{}, http.StatusInternalServerError, errors.New("internal error")
	}
	return storedLocationOf(d), http.StatusOK, nil
}
