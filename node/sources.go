package node

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sources is an ordered set of RPC endpoints queried together. Global chain data
// comes from the best (freshest reachable) endpoint each cycle; peer data is
// unioned across all reachable endpoints. Endpoint order defines merge
// precedence. Reachability transitions are logged (to stderr via logf), never
// surfaced in the UI.
type Sources struct {
	clients   []*Client
	labels    []string // RPC URLs, for logging
	sourceIPs []string // public IP of each endpoint from its RPC URL, or ""
	egressIP  string   // gnockpit's own public egress IP, last-resort fill for co-located sources
	logf      func(string, ...any)

	mu      sync.Mutex
	seen    map[int]bool    // whether endpoint i has been polled yet
	up      map[int]bool    // last-known reachability of endpoint i
	nodeIDs map[string]bool // node IDs of our own endpoints (for the Source flag)
}

// NewSources builds a Sources over the given RPC URLs (in precedence order),
// each backed by a Client sharing the given name registry. logf (may be nil) is
// called on reachability transitions.
func NewSources(rpcs []string, timeout time.Duration, names *NameRegistry, logf func(string, ...any)) *Sources {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Sources{
		labels:  rpcs,
		logf:    logf,
		seen:    make(map[int]bool),
		up:      make(map[int]bool),
		nodeIDs: make(map[string]bool),
	}
	for _, rpc := range rpcs {
		c := NewClient(rpc, timeout)
		c.Names = names
		s.clients = append(s.clients, c)
		s.sourceIPs = append(s.sourceIPs, rpcHostPublicIP(rpc))
	}
	// When an endpoint's RPC URL has no public IP (localhost or a private
	// address), fall back to gnockpit's own public egress IP for that source's
	// map location — correct when gnockpit is co-located with the node.
	for _, ip := range s.sourceIPs {
		if ip == "" {
			s.egressIP = publicEgressIP(egressLookupTimeout)
			break
		}
	}
	return s
}

// egressLookupTimeout bounds the one-shot startup call to the public-IP echo
// services; failure is non-fatal and simply leaves egressIP empty.
const egressLookupTimeout = 5 * time.Second

// publicEgressIP returns this host's public IP as seen from the internet by
// querying public echo services in order, or "" if none return a public IP in
// time.
func publicEgressIP(timeout time.Duration) string {
	client := &http.Client{Timeout: timeout}
	for _, url := range []string{"https://checkip.amazonaws.com", "https://api.ipify.org"} {
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		if err != nil {
			continue
		}
		if ip := strings.TrimSpace(string(body)); IsPublicIP(ip) {
			return ip
		}
	}
	return ""
}

// FillSourceEgressIP assigns gnockpit's own public egress IP to any configured
// source peer that still lacks a public IP (typically a localhost or private
// endpoint that no other source observed). Last resort, below both the RPC-URL
// IP and any IP observed for the node by another source.
func (s *Sources) FillSourceEgressIP(peers []Peer) {
	if s.egressIP == "" {
		return
	}
	for i := range peers {
		if peers[i].Source && peers[i].RemoteIP == "" {
			peers[i].RemoteIP = s.egressIP
		}
	}
}

// rpcHostPublicIP returns the public IP of an RPC URL's host — the IP literal if
// it is public, else the first public IP the hostname resolves to, else "".
func rpcHostPublicIP(rpcURL string) string {
	u, err := url.Parse(rpcURL)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if IsPublicIP(host) {
		return host
	}
	if ips, err := net.LookupIP(host); err == nil {
		for _, ip := range ips {
			if s := ip.String(); IsPublicIP(s) {
				return s
			}
		}
	}
	return ""
}

// SetRequestLogger sets the per-request log function on every endpoint's client
// (used for --verbose HTTP logging).
func (s *Sources) SetRequestLogger(fn LogFunc) {
	for _, c := range s.clients {
		c.LogFn = fn
	}
}

// Primary returns the first endpoint's client (used for name/genesis/valoper
// operations that only need any one client).
func (s *Sources) Primary() *Client {
	if len(s.clients) == 0 {
		return nil
	}
	return s.clients[0]
}

// NodeIDs returns the set of our own endpoints' node IDs discovered so far.
func (s *Sources) NodeIDs() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]bool, len(s.nodeIDs))
	for id := range s.nodeIDs {
		out[id] = true
	}
	return out
}

