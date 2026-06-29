package node

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"
)

var unknownValPattern = regexp.MustCompile(`^unknown-val-(\d+)$`)

// isUnknownVal reports whether name is an "unknown-val-N" placeholder
// assigned when no real moniker could be discovered.
func isUnknownVal(name string) bool {
	return unknownValPattern.MatchString(name)
}

// NameRegistry dynamically maps validator addresses to monikers,
// discovered from /net_info peers and their /status responses.
// Persists to a JSON file so discoveries survive restarts.
type NameRegistry struct {
	mu           sync.RWMutex
	addrToName   map[string]string // validator address → moniker
	nameToAddr   map[string]string // moniker → validator address
	addrToPubKey map[string]string // validator address → base64 pubkey
	ourAddress   string
	ourMoniker   string
	persistPath  string
	lastModTime  time.Time
	GenesisTime  string // from the node's /genesis RPC
}

// NewNameRegistry creates a registry, optionally loading from a persist file.
func NewNameRegistry() *NameRegistry {
	return &NameRegistry{
		addrToName:   make(map[string]string),
		nameToAddr:   make(map[string]string),
		addrToPubKey: make(map[string]string),
	}
}

// NewNameRegistryWithPersist creates a registry that auto-saves to a file.
func NewNameRegistryWithPersist(path string) *NameRegistry {
	r := &NameRegistry{
		addrToName:   make(map[string]string),
		nameToAddr:   make(map[string]string),
		addrToPubKey: make(map[string]string),
		persistPath:  path,
	}
	r.load()
	return r
}

func (r *NameRegistry) load() {
	if r.persistPath == "" {
		return
	}
	info, err := os.Stat(r.persistPath)
	if err != nil {
		return
	}
	r.lastModTime = info.ModTime()
	data, err := os.ReadFile(r.persistPath)
	if err != nil {
		return
	}
	var m map[string]string
	if json.Unmarshal(data, &m) == nil {
		for addr, name := range m {
			r.addrToName[addr] = name
			r.nameToAddr[name] = addr
		}
	}
}

// ReloadIfChanged reloads the name registry from disk if the file has been
// modified since the last load. This allows external edits to be picked up
// automatically without a full restart.
func (r *NameRegistry) ReloadIfChanged() {
	if r.persistPath == "" {
		return
	}
	info, err := os.Stat(r.persistPath)
	if err != nil || !info.ModTime().After(r.lastModTime) {
		return
	}
	r.Reload()
}

func (r *NameRegistry) save() {
	if r.persistPath == "" {
		return
	}
	// Marshal under the read lock: callers release the write lock before calling
	// save(), and concurrent Register() calls (e.g. from QueryAllPeers goroutines)
	// would otherwise race the map read. The file write itself stays off-lock.
	r.mu.RLock()
	data, err := json.MarshalIndent(r.addrToName, "", "  ")
	r.mu.RUnlock()
	if err != nil {
		return
	}
	os.WriteFile(r.persistPath, data, 0644)
}

// SetOurs records our own validator address and moniker (from /status).
// Reset clears all cached name mappings (keeps ourAddress/ourMoniker).
func (r *NameRegistry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	our := r.ourAddress
	ourM := r.ourMoniker
	r.addrToName = make(map[string]string)
	r.nameToAddr = make(map[string]string)
	r.addrToPubKey = make(map[string]string)
	if our != "" && ourM != "" {
		r.addrToName[our] = ourM
		r.nameToAddr[ourM] = our
	}
}

// Reload clears cached name mappings, then reloads from the persist file.
func (r *NameRegistry) Reload() {
	r.Reset()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.load()
}

// SetOurs records our own validator address and moniker. The address is
// always tracked so NameWithUs works. Registry writes follow the
// same rule as Register: a real existing entry is preserved (file is
// gospel); an unknown-val-N placeholder gets upgraded to the real moniker.
func (r *NameRegistry) SetOurs(address, moniker string) {
	r.mu.Lock()
	r.ourAddress = address
	r.ourMoniker = moniker
	if address == "" || moniker == "" {
		r.mu.Unlock()
		return
	}
	existing, existed := r.addrToName[address]
	if existed && !isUnknownVal(existing) {
		r.mu.Unlock()
		return
	}
	if existed && existing == moniker {
		r.mu.Unlock()
		return
	}
	if existed {
		delete(r.nameToAddr, existing)
	}
	r.addrToName[address] = moniker
	r.nameToAddr[moniker] = address
	r.mu.Unlock()
	r.save()
}

