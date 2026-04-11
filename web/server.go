package web

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
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

	"github.com/gorilla/websocket"
	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/web/icon"
	"github.com/gnoverse/gnockpit/web/push"
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

	// SSE clients (legacy fallback)
	sseClients map[chan string]struct{}

	// WebSocket clients
	wsmu      sync.RWMutex
	wsClients map[*wsClient]struct{}

	// Diagnosis reports
	diagmu   sync.RWMutex
	diagData map[string]*DiagReport // keyed by IP

	startTime      time.Time
	genesisSHA     string // computed from local genesis file at startup
	chainDataSize  string
	chainDataTime  time.Time

	// Configurable paths (set by caller before Run)
	DataDir     string         // gnoland data directory (for disk stats)
	GenesisPath string         // path to genesis.json (for hash computation)
	Backend     RuntimeBackend // log and process metrics backend (nil = unavailable)
	PushManager *push.Manager  // push notification manager (nil = disabled)

	// Peer activity from log parsing
	peerActivity   sync.Map // IP -> time.Time (last seen)
	lastLogEvent   time.Time
}

// DiagReport holds a per-node diagnosis report.
type DiagReport struct {
	IP        string      `json:"ip"`
	Moniker   string      `json:"moniker"`
	Time      string      `json:"time"`
	Checks    []DiagCheck `json:"checks"`
}

// DiagCheck is a single check in a diagnosis.
type DiagCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok, warn, err
	Detail string `json:"detail"`
}

// NewServer creates a new web server.
func NewServer(client *node.Client, addr string, interval time.Duration) *Server {
	return &Server{
		Client:     client,
		Addr:       addr,
		Interval:   interval,
		sseClients: make(map[chan string]struct{}),
		wsClients:  make(map[*wsClient]struct{}),
		diagData:   make(map[string]*DiagReport),
		startTime:  time.Now(),
	}
}

// --- SSE client management (legacy fallback) ---

func (s *Server) addSSEClient(ch chan string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sseClients[ch] = struct{}{}
}

func (s *Server) removeSSEClient(ch chan string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sseClients, ch)
	close(ch)
}

func (s *Server) broadcastSSE(event, data string) {
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.sseClients {
		select {
		case ch <- msg:
		default:
		}
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
	GenesisSHA  string           `json:"genesis_sha256"`
	AppHashLast string           `json:"apphash_last,omitempty"`
	Uptime      string           `json:"uptime"`
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
	// Enrich with voting power and pubkey from validator set
	vpByAddr := make(map[string]string, len(snap.Validators))
	pkByAddr := make(map[string]string, len(snap.Validators))
	for _, v := range snap.Validators {
		vpByAddr[v.Address] = v.VotingPower
		pkByAddr[v.Address] = v.PubKey.Value
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
		GenesisSHA:  snap.GenesisSHA,
		AppHashLast: snap.AppHashLast,
		Uptime:     snap.Uptime,
		System:     snap.System,
	}
	if snap.Status != nil {
		cd.ValAddress = snap.Status.ValidatorInfo.Address
		cd.ValPubKey = snap.Status.ValidatorInfo.PubKey.Value
	}
	return cd
}

// buildSnapshotMsg builds a full snapshot message for initial WS connect.
func (s *Server) buildSnapshotMsg(snap *node.Snapshot) wsMsg {
	type snapshotData struct {
		Time   string           `json:"time"`
		Status *node.Status     `json:"status,omitempty"`
		Peers  []node.Peer      `json:"peers,omitempty"`
		Votes  *node.VotesReport `json:"votes,omitempty"`
		Checks checkData        `json:"checks"`
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

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := make(chan string, 32)
	s.addSSEClient(ch)
	defer s.removeSSEClient(ch)

	// Send current snapshot immediately on connect
	if snap := s.getSnapshot(); snap != nil {
		s.sendSSESnapshot(snap, ch)
	}
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprint(w, msg)
			flusher.Flush()
		}
	}
}

