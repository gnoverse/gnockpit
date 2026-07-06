package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gnoverse/gnockpit/history"
	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/web/icon"
	"github.com/gnoverse/gnockpit/web/push"
	"github.com/gorilla/websocket"
)

//go:embed index.html service-worker.js
var content embed.FS

const (
	rpcPort     = "26657"
	peerTimeout = 3 * time.Second
	// valoperRefreshInterval governs how often validator names are refreshed
	// from the on-chain registry. Identity is near-static and the refresh is
	// expensive (one query per valoper), so keep it slow.
	valoperRefreshInterval = 15 * time.Minute
	// geoIPRefreshInterval governs how often the IP-geolocation database is
	// checked for a new monthly release. The check is a cheap date comparison;
	// a download only happens when the month actually changes.
	geoIPRefreshInterval = 24 * time.Hour
	// historyRetention bounds how far back block-signing history is kept.
	historyRetention = 31 * 24 * time.Hour
	// historyPruneInterval governs how often old signing history is pruned.
	historyPruneInterval = time.Hour
)

// wsMsg is a WebSocket message envelope.
type wsMsg struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// wsClient represents a connected WebSocket client.
type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Server serves the web dashboard with WebSocket and background data fetching.
type Server struct {
	Client   *node.Client  // primary endpoint (first --rpc): names, genesis, boot
	Sources  *node.Sources // all endpoints: best-source selection + peer union
	Addr     string
	Interval time.Duration

	mu          sync.RWMutex
	snapshot    *node.Snapshot
	missed24h   map[string]int // val address -> blocks missed in last 24h (guarded by mu)
	missedSince time.Time      // oldest retained block time; how far back missed counts reach (guarded by mu)

	// WebSocket clients
	wsmu      sync.RWMutex
	wsClients map[*wsClient]struct{}

	// Set by caller before Run.
	PushManager     *push.Manager  // push notification manager (nil = disabled)
	MaxMissedInARow int            // consecutive missed/signed blocks that flip a validator down/up
	GeoIP           *node.GeoIP    // IP geolocation for the network map (nil = disabled)
	ASN             *node.ASN      // IP-to-ASN/cloud-provider resolution (nil = disabled)
	History         *history.Store // block-signing history for missed-block windows (nil = disabled)
	NotifyTestToken string         // bearer token for the notify-test API (empty = disabled)
	ChainStuckSecs  int            // seconds without a new block before status is "down"
	Links           []Link         // static header link buttons
	StatusLinks     []*StatusLink  // header links with a live BetterStack status dot
	HideSources     bool           // hide the configured source nodes from the peers list
	ChainName       string         // display name for the chain; defaults to the chain-id when empty
}

// NewServer creates a new web server.
func NewServer(client *node.Client, addr string, interval time.Duration) *Server {
	return &Server{
		Client:    client,
		Addr:      addr,
		Interval:  interval,
		wsClients: make(map[*wsClient]struct{}),
	}
}

// --- WebSocket client management ---

func (s *Server) addWSClient(c *wsClient) {
	s.wsmu.Lock()
	defer s.wsmu.Unlock()
	s.wsClients[c] = struct{}{}
}

func (s *Server) removeWSClient(c *wsClient) {
	s.wsmu.Lock()
	defer s.wsmu.Unlock()
	delete(s.wsClients, c)
	close(c.send)
}

func (s *Server) broadcastWS(msg wsMsg) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	s.wsmu.RLock()
	defer s.wsmu.RUnlock()
	for c := range s.wsClients {
		select {
		case c.send <- b:
		default:
			// drop if client can't keep up
		}
	}
}

func (s *Server) getSnapshot() *node.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot
}

func (s *Server) setSnapshot(snap *node.Snapshot) {
	s.mu.Lock()
	s.snapshot = snap
	s.mu.Unlock()
}

// chainName returns the chain's display name: the configured ChainName if set,
// otherwise the connected chain's network ID (chain-id) from the latest
// snapshot. Falls back to "gnockpit" if neither is available yet.
func (s *Server) chainName() string {
	if s.ChainName != "" {
		return s.ChainName
	}
	snap := s.getSnapshot()
	if snap != nil && snap.Status != nil && snap.Status.NodeInfo.Network != "" {
		return snap.Status.NodeInfo.Network
	}
	return "gnockpit"
}

