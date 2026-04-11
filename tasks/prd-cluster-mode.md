# PRD: Cluster Mode

## Introduction

gnockpit currently monitors a single gno.land node. Cluster mode adds the ability to monitor multiple nodes from a single dashboard. A central **server** aggregates data from remote **probes** that run on each validator node, connected via persistent WebSocket with bearer token auth.

The existing single-node mode remains the default — zero config, no auth, local RPC. Cluster mode is opt-in.

## Modes

| Mode | Command | Description |
|------|---------|-------------|
| **single** | `gnockpit web` (default) | Current behavior. Monitors local RPC. No auth. |
| **server** | `gnockpit server` | Central aggregator. Accepts probe connections. Serves cluster dashboard. |
| **probe** | `gnockpit probe` | Runs on a validator node. Connects to server via WebSocket. Pushes snapshots in real-time. |
| **doctor** | `gnockpit doctor` | Standalone diagnostics. One-shot CLI, against a server, or `--live` for a persistent dashboard tab on the hub. |

The server is a pure aggregator — it does not monitor a local RPC itself. To monitor the server's own node, run a probe alongside it.

## Goals

- Monitor N validator nodes from a single dashboard with real-time updates
- Keep single-node mode unchanged — simplest possible setup
- Detect health issues across the cluster: height drift, staleness, consensus mismatches, validator set differences
- Bearer token auth per probe, managed via companion CLI (no restart needed)
- Multitenant data: each probe's snapshots stored/keyed independently

## User Stories

### US-001: Server mode with probe WebSocket endpoint
**Description:** As an operator, I want to run a gnockpit server that accepts WebSocket connections from probes so I can aggregate data from multiple nodes.

**Acceptance Criteria:**
- [ ] `gnockpit server --port 8080` starts the server with cluster dashboard
- [ ] Server exposes `/ws/probe` endpoint for probe connections
- [ ] Probes authenticate via `Authorization: Bearer <token>` header on WS upgrade
- [ ] Unauthorized connections are rejected with 401
- [ ] Server stores latest snapshot per probe in memory, keyed by probe ID
- [ ] Server broadcasts probe updates to dashboard clients via existing WS mechanism

### US-002: Probe mode with persistent WebSocket to server
**Description:** As an operator, I want to run a gnockpit probe on each validator that pushes data to the central server in real-time.

**Acceptance Criteria:**
- [ ] `gnockpit probe --server wss://gnockpit.example.com --token <bearer>` connects to server
- [ ] Probe runs the existing snapshot fetch loop (same as single-node mode)
- [ ] Each snapshot is sent to server as a JSON message over WebSocket
- [ ] Probe includes its probe ID (derived from token or moniker) in each message
- [ ] Probe auto-reconnects on disconnect with exponential backoff
- [ ] Probe also serves a local dashboard on `--port` (optional, default off) for local debugging
- [ ] Probe logs connection status to stderr

### US-003: Token management CLI
**Description:** As an operator, I want to generate and revoke bearer tokens for probes without restarting the server.

**Acceptance Criteria:**
- [ ] `gnockpit token create --name "moul-val-01" --db-path /path/to/gnockpit.db` generates a token and prints it
- [ ] `gnockpit token list --db-path /path/to/gnockpit.db` lists all tokens with name, created date, last seen
- [ ] `gnockpit token revoke --name "moul-val-01" --db-path /path/to/gnockpit.db` revokes a token
- [ ] Tokens stored in SQLite (same DB as push notifications)
- [ ] Server checks token validity on each WS connection (no restart needed)
- [ ] Token table: `id`, `name` (probe label), `token_hash` (bcrypt), `created_at`, `revoked_at`

### US-004: Multitenant snapshot storage
**Description:** As a developer, I need probe data separated per-probe so that one probe's data doesn't interfere with another's.

**Acceptance Criteria:**
- [ ] Server maintains `map[probeID]*node.Snapshot` in memory
- [ ] Each probe's snapshot includes metadata: probe name, last update time, connection status
- [ ] When a probe disconnects, its last snapshot is retained with a "disconnected" status
- [ ] Stale detection: snapshots older than 3x the probe's interval are marked stale
- [ ] API endpoint `GET /api/probes` returns list of probes with status summary
- [ ] API endpoint `GET /api/probes/{id}` returns full snapshot for a probe

