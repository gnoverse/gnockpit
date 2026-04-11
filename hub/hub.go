package hub

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gnoverse/gnockpit/node"
	"github.com/gorilla/websocket"
)

// ProbeState holds the latest data received from a connected probe.
type ProbeState struct {
	Name       string         `json:"name"`
	Snapshot   *node.Snapshot `json:"snapshot,omitempty"`
	LastUpdate time.Time      `json:"last_update"`
	Connected  bool           `json:"connected"`
	ChainID    string         `json:"chain_id,omitempty"`
	Height     string         `json:"height,omitempty"`
	// Rate limit stats
	MsgPerMin  int `json:"msg_per_min"`
	Violations int `json:"violations"`
}

// probeConn tracks a connected probe's WebSocket and rate state.
type probeConn struct {
	name      string
	conn      *websocket.Conn
	msgTimes  []time.Time // sliding window for rate limiting
	violations int
}

// Hub aggregates data from probes and serves the cluster dashboard.
type Hub struct {
	Tokens   *TokenStore
	Addr     string
	MaxRate  int // max snapshots/sec per probe (0 = unlimited)
	MaxMsgKB int // max message size in KB (0 = unlimited)

	mu     sync.RWMutex
	probes map[string]*ProbeState // name -> state
	conns  map[string]*probeConn  // name -> active connection

	// Browser WebSocket clients
	wsmu      sync.RWMutex
	wsClients map[*wsClient]struct{}

	// Doctor clients
	doctorMu      sync.RWMutex
	doctorClients map[*wsClient]struct{}
}

type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

type wsMsg struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// New creates a Hub. Tokens must already be initialized.
func New(tokens *TokenStore, addr string) *Hub {
	return &Hub{
		Tokens:        tokens,
		Addr:          addr,
		MaxRate:       1,    // 1 snapshot/sec default
		MaxMsgKB:      1024, // 1MB default
		probes:        make(map[string]*ProbeState),
		conns:         make(map[string]*probeConn),
		wsClients:     make(map[*wsClient]struct{}),
		doctorClients: make(map[*wsClient]struct{}),
	}
}

// Probes returns a snapshot of all probe states.
func (h *Hub) Probes() map[string]*ProbeState {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]*ProbeState, len(h.probes))
	for k, v := range h.probes {
		cp := *v
		out[k] = &cp
	}
	return out
}

// ProbeState returns one probe's state by name.
func (h *Hub) Probe(name string) (*ProbeState, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, ok := h.probes[name]
	if !ok {
		return nil, false
	}
	cp := *p
	return &cp, true
}

// --- Probe WebSocket endpoint ---

func (h *Hub) HandleProbeWS(w http.ResponseWriter, r *http.Request) {
	// Auth
	auth := r.Header.Get("Authorization")
	if auth == "" {
		// Try query param for convenience
		auth = "Bearer " + r.URL.Query().Get("token")
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" || token == auth {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name, err := h.Tokens.Verify(token)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("hub: probe ws upgrade: %v", err)
		return
	}

	if h.MaxMsgKB > 0 {
		conn.SetReadLimit(int64(h.MaxMsgKB) * 1024)
	}

	// Probes may take a long time to fetch snapshots (100 block queries),
	// so set a generous read deadline and keep it alive with pongs.
	conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		return nil
	})

	pc := &probeConn{name: name, conn: conn}

	h.mu.Lock()
	// Disconnect existing connection for this probe if any
	if old, ok := h.conns[name]; ok {
		old.conn.Close()
	}
	h.conns[name] = pc
	if _, ok := h.probes[name]; !ok {
		h.probes[name] = &ProbeState{Name: name}
	}
	h.probes[name].Connected = true
	h.mu.Unlock()

	log.Printf("hub: probe %q connected", name)
	h.broadcastBrowser(wsMsg{Type: "probe_connected", Data: name})

	// Ping probe every 30s to keep connection alive
	go func() {
		pingT := time.NewTicker(30 * time.Second)
		defer pingT.Stop()
		for {
			select {
			case <-pingT.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					return
				}
			}
		}
	}()

	defer func() {
		conn.Close()
		h.mu.Lock()
		if cur, ok := h.conns[name]; ok && cur == pc {
			delete(h.conns, name)
			if ps, ok := h.probes[name]; ok {
				ps.Connected = false
			}
		}
		h.mu.Unlock()
		log.Printf("hub: probe %q disconnected", name)
		h.broadcastBrowser(wsMsg{Type: "probe_disconnected", Data: name})
	}()

	for {
		conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if !h.checkRate(pc) {
			conn.WriteJSON(wsMsg{Type: "error", Data: "rate_limited"})
			continue
		}
		h.handleProbeMessage(name, msg)
	}
}