// --- Snapshot data building ---

type checkData struct {
	AppHashLast string           `json:"apphash_last,omitempty"`
	ValAddress  string           `json:"val_address,omitempty"`
	ValPubKey   string           `json:"val_pubkey,omitempty"`
	System      *node.SystemInfo `json:"system,omitempty"`
	HideSources bool             `json:"hide_sources,omitempty"`
}

// applyValidatorHealth fills the active-validator count and BFT margin from the
// alert detector's hysteretic down/up state: a validator counts as inactive
// while its missing-blocks alert is firing (missed the streak threshold, not yet
// recovered by signing the streak back). Must run after the detector.
func (s *Server) applyValidatorHealth(snap *node.Snapshot) {
	if snap == nil || snap.Signing == nil {
		return
	}
	down := map[string]bool{}
	if s.PushManager != nil {
		down = s.PushManager.MissingBlocksFiring()
	}
	snap.Signing.Inactive = down
	active := 0
	for addr := range snap.Signing.ValidatorSigning {
		if !down[addr] {
			active++
		}
	}
	snap.Signing.ActiveCount = active
	snap.Signing.Margin = active - snap.Signing.BFTThreshold
	newThreshold := ((snap.Signing.TotalCount + 1) * 2 / 3) + 1
	snap.Signing.CanAddOne = active >= newThreshold
}

func (s *Server) buildVotesReport(snap *node.Snapshot) *node.VotesReport {
	if snap.Consensus == nil {
		return nil
	}
	report := &node.VotesReport{
		Height:         snap.Consensus.Height,
		Round:          snap.Consensus.Round,
		Step:           snap.Consensus.Step,
		Proposer:       s.Client.Names.NameWithUs(snap.Consensus.Proposer),
		RoundStartTime: snap.Consensus.RoundStartTime,
		Config:         snap.Consensus.Config,
		// Clone: the per-validator loops below mutate elements in place, and this
		// runs concurrently from the publish loop, WS-connect, and /api/status —
		// all sharing the one snapshot pointer. Each caller gets its own copy.
		Validators: slices.Clone(snap.Consensus.Votes),
		Timestamp:  snap.Timestamp,
	}
	// Enrich with voting power and bech32 pubkey from validator set
	vpByAddr := make(map[string]string, len(snap.Validators))
	pkByAddr := make(map[string]string, len(snap.Validators))
	for _, v := range snap.Validators {
		vpByAddr[v.Address] = v.VotingPower
		pkByAddr[v.Address] = v.PubKey.Bech32()
	}
	for i := range report.Validators {
		addr := report.Validators[i].Address
		report.Validators[i].VotingPower = vpByAddr[addr]
		report.Validators[i].PubKey = pkByAddr[addr]
	}
	// Enrich with missed-block count + proposer speed from signing stats, and the
	// down/inactive flag from the alert detector's hysteretic state.
	if snap.Signing != nil {
		for i := range report.Validators {
			addr := report.Validators[i].Address
			report.Validators[i].Missed100 = snap.Signing.ValidatorSigning[addr].Missed
			report.Validators[i].Inactive = snap.Signing.Inactive[addr]
			if perf, ok := snap.Signing.ValidatorPerf[addr]; ok {
				report.Validators[i].AvgBlockMs = perf.AvgBlockMs
			}
		}
	}
	// Attach missed-24h counts and the recording-since timestamp from history.
	s.mu.RLock()
	missed := s.missed24h
	since := s.missedSince
	s.mu.RUnlock()
	if missed != nil {
		for i := range report.Validators {
			report.Validators[i].Missed24h = missed[report.Validators[i].Address]
		}
	}
	if !since.IsZero() {
		report.MissedSince = since.UTC().Format(time.RFC3339)
	}
	return report
}