func (s *Server) sendSSESnapshot(snap *node.Snapshot, ch chan string) {
	sendEvent := func(event string, data interface{}) {
		if b, err := json.Marshal(data); err == nil {
			msg := fmt.Sprintf("event: %s\ndata: %s\n\n", event, string(b))
			select {
			case ch <- msg:
			default:
			}
		}
	}

	msg := fmt.Sprintf("event: time\ndata: %s\n\n", snap.Timestamp.Format("15:04:05"))
	select {
	case ch <- msg:
	default:
	}

	if snap.Status != nil {
		sendEvent("status", snap.Status)
	}
	if snap.Peers != nil {
		sendEvent("peers", snap.Peers)
	}
	if report := s.buildVotesReport(snap); report != nil {
		sendEvent("votes", report)
	}
	sendEvent("checks", s.buildCheckData(snap))
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

func (s *Server) handleResetCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}
	// Clear snapshot
	s.setSnapshot(nil)
	// Clear peer activity
	s.peerActivity.Range(func(key, _ interface{}) bool {
		s.peerActivity.Delete(key)
		return true
	})
	// Clear diagnosis data
	s.diagmu.Lock()
	s.diagData = make(map[string]*DiagReport)
	s.diagmu.Unlock()
	// Clear chain data cache
	s.chainDataSize = ""
	s.chainDataTime = time.Time{}
	// Clear and reload name registry from persist file
	if s.Client != nil && s.Client.Names != nil {
		s.Client.Names.Reload()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// --- System Info ---

func (s *Server) collectSystemInfo(ctx context.Context) *node.SystemInfo {
	si := &node.SystemInfo{}

	// Disk usage
	dfPath := s.DataDir
	if dfPath == "" {
		dfPath = "/"
	}
	if out, err := exec.CommandContext(ctx, "df", "-h", dfPath).Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) >= 2 {
			fields := strings.Fields(lines[1])
			if len(fields) >= 5 {
				si.DiskTotal = fields[1]
				si.DiskUsed = fields[2]
				pct := strings.TrimSuffix(fields[4], "%")
				fmt.Sscanf(pct, "%d", &si.DiskPercent)
			}
		}
	}

	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 3 {
			si.LoadAvg = fields[0] + " " + fields[1] + " " + fields[2]
		}
	}

	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		si.NumCPU = strings.Count(string(data), "processor\t:")
	}

	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close()
		memInfo := map[string]string{}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			parts := strings.SplitN(scanner.Text(), ":", 2)
			if len(parts) == 2 {
				memInfo[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		}
		var totalKB, availKB int
		fmt.Sscanf(memInfo["MemTotal"], "%d", &totalKB)
		fmt.Sscanf(memInfo["MemAvailable"], "%d", &availKB)
		if totalKB > 0 {
			usedKB := totalKB - availKB
			si.MemUsed = fmt.Sprintf("%.1fG", float64(usedKB)/1024/1024)
			si.MemTotal = fmt.Sprintf("%.1fG", float64(totalKB)/1024/1024)
			si.MemPercent = usedKB * 100 / totalKB
		}
	}

	if s.Backend != nil {
		if uptime, err := s.Backend.ServiceUptime(ctx); err == nil {
			si.GnolandUptime = uptime.Truncate(time.Second).String()
		}
		if kb, err := s.Backend.ProcessMemory(ctx); err == nil && kb > 0 {
			si.GnolandMem = fmt.Sprintf("%.1fG", float64(kb)/1024/1024)
		}
	}

	// Chain data size (cached, updated max once per minute)
	if s.DataDir != "" && time.Since(s.chainDataTime) > time.Minute {
		if out, err := exec.CommandContext(ctx, "du", "-sh", s.DataDir).Output(); err == nil {
			parts := strings.Fields(string(out))
			if len(parts) > 0 {
				s.chainDataSize = parts[0]
				s.chainDataTime = time.Now()
			}
		}
	}
	si.ChainDataSize = s.chainDataSize

	si.NodeTime = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	si.GenesisTime = s.Client.Names.GenesisTime

	// Git info for gno source
	if gnoRoot := s.gnoRootDir(); gnoRoot != "" {
		if out, err := exec.CommandContext(ctx, "git", "-C", gnoRoot, "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
			si.GitBranch = strings.TrimSpace(string(out))
		}
		if out, err := exec.CommandContext(ctx, "git", "-C", gnoRoot, "rev-parse", "--short", "HEAD").Output(); err == nil {
			si.GitSHA = strings.TrimSpace(string(out))
		}
	}
	if s.Backend != nil {
		if hash, err := s.Backend.BinaryHash(ctx); err == nil {
			si.BinaryHash = hash
		}
	}

	// Seeds from config
	if dataDir := s.DataDir; dataDir != "" {
		if data, err := os.ReadFile(dataDir + "/config/config.toml"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "seeds") && strings.Contains(line, "=") {
					val := strings.SplitN(line, "=", 2)[1]
					val = strings.Trim(strings.TrimSpace(val), "\"")
					if val != "" {
						si.Seeds = val
					}
					break
				}
			}
		}
	}

	return si
}