// PollResult is the outcome of polling every endpoint in one cycle. Best is the
// freshest reachable endpoint (nil when none answered); Reachable holds every
// endpoint that answered; SourcePeers holds synthetic peers for the configured
// endpoints themselves (for the Source flag and RPC-URL IP precedence).
type PollResult struct {
	Best        *Client
	BestStatus  *Status
	Reachable   []*Client
	SourcePeers []Peer
}

// Poll queries /status on every endpoint, logging reachability transitions, and
// returns the best reachable endpoint (freshest chain view), its status, all
// reachable clients, and synthetic source peers.
func (s *Sources) Poll(ctx context.Context) PollResult {
	var res PollResult
	statuses := make([]*Status, len(s.clients))
	for i, c := range s.clients {
		st, err := c.GetStatus(ctx)
		s.logTransition(i, err)
		if err != nil {
			continue
		}
		statuses[i] = st
		res.Reachable = append(res.Reachable, c)
		// gno's /status leaves node_info.id empty; the node ID is embedded in
		// net_address ("nodeID@host:port").
		id := st.NodeInfo.ID
		if id == "" {
			id = nodeIDFromNetAddress(st.NodeInfo.NetAddress)
		}
		if id != "" {
			s.mu.Lock()
			s.nodeIDs[id] = true
			s.mu.Unlock()
			// Synthetic peer for the source itself: its node ID + moniker, and
			// its public IP from the RPC URL (empty falls back to the union
			// observation during merge, e.g. for a localhost URL).
			res.SourcePeers = append(res.SourcePeers, Peer{
				NodeID:   id,
				Moniker:  st.NodeInfo.Moniker,
				RemoteIP: s.sourceIPs[i],
			})
		}
	}
	if bi := bestStatusIndex(statuses); bi >= 0 {
		res.Best = s.clients[bi]
		res.BestStatus = statuses[bi]
	}
	return res
}

func (s *Sources) logTransition(i int, err error) {
	up := err == nil
	s.mu.Lock()
	first := !s.seen[i]
	changed := s.up[i] != up
	s.seen[i] = true
	s.up[i] = up
	s.mu.Unlock()
	if !first && !changed {
		return
	}
	if up {
		s.logf("source %s: connected", s.labels[i])
	} else {
		s.logf("source %s: unreachable: %v", s.labels[i], err)
	}
}

// bestStatusIndex returns the index of the best status — preferring a
// not-catching-up node, then the highest latest block height. -1 if all nil.
func bestStatusIndex(statuses []*Status) int {
	best := -1
	for i, st := range statuses {
		if st == nil {
			continue
		}
		if best == -1 || betterStatus(st, statuses[best]) {
			best = i
		}
	}
	return best
}

func betterStatus(a, b *Status) bool {
	if a.SyncInfo.CatchingUp != b.SyncInfo.CatchingUp {
		return !a.SyncInfo.CatchingUp // synced beats catching-up
	}
	return blockHeight(a) > blockHeight(b)
}

func blockHeight(st *Status) int64 {
	n, _ := strconv.ParseInt(st.SyncInfo.LatestBlockHeight, 10, 64)
	return n
}

// MergePeers unions raw /net_info peers from several sources (in precedence
// order), deduped by node ID. For each peer the resolved public IP (first valid
// public across sources — observed remote, then advertised external host)
// becomes RemoteIP; other fields take the first non-empty value in source order.
// Source is set when the peer's node ID is one of our configured endpoints.
func MergePeers(perSource [][]Peer, sourceNodeIDs map[string]bool) []Peer {
	merged := make(map[string]*Peer)
	var order []string
	for _, list := range perSource {
		for i := range list {
			p := list[i]
			if p.NodeID == "" {
				continue // can't dedup without a node ID
			}
			m := merged[p.NodeID]
			if m == nil {
				m = &Peer{NodeID: p.NodeID}
				merged[p.NodeID] = m
				order = append(order, p.NodeID)
			}
			if m.Moniker == "" {
				m.Moniker = p.Moniker
			}
			if m.Version == "" {
				m.Version = p.Version
			}
			// Public-only, like RemoteIP: a private advertised host would leak
			// internal topology and is useless for geo/provider or RPC probing.
			if m.ExternalAddress == "" && IsPublicIP(p.ExternalAddress) {
				m.ExternalAddress = p.ExternalAddress
			}
			if m.RemoteIP == "" {
				if ip := firstPublicIP(p.RemoteIP, p.ExternalAddress); ip != "" {
					m.RemoteIP = ip
				}
			}
		}
	}
	out := make([]Peer, 0, len(order))
	for _, id := range order {
		m := merged[id]
		m.Source = sourceNodeIDs[id]
		out = append(out, *m)
	}
	return out
}