### US-005: Cluster overview dashboard (default view)
**Description:** As an operator, I want to see all my nodes at a glance when I open the server dashboard.

**Acceptance Criteria:**
- [ ] Cluster view is the default tab when server has multiple probes
- [ ] Shows a card per probe: name, chain-id, height, round/step, peer count, last update age
- [ ] Cards are color-coded: green (healthy), yellow (warning), red (error/stale)
- [ ] Health rules applied per-card (see US-007)
- [ ] Click on a card navigates to that probe's full dashboard
- [ ] Cluster summary bar at top: total probes, healthy count, warning count, error count

### US-006: Single-probe focus view (dropdown)
**Description:** As an operator, I want to drill into a specific probe and see the full dashboard for that node.

**Acceptance Criteria:**
- [ ] Dropdown in header to select a probe (or "Cluster" for overview)
- [ ] When a probe is selected, the existing dashboard renders using that probe's snapshot
- [ ] All existing panels work: status, peers, consensus, votes, signing stats, blocks, logs, system info
- [ ] Logs panel shows "not available" for remote probes (logs are local only)
- [ ] Real-time updates via WebSocket continue for the selected probe

### US-007: Cluster health detection
**Description:** As an operator, I want the cluster tab to surface problems across my nodes automatically.

**Acceptance Criteria:**
- [ ] **Height drift:** Alert when any probe's height is >5 blocks behind the highest probe
- [ ] **Staleness:** Alert when a probe hasn't sent data in >30 seconds (configurable)
- [ ] **Disconnected:** Alert when a probe's WebSocket is disconnected
- [ ] **Consensus mismatch:** Alert when probes report different rounds for the same height
- [ ] **Validator set divergence:** Alert when probes have different validator counts or different validator addresses
- [ ] **Chain ID mismatch:** Alert when probes report different chain IDs (possible fork or misconfiguration)
- [ ] **Version mismatch:** Alert when probes run different node software versions
- [ ] **Signing gaps:** Alert when a validator visible to one probe is missing blocks on another
- [ ] Health issues displayed as a list in the cluster tab with severity, affected probes, and detail
- [ ] Issues are computed on each snapshot update, not stored

### US-008: Hub rate limiting per probe
**Description:** As a server operator, I want the hub to protect itself from misbehaving probes by enforcing per-member thresholds.

**Acceptance Criteria:**
- [ ] Server enforces max snapshot push rate per probe (default: 1/sec, configurable via `--probe-rate-limit`)
- [ ] Server enforces max message size per probe (default: 1MB)
- [ ] Probe exceeding rate limit receives `{"type":"error","data":"rate_limited"}` and message is dropped
- [ ] Probe exceeding 10x rate limit in a window is auto-disconnected with reason
- [ ] Server logs rate limit violations with probe name
- [ ] Per-probe stats tracked: messages/min, bytes/min, violations count
- [ ] Stats visible in cluster dashboard probe cards and via `GET /api/probes`

### US-009: Doctor — standalone diagnostics
**Description:** As an operator, I want a `doctor` command that can diagnose node health in multiple modes: one-shot CLI, against a remote server, or as a live dashboard tab.

**Acceptance Criteria:**
- [ ] **One-shot CLI:** `gnockpit doctor --rpc http://127.0.0.1:26657` runs all health checks against a single node, prints results, exits with non-zero on errors
- [ ] Checks include: RPC reachable, height advancing, not catching up, peers connected, consensus progressing, validator signing, block time reasonable, disk/mem/load (if local)
- [ ] Output: colored table by default, `--json` for machine-readable
- [ ] **Against server:** `gnockpit doctor --server https://hub.example.com --token <bearer>` fetches all probe snapshots and runs cross-cluster checks (same as US-007 health detection), prints report, exits
- [ ] **Live mode:** `gnockpit doctor --live --server https://hub.example.com --token <bearer>` subscribes to server via WebSocket, continuously evaluates health, displays updating TUI or serves a dashboard
- [ ] Live mode registers as a "doctor" client on the hub — server exposes a "Doctor" tab in the cluster dashboard showing the doctor's findings in real-time
- [ ] Doctor findings are structured: `{severity: "warn"|"error"|"ok", check: "height_drift", probes: ["a","b"], detail: "..."}`
- [ ] Doctor can also run against a single-node `gnockpit web` instance: `gnockpit doctor --server http://localhost:8080`