// --- Data Fetching ---

func (s *Server) fetchSnapshot(ctx context.Context) *node.Snapshot {
	snap := &node.Snapshot{
		GenesisSHA: s.genesisSHA,
		Timestamp:  time.Now(),
		Uptime:     time.Since(s.startTime).Truncate(time.Second).String(),
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
			signing, err := s.Client.GetSigningStats(ctx, h, 100)
			if err == nil {
				snap.Signing = signing
			}
		}
	}

	snap.System = s.collectSystemInfo(ctx)

	// === Definitive validator↔peer correlation ===
	// For validators with unknown names, try to match them to unmatched peers.
	// Strategy: find validators with no name AND peers with no validator address.
	// If a peer's moniker looks like a validator (contains "val") and shares an IP
	// with other known validators, it's likely a validator on that infra.
	if len(snap.Validators) > 0 && len(snap.Peers) > 0 {
		// Step 1: direct matches — peer already has ValAddress that's in the validator set
		matchedAddrs := map[string]bool{}
		matchedPeers := map[string]bool{}
		for _, p := range snap.Peers {
			if p.ValAddress != "" {
				for _, v := range snap.Validators {
					if v.Address == p.ValAddress {
						if p.Moniker != "" {
							s.Client.Names.Register(v.Address, p.Moniker)
						}
						matchedAddrs[v.Address] = true
						matchedPeers[p.Moniker] = true
						break
					}
				}
			}
		}

		// Step 2: for remaining unmatched validators, try to correlate with unmatched peers
		// by querying each unmatched peer's RPC directly and matching the returned node-id
		for _, p := range snap.Peers {
			if matchedPeers[p.Moniker] || p.Moniker == "" {
				continue
			}
			// Check if this peer's moniker is already known for a validator
			if addr, ok := s.Client.Names.AddrByMoniker(p.Moniker); ok {
				for _, v := range snap.Validators {
					if v.Address == addr {
						matchedAddrs[addr] = true
						matchedPeers[p.Moniker] = true
						break
					}
				}
			}
		}

		// Step 3: last resort — for each unknown validator address, find an unmatched
		// peer on the same IP as a known validator from the same team
		unmatchedVals := []string{}
		for _, v := range snap.Validators {
			if !matchedAddrs[v.Address] && s.Client.Names.Name(v.Address) == v.Address {
				unmatchedVals = append(unmatchedVals, v.Address)
			}
		}
		unmatchedPeers := []node.Peer{}
		for _, p := range snap.Peers {
			if !matchedPeers[p.Moniker] && p.Moniker != "" && p.Role == "full" {
				unmatchedPeers = append(unmatchedPeers, p)
			}
		}
		// If there's exactly 1 unmatched validator and 1 unmatched "val-looking" peer, match them
		if len(unmatchedVals) == 1 && len(unmatchedPeers) >= 1 {
			for _, p := range unmatchedPeers {
				if strings.Contains(strings.ToLower(p.Moniker), "val") {
					s.Client.Names.Register(unmatchedVals[0], p.Moniker)
					break
				}
			}
		}
		// Also try: if N unmatched validators and N unmatched val-peers, match by order
		// (less reliable but better than nothing)
		if len(unmatchedVals) > 1 {
			valPeers := []node.Peer{}
			for _, p := range unmatchedPeers {
				if strings.Contains(strings.ToLower(p.Moniker), "val") {
					valPeers = append(valPeers, p)
				}
			}
			if len(valPeers) == len(unmatchedVals) {
				for i, addr := range unmatchedVals {
					s.Client.Names.Register(addr, valPeers[i].Moniker)
				}
			}
		}
	}

	return snap
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

		timeStr := snap.Timestamp.Format("15:04:05")

		// Broadcast to SSE clients (legacy)
		s.broadcastSSE("time", timeStr)
		broadcastSSEJSON := func(event string, data interface{}) {
			if b, err := json.Marshal(data); err == nil {
				s.broadcastSSE(event, string(b))
			}
		}
		if snap.Status != nil {
			broadcastSSEJSON("status", snap.Status)
		}
		if snap.Peers != nil {
			broadcastSSEJSON("peers", snap.Peers)
		}
		if report := s.buildVotesReport(snap); report != nil {
			broadcastSSEJSON("votes", report)
		}
		broadcastSSEJSON("checks", s.buildCheckData(snap))

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
			s.broadcastWS(wsMsg{Type: "log", Data: entry})
			s.parseLogEvent(entry)
		}

		rc.Close()
		log.Printf("log stream ended, restarting in 2s")
		time.Sleep(2 * time.Second)
	}
}

