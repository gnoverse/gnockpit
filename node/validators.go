package node

import (
	"encoding/json"
	"os"
	"sync"
)

// NameRegistry dynamically maps validator addresses to monikers,
// discovered from /net_info peers and their /status responses.
// Persists to a JSON file so discoveries survive restarts.
type NameRegistry struct {
	mu          sync.RWMutex
	addrToName  map[string]string // validator address → moniker
	nameToAddr  map[string]string // moniker → validator address
	addrToPubKey map[string]string // validator address → base64 pubkey
	ourAddress  string
	ourMoniker  string
	persistPath string
	GenesisTime string // from genesis.json
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

func (r *NameRegistry) save() {
	if r.persistPath == "" {
		return
	}
	data, err := json.MarshalIndent(r.addrToName, "", "  ")
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

func (r *NameRegistry) SetOurs(address, moniker string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ourAddress = address
	r.ourMoniker = moniker
	if address != "" && moniker != "" {
		r.addrToName[address] = moniker
		r.nameToAddr[moniker] = address
	}
}

// Register maps a validator address to a moniker.
// Called when we discover a peer's validator address via their RPC /status.
func (r *NameRegistry) Register(address, moniker string) {
	if address == "" || moniker == "" {
		return
	}
	r.mu.Lock()
	_, existed := r.addrToName[address]
	r.addrToName[address] = moniker
	r.nameToAddr[moniker] = address
	r.mu.Unlock()
	if !existed {
		r.save()
	}
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

// IsOurs returns true if the address is our validator.
func (r *NameRegistry) IsOurs(addr string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return addr != "" && addr == r.ourAddress
}

// AddrByMoniker returns the validator address for a moniker, if known.
func (r *NameRegistry) AddrByMoniker(moniker string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	addr, ok := r.nameToAddr[moniker]
	return addr, ok
}

// IsKnownMoniker returns true if we've seen this moniker as a validator.
func (r *NameRegistry) IsKnownMoniker(moniker string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.nameToAddr[moniker]
	return ok
}

// OurAddress returns our validator address.
func (r *NameRegistry) OurAddress() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ourAddress
}

// SeedFromGenesis reads validator names from a genesis.json file.
// The genesis contains validators with "address" and "name" fields.
func (r *NameRegistry) SeedFromGenesis(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Parse just the validators array — genesis is huge, only unmarshal what we need
	var genesis struct {
		GenesisTime string `json:"genesis_time"`
		Validators  []struct {
			Address string `json:"address"`
			Name    string `json:"name"`
		} `json:"validators"`
	}
	if err := json.Unmarshal(data, &genesis); err != nil {
		return err
	}
	r.mu.Lock()
	if genesis.GenesisTime != "" {
		r.GenesisTime = genesis.GenesisTime
	}
	for _, v := range genesis.Validators {
		if v.Address != "" && v.Name != "" {
			r.addrToName[v.Address] = v.Name
			r.nameToAddr[v.Name] = v.Address
		}
	}
	r.mu.Unlock()
	r.save()
	return nil
}