### US-010: Side-by-side comparison view
**Description:** As an operator, I want to compare two probes side-by-side to debug discrepancies.

**Acceptance Criteria:**
- [ ] "Compare" tab/button available from cluster view
- [ ] Two-column layout with a probe selector per column
- [ ] Shows key metrics side by side: height, round, step, peers, validators, signing stats
- [ ] Differences highlighted (e.g., different height in red)
- [ ] Updates in real-time as snapshots arrive

## Functional Requirements

- FR-1: Add `server` subcommand that starts the cluster server (HTTP + WS for browser clients + WS for probe connections)
- FR-2: Add `probe` subcommand that connects to a server and pushes snapshots
- FR-3: Add `token` subcommand group (`create`, `list`, `revoke`) for bearer token management
- FR-4: Probe WebSocket protocol: probe sends `{"type":"snapshot","data":<Snapshot>}` messages; server sends `{"type":"ack"}` or `{"type":"config","data":{"interval":5}}` to adjust probe interval remotely
- FR-5: Server stores probe metadata in SQLite: `probes` table with `id`, `name`, `token_id`, `chain_id`, `last_seen`, `status`
- FR-6: Server broadcasts to browser clients: `{"type":"probe_update","probe":"<name>","data":<Snapshot>}` and `{"type":"cluster_health","data":<HealthReport>}`
- FR-7: Existing single-node `web` subcommand unchanged — no cluster code loaded when running in single mode
- FR-8: Push notifications (existing) work per-probe in cluster mode — operator can subscribe to alerts for specific probes
- FR-9: Server serves the same embedded `index.html` but with cluster-aware JavaScript that detects mode from `/api/mode` endpoint
- FR-10: Server enforces per-probe rate limits (snapshot frequency, message size) with configurable thresholds
- FR-11: Add `doctor` subcommand with three modes: one-shot CLI, against-server report, live subscription
- FR-12: Doctor live mode registers on hub and exposes findings as a "Doctor" tab in the cluster dashboard
- FR-13: Doctor checks are a superset of US-007 cluster health checks plus single-node checks (RPC reachable, height advancing, peers, consensus, signing, system resources)

## Non-Goals

- No multi-server federation (single server only)
- No probe auto-discovery (probes must be configured with server URL + token)
- No historical data / time-series storage (snapshots are current state only)
- No RBAC — all tokens have equal access
- No probe-to-probe communication
- No remote log streaming from probes (logs stay local)

## Technical Considerations

- Probe reuses the existing `fetchSnapshot()` loop from `web/server.go` — extract it into a shared function
- WebSocket library: reuse existing `gorilla/websocket`
- Token hashing: use `golang.org/x/crypto/bcrypt` (new dependency)
- SQLite: reuse existing `push.DB` — add `tokens` and `probes` tables
- Server memory: ~50KB per probe snapshot × N probes. For <100 probes this is negligible
- Probe WebSocket reconnect: start at 1s, double up to 30s, reset on successful connect
- The cluster health engine should run as a goroutine that recomputes on every probe update

## Success Metrics

- Operator can set up a 3-node cluster in under 5 minutes
- Cluster dashboard loads and shows all probes within 2 seconds
- Height drift detected within one snapshot interval (5s default)
- Zero impact on single-node mode performance

## Open Questions

- Should probes also push system info (disk, memory, load) to the server? (Probably yes — it's in the snapshot already)
- Should the comparison view support >2 probes? (Start with 2, extend later)
- Should push notifications reference the probe name in alerts? (Yes — "moul-val-01: chain stuck")