func (h *Hub) checkRate(pc *probeConn) bool {
	if h.MaxRate <= 0 {
		return true
	}
	now := time.Now()
	// Keep only messages in the last second
	cutoff := now.Add(-time.Second)
	clean := pc.msgTimes[:0]
	for _, t := range pc.msgTimes {
		if t.After(cutoff) {
			clean = append(clean, t)
		}
	}
	pc.msgTimes = append(clean, now)
	if len(pc.msgTimes) > h.MaxRate {
		pc.violations++
		h.mu.Lock()
		if ps, ok := h.probes[pc.name]; ok {
			ps.Violations = pc.violations
		}
		h.mu.Unlock()
		// Auto-disconnect at 10x
		if pc.violations > h.MaxRate*10 {
			log.Printf("hub: probe %q exceeded rate limit 10x, disconnecting", pc.name)
			pc.conn.Close()
		}
		return false
	}
	return true
}

func (h *Hub) handleProbeMessage(name string, raw []byte) {
	var msg struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		log.Printf("hub: probe %q bad message: %v", name, err)
		return
	}

	switch msg.Type {
	case "snapshot":
		var snap node.Snapshot
		if err := json.Unmarshal(msg.Data, &snap); err != nil {
			log.Printf("hub: probe %q bad snapshot: %v", name, err)
			return
		}
		now := time.Now()
		h.mu.Lock()
		ps := h.probes[name]
		ps.Snapshot = &snap
		ps.LastUpdate = now
		ps.Connected = true
		if snap.Status != nil {
			ps.ChainID = snap.Status.NodeInfo.Network
			ps.Height = snap.Status.SyncInfo.LatestBlockHeight
		}
		// Count msg/min
		if h.conns[name] != nil {
			recent := 0
			cutoff := now.Add(-time.Minute)
			for _, t := range h.conns[name].msgTimes {
				if t.After(cutoff) {
					recent++
				}
			}
			ps.MsgPerMin = recent
		}
		h.mu.Unlock()

		h.broadcastBrowser(wsMsg{Type: "probe_update", Data: map[string]interface{}{
			"probe": name,
			"data":  &snap,
		}})
		// Also broadcast health
		health := h.ComputeHealth()
		h.broadcastBrowser(wsMsg{Type: "cluster_health", Data: health})
		h.broadcastDoctors(wsMsg{Type: "cluster_health", Data: health})
	}
}

// --- Browser WebSocket ---

func (h *Hub) HandleBrowserWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("hub: browser ws upgrade: %v", err)
		return
	}
	client := &wsClient{conn: conn, send: make(chan []byte, 64)}
	h.wsmu.Lock()
	h.wsClients[client] = struct{}{}
	h.wsmu.Unlock()

	// Send initial state
	h.sendInitialState(client)

	go func() {
		defer conn.Close()
		for msg := range client.send {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}()
	go func() {
		defer func() {
			h.wsmu.Lock()
			delete(h.wsClients, client)
			h.wsmu.Unlock()
			close(client.send)
			conn.Close()
		}()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
}

func (h *Hub) sendInitialState(client *wsClient) {
	probes := h.Probes()
	b, _ := json.Marshal(wsMsg{Type: "cluster_snapshot", Data: probes})
	select {
	case client.send <- b:
	default:
	}
	health := h.ComputeHealth()
	b, _ = json.Marshal(wsMsg{Type: "cluster_health", Data: health})
	select {
	case client.send <- b:
	default:
	}
}

func (h *Hub) broadcastBrowser(msg wsMsg) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.wsmu.RLock()
	defer h.wsmu.RUnlock()
	for c := range h.wsClients {
		select {
		case c.send <- b:
		default:
		}
	}
}

// --- Doctor WebSocket ---

func (h *Hub) HandleDoctorWS(w http.ResponseWriter, r *http.Request) {
	// Doctors auth the same way as probes
	auth := r.Header.Get("Authorization")
	if auth == "" {
		auth = "Bearer " + r.URL.Query().Get("token")
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" || token == auth {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if _, err := h.Tokens.Verify(token); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &wsClient{conn: conn, send: make(chan []byte, 64)}
	h.doctorMu.Lock()
	h.doctorClients[client] = struct{}{}
	h.doctorMu.Unlock()

	// Send current state
	h.sendInitialState(client)

	go func() {
		defer conn.Close()
		for msg := range client.send {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}()
	go func() {
		defer func() {
			h.doctorMu.Lock()
			delete(h.doctorClients, client)
			h.doctorMu.Unlock()
			close(client.send)
			conn.Close()
		}()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
}

func (h *Hub) broadcastDoctors(msg wsMsg) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.doctorMu.RLock()
	defer h.doctorMu.RUnlock()
	for c := range h.doctorClients {
		select {
		case c.send <- b:
		default:
		}
	}
}

// --- HTTP API ---

func (h *Hub) HandleAPIProbes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(h.Probes())
}

func (h *Hub) HandleAPIProbe(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/probes/")
	if name == "" {
		http.Error(w, "missing probe name", 400)
		return
	}
	ps, ok := h.Probe(name)
	if !ok {
		http.Error(w, "probe not found", 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(ps)
}

func (h *Hub) HandleAPIMode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	fmt.Fprintf(w, `{"mode":"cluster"}`)
}