// parseLogEvent extracts structured events from log lines
// parseLogEvent extracts structured events from a parsed log entry.
func (s *Server) parseLogEvent(entry LogEntry) {
	now := time.Now()
	// Rate limit: don't trigger snapshot refresh more than once per second.
	if now.Sub(s.lastLogEvent) < time.Second {
		return
	}

	// Extract peer IP from the "peer" extra field ("nodeID@IP:port").
	if peer, ok := entry.Extra["peer"].(string); ok {
		if at := strings.Index(peer, "@"); at > 0 {
			hostPort := peer[at+1:]
			if col := strings.LastIndex(hostPort, ":"); col > 0 {
				s.peerActivity.Store(hostPort[:col], now)
			}
		}
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

// --- Log API ---

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if s.Backend == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"lines": []LogEntry{}, "count": 0})
		return
	}

	nInt := 200
	if n := r.URL.Query().Get("n"); n != "" {
		fmt.Sscanf(n, "%d", &nInt) // invalid values leave nInt at 200 (intentional)
	}

	lines, err := s.Backend.FetchLogs(ctx, nInt)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	entries := make([]LogEntry, len(lines))
	for i, l := range lines {
		e := parseGnolandLog(l)
		e.Msg = strings.ReplaceAll(e.Msg, "/root/", "~/")
		entries[i] = e
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"lines": entries, "count": len(entries)})
}

// --- Diagnose API ---