// Register maps a validator address to a moniker. gnockpit-names.json is
// the source of truth: a real existing entry is never overwritten. An
// "unknown-val-N" placeholder, however, is upgraded to the real moniker.
func (r *NameRegistry) Register(address, moniker string) {
	if address == "" || moniker == "" {
		return
	}
	r.mu.Lock()
	existing, existed := r.addrToName[address]
	if existed && !isUnknownVal(existing) {
		r.mu.Unlock()
		return
	}
	if existed && existing == moniker {
		r.mu.Unlock()
		return
	}
	if existed {
		delete(r.nameToAddr, existing)
	}
	r.addrToName[address] = moniker
	r.nameToAddr[moniker] = address
	r.mu.Unlock()
	r.save()
}

// EnsureName guarantees that address has an entry in the registry.
// A real existing entry is preserved. An "unknown-val-N" placeholder is
// upgraded if a non-empty candidate is supplied (otherwise kept as-is to
// avoid renumbering churn). If no entry exists, the first non-empty
// candidate is used; otherwise a fresh "unknown-val-N" is assigned.
func (r *NameRegistry) EnsureName(address string, candidates ...string) {
	if address == "" {
		return
	}
	r.mu.Lock()
	existing, existed := r.addrToName[address]
	if existed && !isUnknownVal(existing) {
		r.mu.Unlock()
		return
	}
	chosen := ""
	for _, c := range candidates {
		if c != "" {
			chosen = c
			break
		}
	}
	if chosen == "" {
		if existed {
			// Already a placeholder, no candidate — leave it alone.
			r.mu.Unlock()
			return
		}
		chosen = fmt.Sprintf("unknown-val-%d", r.nextUnknownValNLocked())
	}
	if existed && existing == chosen {
		r.mu.Unlock()
		return
	}
	if existed {
		delete(r.nameToAddr, existing)
	}
	r.addrToName[address] = chosen
	r.nameToAddr[chosen] = address
	r.mu.Unlock()
	r.save()
}

// nextUnknownValNLocked returns max(N) + 1 across all "unknown-val-N"
// names currently in the registry. Caller must hold r.mu.
func (r *NameRegistry) nextUnknownValNLocked() int {
	max := 0
	for _, name := range r.addrToName {
		m := unknownValPattern.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		if n, err := strconv.Atoi(m[1]); err == nil && n > max {
			max = n
		}
	}
	return max + 1
}

// RegisterPubKey stores a validator's base64 pubkey.
func (r *NameRegistry) RegisterPubKey(address, pubkey string) {
	if address == "" || pubkey == "" {
		return
	}
	r.mu.Lock()
	r.addrToPubKey[address] = pubkey
	r.mu.Unlock()
}

// PubKey returns the base64 pubkey for a validator address, or empty string.
func (r *NameRegistry) PubKey(addr string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.addrToPubKey[addr]
}

// Name returns the moniker for a validator address, or the address itself.
func (r *NameRegistry) Name(addr string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if name, ok := r.addrToName[addr]; ok {
		return name
	}
	return addr
}

// NameWithUs returns the moniker with "(us)" suffix if it's our address.
func (r *NameRegistry) NameWithUs(addr string) string {
	name := r.Name(addr)
	r.mu.RLock()
	isOurs := addr != "" && addr == r.ourAddress
	r.mu.RUnlock()
	if isOurs {
		return name + " (us)"
	}
	return name
}

// AddrByMoniker returns the validator address for a moniker, if known.
func (r *NameRegistry) AddrByMoniker(moniker string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	addr, ok := r.nameToAddr[moniker]
	return addr, ok
}