func (s *Server) buildCheckData(snap *node.Snapshot) checkData {
	cd := checkData{
		AppHashLast: snap.AppHashLast,
		System:      snap.System,
		HideSources: s.HideSources,
	}
	if snap.Status != nil {
		cd.ValAddress = snap.Status.ValidatorInfo.Address
		cd.ValPubKey = snap.Status.ValidatorInfo.PubKey.Bech32()
	}
	return cd
}

// buildSnapshotMsg builds a full snapshot message for initial WS connect.
func (s *Server) buildSnapshotMsg(snap *node.Snapshot) wsMsg {
	type snapshotData struct {
		Time   string            `json:"time"`
		Status *node.Status      `json:"status,omitempty"`
		Peers  []node.Peer       `json:"peers,omitempty"`
		Votes  *node.VotesReport `json:"votes,omitempty"`
		Checks checkData         `json:"checks"`
	}
	sd := snapshotData{
		Time:   snap.Timestamp.Format("15:04:05"),
		Status: snap.Status,
		Peers:  snap.Peers,
		Votes:  s.buildVotesReport(snap),
		Checks: s.buildCheckData(snap),
	}
	return wsMsg{Type: "snapshot", Data: sd}
}

// buildUpdateMsg builds a batched periodic update message.
// Sending one message instead of 5-6 individual ones reduces JSON.parse
// calls and WS event handler invocations on the frontend.
func (s *Server) buildUpdateMsg(snap *node.Snapshot) wsMsg {
	type updateData struct {
		Time    string             `json:"time"`
		Status  *node.Status       `json:"status,omitempty"`
		Peers   []node.Peer        `json:"peers,omitempty"`
		Votes   *node.VotesReport  `json:"votes,omitempty"`
		Checks  checkData          `json:"checks"`
		Signing *node.SigningStats `json:"signing,omitempty"`
	}
	ud := updateData{
		Time:    snap.Timestamp.Format("15:04:05"),
		Status:  snap.Status,
		Peers:   snap.Peers,
		Votes:   s.buildVotesReport(snap),
		Checks:  s.buildCheckData(snap),
		Signing: snap.Signing,
	}
	return wsMsg{Type: "update", Data: ud}
}

// --- HTTP Handlers ---

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	// Mux registers this on "/", which also catches unknown paths; reject those.
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := content.ReadFile("index.html")
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write(data)
}

func (s *Server) handleIconSVG(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, icon.SVG(s.chainName(), 64))
}