func (s *Server) handleDiagnose(w http.ResponseWriter, r *http.Request) {
	ip := r.URL.Query().Get("ip")
	moniker := r.URL.Query().Get("moniker")
	if ip == "" {
		http.Error(w, "missing ip parameter", 400)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	report := &DiagReport{
		IP:      ip,
		Moniker: moniker,
		Time:    time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}

	rpcURL := fmt.Sprintf("http://%s:%s", ip, rpcPort)
	pc := node.NewClient(rpcURL, 5*time.Second)

	// Check 1: RPC reachable
	status, err := pc.GetStatus(ctx)
	if err != nil {
		report.Checks = append(report.Checks, DiagCheck{"rpc", "err", "Not reachable: " + err.Error()})
		// Check what we know from our P2P side
		snap := s.getSnapshot()
		if snap != nil {
			for _, p := range snap.Peers {
				if p.RemoteIP == ip && p.PeerHeight != "" {
					report.Checks = append(report.Checks, DiagCheck{"gossip", "ok", fmt.Sprintf("We see their consensus at h=%s (from our P2P)", p.PeerHeight)})
					break
				}
			}
		}
	} else {
		report.Checks = append(report.Checks, DiagCheck{"rpc", "ok", "Reachable at " + rpcURL})

		// Check 2: Block height
		h := status.SyncInfo.LatestBlockHeight
		snap := s.getSnapshot()
		nh := "?"
		var nhInt int
		if snap != nil && snap.Consensus != nil {
			nh = snap.Consensus.Height
			fmt.Sscanf(nh, "%d", &nhInt)
		}

		// Check 2: Block height vs network
		var hInt int
		fmt.Sscanf(h, "%d", &hInt)
		if nhInt > 0 && hInt < nhInt-10 {
			report.Checks = append(report.Checks, DiagCheck{"height", "warn", fmt.Sprintf("Block %s — behind network (%s)", h, nh)})
		} else {
			report.Checks = append(report.Checks, DiagCheck{"height", "ok", fmt.Sprintf("Block %s (network: %s)", h, nh)})
		}

		// Check 3: Catching up
		if status.SyncInfo.CatchingUp {
			report.Checks = append(report.Checks, DiagCheck{"sync", "warn", "Node is catching up"})
		} else {
			report.Checks = append(report.Checks, DiagCheck{"sync", "ok", "Synced"})
		}

		// Check 4: Block time
		bt := status.SyncInfo.LatestBlockTime
		if strings.HasPrefix(bt, "1970") {
			// Check if genesis is in the future — if so, this is expected
			genTime := s.Client.Names.GenesisTime
			preGenesis := false
			if genTime != "" {
				if gt, err := time.Parse(time.RFC3339, genTime); err == nil && time.Now().Before(gt) {
					preGenesis = true
				}
			}
			if preGenesis {
				report.Checks = append(report.Checks, DiagCheck{"block_time", "ok", "Pre-genesis — waiting for genesis time"})
			} else {
				report.Checks = append(report.Checks, DiagCheck{"block_time", "warn", "Block time is 1970 — no blocks produced yet"})
			}
		} else {
			report.Checks = append(report.Checks, DiagCheck{"block_time", "ok", bt})
		}

		// Check 5: Validator info
		if status.ValidatorInfo.Address != "" {
			valAddr := status.ValidatorInfo.Address
			pubKey := s.Client.Names.PubKey(valAddr)
			valDetail := "Address: " + valAddr
			if pubKey != "" {
				valDetail += " | PubKey: " + pubKey
			}
			report.Checks = append(report.Checks, DiagCheck{"validator", "ok", valDetail})
			// Add copyable "addr pubkey # moniker" line
			valLine := valAddr
			if pubKey != "" {
				valLine += " " + pubKey
			}
			peerMoniker := status.NodeInfo.Moniker
			if moniker != "" {
				peerMoniker = moniker
			}
			valLine += " # " + peerMoniker
			report.Checks = append(report.Checks, DiagCheck{"val_identity", "ok", valLine})
			if moniker != "" {
				s.Client.Names.Register(valAddr, moniker)
			}
		} else {
			report.Checks = append(report.Checks, DiagCheck{"validator", "warn", "No validator address — not a validator?"})
		}

		// Check 6: Moniker
		if moniker != "" && status.NodeInfo.Moniker != moniker {
			report.Checks = append(report.Checks, DiagCheck{"moniker", "warn", fmt.Sprintf("Reports as '%s' but we expected '%s'", status.NodeInfo.Moniker, moniker)})
		} else {
			report.Checks = append(report.Checks, DiagCheck{"moniker", "ok", "Reports as: " + status.NodeInfo.Moniker})
		}

		// Check 7: Network/chain
		ourNet := ""
		if snap != nil && snap.Status != nil {
			ourNet = snap.Status.NodeInfo.Network
		}
		if ourNet != "" && status.NodeInfo.Network != ourNet {
			report.Checks = append(report.Checks, DiagCheck{"chain", "err", "Different chain! Theirs: " + status.NodeInfo.Network + ", ours: " + ourNet})
		} else {
			report.Checks = append(report.Checks, DiagCheck{"chain", "ok", status.NodeInfo.Network})
		}

		// Check 8: Version
		ourVer := ""
		if snap != nil && snap.Status != nil {
			ourVer = snap.Status.NodeInfo.Version
		}
		if ourVer != "" && status.NodeInfo.Version != ourVer {
			report.Checks = append(report.Checks, DiagCheck{"version", "warn", fmt.Sprintf("%s (ours: %s)", status.NodeInfo.Version, ourVer)})
		} else {
			report.Checks = append(report.Checks, DiagCheck{"version", "ok", status.NodeInfo.Version})
		}

		// Check 9: Net address
		report.Checks = append(report.Checks, DiagCheck{"net_address", "ok", status.NodeInfo.NetAddress})

		// Check 10: Their peers
		theirPeers, peerErr := pc.GetNetInfo(ctx)
		if peerErr != nil {
			report.Checks = append(report.Checks, DiagCheck{"their_peers", "warn", "Cannot fetch: " + peerErr.Error()})
		} else {
			if len(theirPeers) == 0 {
				report.Checks = append(report.Checks, DiagCheck{"their_peers", "err", "No peers connected!"})
			} else if len(theirPeers) < 3 {
				report.Checks = append(report.Checks, DiagCheck{"their_peers", "warn", fmt.Sprintf("%d peers (low)", len(theirPeers))})
			} else {
				report.Checks = append(report.Checks, DiagCheck{"their_peers", "ok", fmt.Sprintf("%d peers", len(theirPeers))})
			}
		}

		// Check 11: Their validator set
		theirVals, valErr := pc.GetValidators(ctx)
		if valErr != nil {
			report.Checks = append(report.Checks, DiagCheck{"their_valset", "warn", "Cannot fetch: " + valErr.Error()})
		} else {
			ourValCount := 0
			if snap != nil && snap.Consensus != nil {
				ourValCount = len(snap.Consensus.Votes)
			}
			if ourValCount > 0 && len(theirVals) != ourValCount {
				report.Checks = append(report.Checks, DiagCheck{"their_valset", "warn", fmt.Sprintf("%d validators (we have %d)", len(theirVals), ourValCount)})
			} else {
				report.Checks = append(report.Checks, DiagCheck{"their_valset", "ok", fmt.Sprintf("%d validators", len(theirVals))})
			}
		}

		// Check 12: Consensus state + gossip (combined)
		cs, _, csErr := pc.GetConsensusState(ctx)
		gossipLag := false
		if snap != nil {
			for _, p := range snap.Peers {
				if p.RemoteIP == ip && p.PeerHeight != "" {
					var ourView int
					fmt.Sscanf(p.PeerHeight, "%d", &ourView)
					if ourView > 0 && nhInt > 0 && ourView < nhInt-10 {
						gossipLag = true
						report.Checks = append(report.Checks, DiagCheck{"gossip", "warn", fmt.Sprintf("We see their consensus at h=%d but network is at %s — P2P gossip lag (sentry issue?)", ourView, nh)})
					} else if ourView > 0 {
						report.Checks = append(report.Checks, DiagCheck{"gossip", "ok", fmt.Sprintf("We see their consensus at h=%d", ourView)})
					}
					break
				}
			}
		}

		if csErr != nil {
			report.Checks = append(report.Checks, DiagCheck{"consensus", "err", "Cannot fetch: " + csErr.Error()})
		} else {
			csInfo := fmt.Sprintf("h=%s r=%s s=%s", cs.Height, cs.Round, cs.Step)
			var csHInt int
			fmt.Sscanf(cs.Height, "%d", &csHInt)
			if nhInt > 0 && csHInt < nhInt-10 {
				report.Checks = append(report.Checks, DiagCheck{"consensus", "warn", fmt.Sprintf("%s — behind network (%s)", csInfo, nh)})
			} else if gossipLag {
				report.Checks = append(report.Checks, DiagCheck{"consensus", "warn", fmt.Sprintf("%s — their RPC ok but our P2P view is stale", csInfo)})
			} else {
				report.Checks = append(report.Checks, DiagCheck{"consensus", "ok", csInfo})
			}

			// Check round age — only warn if THEY are stuck but network is not
			if cs.RoundStartTime != "" {
				if t, err := time.Parse(time.RFC3339Nano, cs.RoundStartTime); err == nil {
					age := time.Since(t).Truncate(time.Second)
					networkAlsoStuck := false
					if snap != nil && snap.Consensus != nil && snap.Consensus.RoundStartTime != "" {
						if nt, err := time.Parse(time.RFC3339Nano, snap.Consensus.RoundStartTime); err == nil {
							networkAlsoStuck = time.Since(nt) > 30*time.Second
						}
					}
					if age > 30*time.Second && !networkAlsoStuck {
						report.Checks = append(report.Checks, DiagCheck{"round_age", "warn", fmt.Sprintf("Stuck on round for %s (network is advancing)", age)})
					} else if age > 30*time.Second {
						report.Checks = append(report.Checks, DiagCheck{"round_age", "ok", fmt.Sprintf("%s (network also stuck)", age)})
					} else {
						report.Checks = append(report.Checks, DiagCheck{"round_age", "ok", age.String()})
					}
				}
			}
		}
	}

	// Store and broadcast
	s.diagmu.Lock()
	s.diagData[ip] = report
	s.diagmu.Unlock()

	s.broadcastWS(wsMsg{Type: "diagnose", Data: report})

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(report)
}

func (s *Server) handleDiagnoseList(w http.ResponseWriter, r *http.Request) {
	s.diagmu.RLock()
	defer s.diagmu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(s.diagData)
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

func (s *Server) handleNotifyChannels(w http.ResponseWriter, r *http.Request) {
	if s.PushManager == nil {
		http.Error(w, "notifications not configured", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"channels": s.PushManager.NotifyChannels(),
	})
}

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	if s.PushManager == nil {
		http.Error(w, "notifications not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Channels []int  `json:"channels"`
		Message  string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	results := s.PushManager.SendTestNotify(req.Channels, req.Message)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"results": results,
	})
}

// Run starts the web server, publish loop, and log streamer.
// computeGenesisSHA hashes the local genesis file to avoid hardcoding.
func (s *Server) computeGenesisSHA() string {
	candidates := []string{}
	if s.GenesisPath != "" {
		candidates = append(candidates, s.GenesisPath)
	}
	if s.DataDir != "" {
		candidates = append(candidates, s.DataDir+"/config/genesis.json")
	}
	// Try relative paths
	entries, _ := os.ReadDir(".")
	for _, e := range entries {
		if e.IsDir() {
			candidates = append(candidates, e.Name()+"/gnoland-data/config/genesis.json")
		}
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		h := sha256.Sum256(data)
		return fmt.Sprintf("%x", h)
	}
	return ""
}

// gnoRootDir tries to find the gno source directory.
func (s *Server) gnoRootDir() string {
	// Check GNOROOT env
	if v := os.Getenv("GNOROOT"); v != "" {
		return v
	}
	// Common locations
	for _, p := range []string{"/root/gno", "/usr/local/src/gno"} {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
	}
	return ""
}


func (s *Server) Run(ctx context.Context) error {
	s.genesisSHA = s.computeGenesisSHA()
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
	mux.HandleFunc("/api/reset-cache", s.handleResetCache)
	mux.HandleFunc("/api/logs", s.handleLogs)
	mux.HandleFunc("/api/diagnose", s.handleDiagnose)
	mux.HandleFunc("/api/diagnoses", s.handleDiagnoseList)
	mux.HandleFunc("/events", s.handleEvents)
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
	mux.HandleFunc("/api/notify/channels", s.handleNotifyChannels)
	mux.HandleFunc("/api/notify/test", s.handleNotifyTest)

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

	log.Printf("gnockpit web: http://%s", s.Addr)
	return srv.ListenAndServe()
}
