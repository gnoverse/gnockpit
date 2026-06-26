package web

import (
	"bufio"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/web/icon"
	"github.com/gnoverse/gnockpit/web/push"
	"github.com/gorilla/websocket"
)

//go:embed index.html service-worker.js
var content embed.FS

// Version is set at build time via -ldflags or computed at startup.
var Version = ""

const (
	rpcPort     = "26657"
	peerTimeout = 3 * time.Second
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
	Client   *node.Client
	Addr     string
	Interval time.Duration

	mu       sync.RWMutex
	snapshot *node.Snapshot

	// WebSocket clients
	wsmu      sync.RWMutex
	wsClients map[*wsClient]struct{}

	// Configurable paths (set by caller before Run)
	DataDir         string         // gnoland data directory (for DB sizes on the node-down panel)
	Backend         RuntimeBackend // log and process metrics backend (nil = unavailable)
	PushManager     *push.Manager  // push notification manager (nil = disabled)
	MissedBlocksPct int            // missed-block threshold for active validator counting

	// Rate-limit timestamp for consensus-event snapshot refresh (see parseLogEvent).
	lastLogEvent time.Time
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

// GetSnapshot returns the most recent data snapshot. Used by main to construct backends.
func (s *Server) GetSnapshot() *node.Snapshot {
	return s.getSnapshot()
}

// chainName returns the connected chain's network ID from the latest snapshot.
// Falls back to "gnockpit" if no snapshot is available yet.
func (s *Server) chainName() string {
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
		Validators:     snap.Consensus.Votes,
		Timestamp:      snap.Timestamp,
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
	// Enrich with signing rate + proposer speed from signing stats
	if snap.Signing != nil && snap.Signing.WindowSize > 0 {
		for i := range report.Validators {
			addr := report.Validators[i].Address
			signed := snap.Signing.ValidatorSigns[addr]
			report.Validators[i].SignRate = signed * 100 / snap.Signing.WindowSize
			if perf, ok := snap.Signing.ValidatorPerf[addr]; ok {
				report.Validators[i].AvgBlockMs = perf.AvgBlockMs
			}
		}
	}
	return report
}

func (s *Server) buildCheckData(snap *node.Snapshot) checkData {
	cd := checkData{
		AppHashLast: snap.AppHashLast,
		System:      snap.System,
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
	// Redirect bare "/" to "/?v=<hash>" to bust aggressive browser caches.
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("v") != Version && Version != "" {
		http.Redirect(w, r, "/?v="+Version, http.StatusFound)
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
	type dbInfo struct {
		Name string `json:"name"`
		Size string `json:"size"`
	}
	type bootStatus struct {
		RPC     bool     `json:"rpc"`
		DBs     []dbInfo `json:"dbs"`
		Process bool     `json:"process"`
		CPU     string   `json:"cpu,omitempty"`
		Mem     string   `json:"mem,omitempty"`
		Uptime  string   `json:"uptime,omitempty"`
	}

	bs := bootStatus{}

	// Check RPC
	ctx := r.Context()
	if s.Client != nil {
		_, err := s.Client.GetStatus(ctx)
		bs.RPC = err == nil
	}

	// DB sizes
	if s.DataDir != "" {
		dbDir := s.DataDir + "/db"
		entries, _ := os.ReadDir(dbDir)
		for _, e := range entries {
			if !e.IsDir() || !strings.HasSuffix(e.Name(), ".db") {
				continue
			}
			if out, err := exec.CommandContext(ctx, "du", "-sh", dbDir+"/"+e.Name()).Output(); err == nil {
				fields := strings.Fields(string(out))
				if len(fields) >= 1 {
					bs.DBs = append(bs.DBs, dbInfo{Name: e.Name(), Size: fields[0]})
				}
			}
		}
	}

	// Process info via backend
	if s.Backend != nil {
		if d, err := s.Backend.ServiceUptime(ctx); err == nil {
			bs.Process = true
			bs.Uptime = d.Truncate(time.Second).String()
		}
		if kb, err := s.Backend.ProcessMemory(ctx); err == nil && kb > 0 {
			bs.Mem = fmt.Sprintf("%.0f MB", float64(kb)/1024)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(bs)
}

// --- System Info ---

func (s *Server) collectSystemInfo(ctx context.Context) *node.SystemInfo {
	si := &node.SystemInfo{
		NodeTime:    time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		GenesisTime: s.Client.Names.GenesisTime,
	}
	if s.Backend != nil {
		if uptime, err := s.Backend.ServiceUptime(ctx); err == nil {
			si.GnolandUptime = uptime.Truncate(time.Second).String()
		}
	}
	return si
}

// --- Data Fetching ---

func (s *Server) fetchSnapshot(ctx context.Context) *node.Snapshot {
	snap := &node.Snapshot{
		Timestamp: time.Now(),
	}

	status, err := s.Client.GetStatus(ctx)
	if err != nil {
		snap.Error = err.Error()
	} else {
		snap.Status = status
		s.Client.Names.SetOurs(status.ValidatorInfo.Address, status.NodeInfo.Moniker)
	}

	validators, _ := s.Client.GetValidators(ctx)
	snap.Validators = validators
	// Register pubkeys for all validators
	for _, v := range validators {
		if v.PubKey.Value != "" {
			s.Client.Names.RegisterPubKey(v.Address, v.PubKey.Value)
		}
	}

	cs, dumpPeers, err := s.Client.GetConsensusState(ctx)
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

	peers, err := s.Client.GetNetInfo(ctx)
	if err == nil {
		enriched := node.QueryAllPeers(ctx, peers, rpcPort, peerTimeout, false, validators, s.Client.LogFn, s.Client.Names)
		sort.Slice(enriched, func(i, j int) bool {
			return enriched[i].Moniker < enriched[j].Moniker
		})
		now := time.Now().Format(time.RFC3339)
		for i := range enriched {
			enriched[i].LastSeen = now
			if enriched[i].FirstSeen == "" {
				enriched[i].FirstSeen = now
			}
			var dp *node.DumpPeer
			if d, ok := dumpByIP[enriched[i].RemoteIP]; ok {
				dp = d
			} else if d, ok := dumpByNodeID[enriched[i].NodeID]; ok {
				dp = d
			}
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
	}

	// App hash for last block
	if snap.Status != nil {
		var h int
		fmt.Sscanf(snap.Status.SyncInfo.LatestBlockHeight, "%d", &h)
		if h > 1 {
			if ah, err := s.Client.GetBlockAppHash(ctx, h); err == nil {
				snap.AppHashLast = ah
			}
		}
	}

	// Signing stats from recent blocks
	if snap.Status != nil {
		var h int
		fmt.Sscanf(snap.Status.SyncInfo.LatestBlockHeight, "%d", &h)
		if h > 2 {
			signing, err := s.Client.GetSigningStats(ctx, h, 100, s.MissedBlocksPct)
			if err == nil {
				snap.Signing = signing
			}
		}
	}

	snap.System = s.collectSystemInfo(ctx)

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
		for addr := range snap.Signing.ValidatorSigns {
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

func (s *Server) publishLoop(ctx context.Context) {
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()

	publish := func() {
		if s.Client != nil && s.Client.Names != nil {
			s.Client.Names.ReloadIfChanged()
		}
		snap := s.fetchSnapshot(ctx)
		s.setSnapshot(snap)
		if s.PushManager != nil {
			s.PushManager.EvaluateAndNotify(snap)
		}

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

// --- Log Streaming ---

func (s *Server) logStreamLoop(ctx context.Context) {
	if s.Backend == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		rc, err := s.Backend.StreamLogs(ctx)
		if err != nil {
			log.Printf("log stream error: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		scanner := bufio.NewScanner(rc)
		for scanner.Scan() {
			entry := parseGnolandLog(scanner.Text())
			entry.Msg = strings.ReplaceAll(entry.Msg, "/root/", "~/")
			s.parseLogEvent(entry)
		}

		rc.Close()
		log.Printf("log stream ended, restarting in 2s")
		time.Sleep(2 * time.Second)
	}
}

// parseLogEvent triggers an immediate snapshot refresh on consensus events.
func (s *Server) parseLogEvent(entry LogEntry) {
	now := time.Now()
	// Rate limit: don't trigger snapshot refresh more than once per second.
	if now.Sub(s.lastLogEvent) < time.Second {
		return
	}

	// Consensus events: trigger an immediate snapshot refresh.
	if strings.Contains(entry.Msg, "enterNewRound") || strings.Contains(entry.Msg, "enterPrevote") ||
		strings.Contains(entry.Msg, "enterPrecommit") || strings.Contains(entry.Msg, "finalizing commit") ||
		strings.Contains(entry.Msg, "executed block") {
		s.lastLogEvent = now
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			snap := s.fetchSnapshot(ctx)
			s.setSnapshot(snap)
			timeStr := snap.Timestamp.Format("15:04:05")
			s.broadcastWS(wsMsg{Type: "time", Data: timeStr})
			if snap.Status != nil {
				s.broadcastWS(wsMsg{Type: "status", Data: snap.Status})
			}
			if snap.Peers != nil {
				s.broadcastWS(wsMsg{Type: "peers", Data: snap.Peers})
			}
			if report := s.buildVotesReport(snap); report != nil {
				s.broadcastWS(wsMsg{Type: "votes", Data: report})
			}
			s.broadcastWS(wsMsg{Type: "checks", Data: s.buildCheckData(snap)})
			if snap.Signing != nil {
				s.broadcastWS(wsMsg{Type: "signing", Data: snap.Signing})
			}
		}()
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
		MissedBlocksPct: s.PushManager.MissedBlocksPct(),
	}
	if snap != nil && snap.Signing != nil {
		resp.WindowSize = snap.Signing.WindowSize
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

// Run starts the web server, publish loop, and log streamer.
func (s *Server) Run(ctx context.Context) error {
	if Version == "" {
		if out, err := exec.Command("git", "describe", "--tags", "--always", "--dirty").Output(); err == nil {
			Version = strings.TrimSpace(string(out))
		}
	}
	go s.publishLoop(ctx)
	go s.logStreamLoop(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api", s.handleAPI)
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"version": Version})
	})
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