// SeedFromValopers applies validator monikers discovered from the on-chain
// valoper registry, keyed by signing (consensus) address. Same precedence as
// the persistent registry: a real existing name is never overwritten, but an
// "unknown-val-N" placeholder is upgraded.
func (r *NameRegistry) SeedFromValopers(byAddr map[string]string) {
	if len(byAddr) == 0 {
		return
	}
	r.mu.Lock()
	changed := false
	for addr, moniker := range byAddr {
		if addr == "" || moniker == "" {
			continue
		}
		existing, existed := r.addrToName[addr]
		if existed && !isUnknownVal(existing) {
			continue
		}
		if existed && existing == moniker {
			continue
		}
		if existed {
			delete(r.nameToAddr, existing)
		}
		r.addrToName[addr] = moniker
		r.nameToAddr[moniker] = addr
		changed = true
	}
	r.mu.Unlock()
	if changed {
		r.save()
	}
}

// SeedFromGenesisStream reads the genesis time and validator names from a
// streamed /genesis RPC response, parsing only the head of the document.
// genesis_time and the validators array precede the multi-hundred-MB app_state,
// so the caller can abort the download immediately after this returns. The same
// precedence rules as the persistent registry apply: a real existing name is
// never overwritten, but an "unknown-val-N" placeholder is upgraded.
func (r *NameRegistry) SeedFromGenesisStream(rd io.Reader) error {
	dec := json.NewDecoder(rd)
	if err := enterObjectKey(dec, "result"); err != nil {
		return err
	}
	if err := enterObjectKey(dec, "genesis"); err != nil {
		return err
	}
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("expected genesis object, got %v", t)
	}

	type genesisValidator struct {
		Address string `json:"address"`
		Name    string `json:"name"`
	}
	var genesisTime string
	var validators []genesisValidator

readFields:
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		switch key, _ := kt.(string); key {
		case "genesis_time":
			vt, err := dec.Token()
			if err != nil {
				return err
			}
			// genesis_time is cosmetic (drives the UI countdown); a non-string
			// value is intentionally ignored rather than failing name seeding.
			genesisTime, _ = vt.(string)
		case "validators":
			at, err := dec.Token()
			if err != nil {
				return err
			}
			if d, ok := at.(json.Delim); !ok || d != '[' {
				return fmt.Errorf("expected validators array, got %v", at)
			}
			for dec.More() {
				var v genesisValidator
				if err := dec.Decode(&v); err != nil {
					return err
				}
				validators = append(validators, v)
			}
			// validators holds everything we need; stop before app_state.
			break readFields
		case "app_state":
			// Defensive: never consume the multi-hundred-MB app_state.
			break readFields
		default:
			if err := skipValue(dec); err != nil {
				return err
			}
		}
	}

	r.mu.Lock()
	if genesisTime != "" {
		r.GenesisTime = genesisTime
	}
	for _, v := range validators {
		if v.Address == "" || v.Name == "" {
			continue
		}
		existing, existed := r.addrToName[v.Address]
		if existed && !isUnknownVal(existing) {
			continue
		}
		if existed && existing == v.Name {
			continue
		}
		if existed {
			delete(r.nameToAddr, existing)
		}
		r.addrToName[v.Address] = v.Name
		r.nameToAddr[v.Name] = v.Address
	}
	r.mu.Unlock()
	r.save()
	return nil
}

// enterObjectKey reads an opening '{' and advances the decoder to the value of
// the given key, skipping any preceding keys. After it returns nil, the next
// decoder read consumes that key's value.
func enterObjectKey(dec *json.Decoder, key string) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("expected '{', got %v", t)
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		if k, _ := kt.(string); k == key {
			return nil
		}
		if err := skipValue(dec); err != nil {
			return err
		}
	}
	return fmt.Errorf("key %q not found", key)
}

// skipValue consumes exactly one complete JSON value (scalar, object, or array)
// from the decoder, stepping over fields not needed on the way to validators.
func skipValue(dec *json.Decoder) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := t.(json.Delim)
	if !ok {
		return nil // a scalar — already consumed
	}
	if d != '{' && d != '[' {
		return fmt.Errorf("unexpected delimiter %q", d)
	}
	for depth := 1; depth > 0; {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := t.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}