func (s *Server) handleIconPNG(w http.ResponseWriter, r *http.Request) {
	// Extract size from path: /icon-192.png → 192.
	// Supported: 32, 192, 512. Default: 192.
	size := 192
	path := r.URL.Path
	dash := strings.LastIndex(path, "-")
	dot := strings.LastIndex(path, ".")
	if dash >= 0 && dot > dash {
		switch path[dash+1 : dot] {
		case "32":
			size = 32
		case "512":
			size = 512
		}
	}
	data, err := icon.PNG(s.chainName(), size)
	if err != nil {
		http.Error(w, "icon generation failed", 500)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
}

func (s *Server) handleAppleTouchIcon(w http.ResponseWriter, r *http.Request) {
	data, err := icon.PNG(s.chainName(), 180)
	if err != nil {
		http.Error(w, "icon generation failed", 500)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	chain := s.chainName()
	col := icon.ChainColor(chain)
	manifest := map[string]interface{}{
		"name":             "Gnockpit " + chain,
		"short_name":       "Gnockpit " + chain,
		"description":      "Real-time gno.land validator node monitoring dashboard",
		"start_url":        "/",
		"display":          "standalone",
		"background_color": "#0d1117",
		"theme_color":      col.HexBG,
		"icons": []map[string]string{
			{"src": "/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
			{"src": "/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
			{"src": "/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(manifest)
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	snap := s.getSnapshot()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if snap == nil {
		w.Write([]byte(`{"error":"no data yet"}`))
		return
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(snap)
}

// --- WebSocket Handler ---

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade error: %v", err)
		return
	}

	client := &wsClient{
		conn: conn,
		send: make(chan []byte, 64),
	}
	s.addWSClient(client)

	// Send snapshot immediately
	if snap := s.getSnapshot(); snap != nil {
		msg := s.buildSnapshotMsg(snap)
		if b, err := json.Marshal(msg); err == nil {
			select {
			case client.send <- b:
			default:
			}
		}
	}

	// Write pump
	go func() {
		defer conn.Close()
		for msg := range client.send {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}()

	// Read pump (just drain / detect close)
	go func() {
		defer func() {
			s.removeWSClient(client)
			conn.Close()
		}()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
}

// --- Boot Status (visible even when RPC is down) ---

func (s *Server) handleBootStatus(w http.ResponseWriter, r *http.Request) {
	type bootStatus struct {
		RPC       bool   `json:"rpc"`
		ChainName string `json:"chain_name,omitempty"` // configured display-name override, empty = use chain-id
	}

	bs := bootStatus{ChainName: s.ChainName}

	// "Up" once at least one source has yielded chain data; goes false again if
	// every source is unreachable — the banner then doubles as the all-down
	// warning.
	if snap := s.getSnapshot(); snap != nil && snap.Status != nil {
		bs.RPC = true
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(bs)
}

// --- System Info ---

func (s *Server) collectSystemInfo() *node.SystemInfo {
	return &node.SystemInfo{
		NodeTime:    time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		GenesisTime: s.Client.Names.GenesisTime,
	}
}

// --- Data Fetching ---

func (s *Server) fetchSnapshot(ctx context.Context) *node.Snapshot {
	snap := &node.Snapshot{
		Timestamp: time.Now(),
	}

	poll := s.Sources.Poll(ctx)
	if poll.Best == nil {
		snap.Error = "all sources unreachable"
		snap.System = s.collectSystemInfo()
		return snap
	}
	best := poll.Best
	snap.Status = poll.BestStatus
	best.Names.SetOurs(poll.BestStatus.ValidatorInfo.Address, poll.BestStatus.NodeInfo.Moniker)

	validators, err := best.GetValidators(ctx)
	if err != nil {
		log.Printf("validators: %v", err)
	}
	snap.Validators = validators
	// Register pubkeys for all validators
	for _, v := range validators {
		if v.PubKey.Value != "" {
			best.Names.RegisterPubKey(v.Address, v.PubKey.Value)
		}
	}

	cs, dumpPeers, err := best.GetConsensusState(ctx)
	if err == nil {
		snap.Consensus = cs
		snap.RoundStartTime = cs.RoundStartTime
	}

	dumpByIP := make(map[string]*node.DumpPeer)
	dumpByNodeID := make(map[string]*node.DumpPeer)
	for i := range dumpPeers {
		dp := &dumpPeers[i]
		if dp.RemoteIP != "" {
			dumpByIP[dp.RemoteIP] = dp
		}
		if dp.NodeID != "" {
			dumpByNodeID[dp.NodeID] = dp
		}
	}

	// Union raw /net_info peers across all reachable sources, deduped by node ID
	// (which also resolves each peer's public IP), then probe the union once. The
	// source nodes go first so they're flagged and their RPC-URL IP takes
	// precedence over any observation of them.
	perSource := [][]node.Peer{poll.SourcePeers}
	for _, c := range poll.Reachable {
		raw, err := c.GetNetInfo(ctx)
		if err != nil {
			log.Printf("net_info from %s: %v", c.RPCURL, err)
			continue
		}
		perSource = append(perSource, raw)
	}
	merged := node.MergePeers(perSource, s.Sources.NodeIDs())
	s.Sources.FillSourceEgressIP(merged)
	enriched := node.QueryAllPeers(ctx, merged, rpcPort, peerTimeout, false, validators, best.LogFn, best.Names)
	sort.Slice(enriched, func(i, j int) bool {
		return enriched[i].Moniker < enriched[j].Moniker
	})
	now := time.Now().Format(time.RFC3339)
	for i := range enriched {
		enriched[i].LastSeen = now
		if enriched[i].FirstSeen == "" {
			enriched[i].FirstSeen = now
		}
		dp := node.MatchDumpPeer(enriched[i].NodeID, enriched[i].RemoteIP, dumpByNodeID, dumpByIP)
		if dp != nil {
			enriched[i].PeerHeight = dp.Height
			enriched[i].PeerRound = dp.Round
			enriched[i].PeerStep = dp.Step
			enriched[i].HasProposal = dp.Proposal
			enriched[i].PeerPrevotes = dp.Prevotes
			enriched[i].PeerPrecommits = dp.Precommits
			if dp.NodeID != "" && dp.RemoteIP != "" && dp.P2PPort != "" {
				enriched[i].P2PAddress = dp.NodeID + "@" + dp.RemoteIP + ":" + dp.P2PPort
			}
		}
	}
	snap.Peers = enriched

	// Geolocate peers for the network map (best-effort; empty until the
	// database has been downloaded).
	if s.GeoIP != nil {
		for i := range snap.Peers {
			ip := snap.Peers[i].RemoteIP // public-or-empty after the merge
			if ip == "" {
				continue
			}
			if lat, lon, city, country, ok := s.GeoIP.Lookup(ip); ok {
				snap.Peers[i].Lat = lat
				snap.Peers[i].Lon = lon
				snap.Peers[i].City = city
				snap.Peers[i].Country = country
			}
		}
	}

	// Resolve each peer's cloud provider from its IP (best-effort; empty until
	// the ASN database has been downloaded).
	if s.ASN != nil {
		for i := range snap.Peers {
			ip := snap.Peers[i].RemoteIP // public-or-empty after the merge
			if ip == "" {
				continue
			}
			if asn, org, ok := s.ASN.Lookup(ip); ok {
				snap.Peers[i].ASN = asn
				snap.Peers[i].ASOrg = org
				snap.Peers[i].Provider = node.Provider(org)
			}
		}
	}

	// App hash for last block
	if snap.Status != nil {
		var h int
		fmt.Sscanf(snap.Status.SyncInfo.LatestBlockHeight, "%d", &h)
		if h > 1 {
			if ah, err := best.GetBlockAppHash(ctx, h); err == nil {
				snap.AppHashLast = ah
			}
		}
	}

	// Signing stats from recent blocks
	if snap.Status != nil {
		var h int
		fmt.Sscanf(snap.Status.SyncInfo.LatestBlockHeight, "%d", &h)
		if h > 2 {
			signing, err := best.GetSigningStats(ctx, h, 100)
			if err != nil {
				log.Printf("signing stats: %v", err)
			} else {
				signing.MaxMissedInARow = s.MaxMissedInARow
				snap.Signing = signing
			}
		}
	}

	snap.System = s.collectSystemInfo()

	// gnockpit-names.json is the single source of truth for validator names.
	// QueryAllPeers above already filled gaps with monikers it discovered via
	// peer /status RPC (Register is non-overwriting). For any validator address
	// still unnamed, EnsureName assigns "unknown-val-N" and persists it. Then
	// refresh Name fields in the snapshot so the UI sees the canonical names.
	s.ensureValidatorNames(snap)

	return snap
}

// ensureValidatorNames walks every validator address in the snapshot, calls
// EnsureName so each one is registered (assigning unknown-val-N if no
// candidate moniker is available), then refreshes Name fields so the UI
// reads canonical values from gnockpit-names.json.
func (s *Server) ensureValidatorNames(snap *node.Snapshot) {
	if snap == nil {
		return
	}

	// Collect candidate monikers per address from peers.
	candidates := map[string]string{}
	for _, p := range snap.Peers {
		if p.ValAddress != "" && p.Moniker != "" {
			if _, ok := candidates[p.ValAddress]; !ok {
				candidates[p.ValAddress] = p.Moniker
			}
		}
	}

	// Collect every validator address we've seen.
	addrs := map[string]struct{}{}
	for _, v := range snap.Validators {
		if v.Address != "" {
			addrs[v.Address] = struct{}{}
		}
	}
	if snap.Consensus != nil {
		if snap.Consensus.Proposer != "" {
			addrs[snap.Consensus.Proposer] = struct{}{}
		}
		for _, vi := range snap.Consensus.Votes {
			if vi.Address != "" {
				addrs[vi.Address] = struct{}{}
			}
		}
	}
	if snap.Signing != nil {
		for addr := range snap.Signing.ValidatorSigning {
			if addr != "" {
				addrs[addr] = struct{}{}
			}
		}
		for _, b := range snap.Signing.RecentBlocks {
			for _, mv := range b.Missing {
				if mv.Address != "" {
					addrs[mv.Address] = struct{}{}
				}
			}
		}
	}

	for addr := range addrs {
		s.Client.Names.EnsureName(addr, candidates[addr])
	}

	// Refresh Name fields so the UI sees canonical names from the registry.
	for i := range snap.Validators {
		snap.Validators[i].Name = s.Client.Names.NameWithUs(snap.Validators[i].Address)
	}
	if snap.Consensus != nil {
		for i := range snap.Consensus.Votes {
			snap.Consensus.Votes[i].Name = s.Client.Names.NameWithUs(snap.Consensus.Votes[i].Address)
		}
	}
	if snap.Signing != nil {
		for i := range snap.Signing.RecentBlocks {
			for j := range snap.Signing.RecentBlocks[i].Missing {
				snap.Signing.RecentBlocks[i].Missing[j].Name = s.Client.Names.Name(snap.Signing.RecentBlocks[i].Missing[j].Address)
			}
		}
	}
}

// --- Publish Loop ---

// recordHistory persists this snapshot's recent blocks into the signing-history
// store and refreshes the cached missed-24h counts used by the validators view.
// Errors are logged and swallowed: the live dashboard must keep working even if
// history recording fails.
func (s *Server) recordHistory(ctx context.Context, snap *node.Snapshot) {
	if s.History == nil || snap == nil || snap.Signing == nil {
		return
	}
	blocks := make([]history.Block, 0, len(snap.Signing.RecentBlocks))
	for _, b := range snap.Signing.RecentBlocks {
		h, err := strconv.ParseInt(b.Height, 10, 64)
		if err != nil {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, b.Time)
		if err != nil {
			continue
		}
		missing := make([]string, 0, len(b.Missing))
		for _, mv := range b.Missing {
			missing = append(missing, mv.Address)
		}
		blocks = append(blocks, history.Block{Height: h, Time: t, Missing: missing})
	}
	if err := s.History.RecordBlocks(ctx, blocks); err != nil {
		log.Printf("history: record blocks: %v", err)
		return
	}
	now := time.Now()
	wc, err := s.History.MissedInWindow(ctx, 24*time.Hour, now)
	if err != nil {
		log.Printf("history: missed-24h: %v", err)
		return
	}
	earliest, err := s.History.EarliestRecorded(ctx)
	if err != nil {
		log.Printf("history: earliest recorded: %v", err)
		return
	}
	s.mu.Lock()
	s.missed24h = wc.Missed
	s.missedSince = earliest
	s.mu.Unlock()
}

// historyPruneLoop periodically drops signing history older than the retention
// horizon, then repeats on a slow timer.
func (s *Server) historyPruneLoop(ctx context.Context) {
	if s.History == nil {
		return
	}
	prune := func() {
		if err := s.History.Prune(ctx, time.Now().Add(-historyRetention)); err != nil {
			log.Printf("history: prune: %v", err)
		}
	}
	prune()
	ticker := time.NewTicker(historyPruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}

func (s *Server) publishLoop(ctx context.Context) {
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()

	publish := func() {
		if s.Client != nil && s.Client.Names != nil {
			s.Client.Names.ReloadIfChanged()
		}
		snap := s.fetchSnapshot(ctx)
		if s.PushManager != nil {
			s.PushManager.EvaluateAndNotify(snap)
		}
		// Active-validator count and the down set depend on the detector's
		// hysteretic state, so derive them after EvaluateAndNotify has updated it.
		// Both run before setSnapshot so the published snapshot is complete and
		// immutable when a WebSocket client reads it on connect.
		s.applyValidatorHealth(snap)
		s.setSnapshot(snap)
		s.recordHistory(ctx, snap)

		// Broadcast to WebSocket clients as a single batched message to reduce
		// JSON.parse calls and WS message overhead on the frontend.
		s.broadcastWS(s.buildUpdateMsg(snap))
	}

	publish()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publish()
		}
	}
}

// valoperRefreshLoop refreshes validator names from the on-chain
// r/gnops/valopers registry: once at startup, then on a slow timer. The
// registry enumeration is one RPC per valoper, so it must never run in the
// snapshot loop. Names land in the registry (non-overwriting / upgrading
// unknown-val-N) and the next snapshot's ensureValidatorNames picks them up.
func (s *Server) valoperRefreshLoop(ctx context.Context) {
	refresh := func() {
		names, err := s.Client.FetchValoperNames(ctx)
		if err != nil {
			log.Printf("valoper name refresh failed: %v", err)
			return
		}
		s.Client.Names.SeedFromValopers(names)
	}
	refresh()

	ticker := time.NewTicker(valoperRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}

// geoIPRefreshLoop keeps the IP-geolocation database current: it checks daily
// and downloads the new month's DB-IP City Lite file when the month rolls over
// (EnsureFresh is a no-op when the loaded month is already current). The first
// run downloads the database in the background, so the map fills in once it
// completes without blocking the dashboard.
func (s *Server) geoIPRefreshLoop(ctx context.Context) {
	if s.GeoIP == nil {
		return
	}
	refresh := func() {
		if err := s.GeoIP.EnsureFresh(ctx, time.Now()); err != nil {
			log.Printf("geoip refresh failed: %v", err)
		}
	}
	refresh()

	ticker := time.NewTicker(geoIPRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}

// asnRefreshLoop keeps the IP-to-ASN database current, checking daily and
// downloading only when the month rolls over, mirroring geoIPRefreshLoop.
func (s *Server) asnRefreshLoop(ctx context.Context) {
	if s.ASN == nil {
		return
	}
	refresh := func() {
		if err := s.ASN.EnsureFresh(ctx, time.Now()); err != nil {
			log.Printf("asn refresh failed: %v", err)
		}
	}
	refresh()

	ticker := time.NewTicker(geoIPRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}

// generateID generates a random hex string suitable for use as a subscription ID.
func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	data, err := content.ReadFile("service-worker.js")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/javascript")
	w.Header().Set("Service-Worker-Allowed", "/")
	w.Write(data)
}

func (s *Server) handlePushVAPIDKey(w http.ResponseWriter, r *http.Request) {
	if s.PushManager == nil {
		http.Error(w, "push not configured", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"key": s.PushManager.VAPIDPublicKey()})
}

func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.PushManager == nil {
		http.Error(w, "push not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodDelete {
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		if err := s.PushManager.DB().DeleteSubscription(id); err != nil {
			http.Error(w, "delete subscription", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var req push.SubscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Endpoint == "" || req.P256dh == "" || req.Auth == "" {
		http.Error(w, "endpoint, p256dh and auth are required", http.StatusBadRequest)
		return
	}
	sub := push.Subscription{
		ID:       generateID(),
		Endpoint: req.Endpoint,
		P256dh:   req.P256dh,
		Auth:     req.Auth,
	}
	for i := range req.Alerts {
		req.Alerts[i].SubscriptionID = sub.ID
	}
	if err := s.PushManager.DB().SaveSubscriptionWithAlerts(sub, req.Alerts); err != nil {
		http.Error(w, "save subscription", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": sub.ID})
}

func (s *Server) handlePushEntities(w http.ResponseWriter, r *http.Request) {
	if s.PushManager == nil {
		http.Error(w, "push not configured", http.StatusServiceUnavailable)
		return
	}
	snap := s.getSnapshot()
	resp := push.EntitiesResponse{
		ChainStuckSecs:  s.PushManager.ChainStuckSecs(),
		MaxMissedInARow: s.PushManager.MaxMissedInARow(),
	}

	if snap != nil && snap.Consensus != nil {
		for _, v := range snap.Consensus.Votes {
			resp.Validators = append(resp.Validators, push.Entity{
				ValAddress: v.Address,
				Moniker:    v.Name,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handlePushTest(w http.ResponseWriter, r *http.Request) {
	if s.PushManager == nil {
		http.Error(w, "push not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if err := s.PushManager.SendTest(req.ID); err != nil {
		http.Error(w, "subscription not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// --- Notify-test API (opt-in, token-gated) ---

// authorizeNotifyTest gates the notify-test endpoints. The feature is disabled
// (404) unless NotifyTestToken is set; when it is, requests must carry a
// matching bearer token. Returns the HTTP status to send on failure.
func (s *Server) authorizeNotifyTest(r *http.Request) (int, bool) {
	if s.NotifyTestToken == "" {
		return http.StatusNotFound, false
	}
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return http.StatusUnauthorized, false
	}
	tok := strings.TrimPrefix(h, prefix)
	if subtle.ConstantTimeCompare([]byte(tok), []byte(s.NotifyTestToken)) != 1 {
		return http.StatusUnauthorized, false
	}
	return http.StatusOK, true
}

// handleNotifyTargets lists the configured Shoutrrr notification URLs as
// redacted metadata (never the raw URLs, which hold secrets).
func (s *Server) handleNotifyTargets(w http.ResponseWriter, r *http.Request) {
	if code, ok := s.authorizeNotifyTest(r); !ok {
		http.Error(w, http.StatusText(code), code)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.PushManager == nil {
		http.Error(w, "notifications not configured", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"targets": s.PushManager.NotifyTargets()})
}

// handleNotifyTest sends a custom message to one configured Shoutrrr URL,
// selected by index. The caller can never supply an arbitrary URL.
func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	if code, ok := s.authorizeNotifyTest(r); !ok {
		http.Error(w, http.StatusText(code), code)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.PushManager == nil {
		http.Error(w, "notifications not configured", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Index   int    `json:"index"`
		Message string `json:"message"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Index < 0 || req.Index >= s.PushManager.NotifyTargetCount() {
		http.Error(w, "notify target index out of range", http.StatusBadRequest)
		return
	}
	msg := req.Message
	if msg == "" {
		msg = "gnockpit test notification"
	}
	if err := s.PushManager.SendTestNotify(req.Index, msg); err != nil {
		// The send error embeds the raw notification URL (with its secret), so
		// it must never reach the client — log it server-side and return a
		// generic message.
		log.Printf("notify-test: send to target %d failed: %v", req.Index, err)
		http.Error(w, "notification delivery failed", http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Run starts the web server, publish loop, and log streamer.
func (s *Server) Run(ctx context.Context) error {
	go s.publishLoop(ctx)
	go s.valoperRefreshLoop(ctx)
	go s.geoIPRefreshLoop(ctx)
	go s.asnRefreshLoop(ctx)
	go s.statusLinkRefreshLoop(ctx)
	go s.historyPruneLoop(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api", s.handleAPI)
	mux.HandleFunc("/api/boot", s.handleBootStatus)
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/manifest.json", s.handleManifest)
	mux.HandleFunc("/icon.svg", s.handleIconSVG)
	mux.HandleFunc("/icon-32.png", s.handleIconPNG)
	mux.HandleFunc("/icon-192.png", s.handleIconPNG)
	mux.HandleFunc("/icon-512.png", s.handleIconPNG)
	mux.HandleFunc("/apple-touch-icon.png", s.handleAppleTouchIcon)
	mux.HandleFunc("/service-worker.js", s.handleServiceWorker)
	mux.HandleFunc("/api/push/vapid-key", s.handlePushVAPIDKey)
	mux.HandleFunc("/api/push/subscribe", s.handlePushSubscribe)
	mux.HandleFunc("/api/push/entities", s.handlePushEntities)
	mux.HandleFunc("/api/push/test", s.handlePushTest)
	mux.HandleFunc("/api/notify/targets", s.handleNotifyTargets)
	mux.HandleFunc("/api/notify/test", s.handleNotifyTest)
	mux.HandleFunc("/api/links", s.handleLinks)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/badge.svg", s.handleBadge)

	srv := &http.Server{
		Addr:    s.Addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Printf("gnockpit listening on http://%s", s.Addr)
	return srv.ListenAndServe()
}
