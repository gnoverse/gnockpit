# Agent Guide — gnockpit

## What is this?

gnockpit is a real-time monitoring dashboard for gno.land validator nodes. It's a single Go binary that connects to a gnoland node's Tendermint RPC and provides both a web UI and CLI tools.

## Architecture

```
main.go          — CLI entry point (cobra commands: status, peers, web, etc.)
node/            — RPC client + types (pure data layer, no UI)
  client.go      — HTTP client for Tendermint RPC (/status, /net_info, /validators, etc.)
  types.go       — All data types (Status, Peer, Validator, Snapshot, SigningStats, etc.)
  validators.go  — NameRegistry: maps validator addresses ↔ monikers, persists to JSON
web/             — Web dashboard
  server.go      — HTTP/WebSocket server, background data fetcher, system info collector
  index.html     — Single-page dashboard (embedded via go:embed, vanilla JS, no framework)
```

## Key Concepts

### Data Flow
1. `server.go` runs a **publish loop** every N seconds (default 5s)
2. Each cycle calls `fetchSnapshot()` which queries the local RPC for status, validators, consensus, peers, block signing stats
3. The snapshot is broadcast to all connected WebSocket clients as typed messages: `status`, `peers`, `votes`, `checks`, `signing`
4. `index.html` receives these messages and updates the DOM in-place
5. Log lines are streamed from `journalctl` via a separate goroutine

### NameRegistry (validators.go)
Maps validator addresses to human-readable monikers. This is critical because Tendermint RPC only returns addresses, not names. Discovery happens through:
- **Genesis file** — validator names from genesis.json at startup
- **Peer RPC queries** — when we query a peer's `/status`, we get their validator_info.address + moniker
- **Node-ID verification** — on shared IPs (multiple nodes same IP), the RPC response's node_info.id is checked against the peer's P2P node-id before trusting the mapping
- **Correlation** — unmatched validators are matched to unmatched peers heuristically

The registry persists to a JSON file (`--names` flag) so discoveries survive restarts.

### Signing Stats (client.go: GetSigningStats)
Fetches the last N blocks (default 100), extracts:
- Who signed each block (from `last_commit.precommits`)
- Who proposed each block (from `header.proposer_address`)
- Block timestamps → compute per-proposer average block time
- Sign rate per validator (signed/total as percentage)

### Doctor (index.html: runDoctor)
Client-side diagnostic checks that run on every data update:
- Prevote/precommit threshold not reached
- Vote gossip fragmentation (peers see different vote counts)
- Split-height deadlock (validators at different consensus heights)
- Consensus frozen (stuck for >5min or >1h)
- Old-chain peers (peer height way higher = different genesis)
- Validators not voting when network is stuck

### Diagnose (server.go: handleDiagnose)
Per-node diagnostics triggered by clicking the 💡 button. Queries the target node's RPC and checks: reachability, height, sync, block time, validator status, chain-id match, version, peer count, consensus state, round age.

## Important Patterns

### No hardcoded values
Everything is auto-detected or configurable via flags. The tool works on any gno.land chain without code changes:
- Chain ID → from RPC `/status`
- Service name → from chain ID + `.service`
- Genesis path → from `--data-dir` + `/config/genesis.json`
- Validator names → discovered dynamically from peers
- Gno source path → from `GNOROOT` env

### Single HTML file
`web/index.html` is a complete SPA with no build step. Vanilla JS, CSS variables for dark theme, no external dependencies. It's embedded in the binary via `go:embed`.

### WebSocket protocol
Messages are JSON with `{type: string, data: any}`. Types: `status`, `peers`, `votes`, `checks`, `signing`, `time`, `log`, `diagnose`.

### Scroll preservation
`renderNetwork()` saves `window.scrollY` before DOM updates and restores it after, preventing the page from jumping during live updates.

### Collapsible sections
Each card has `<h2 data-section="name">` that toggles `.collapsed` class. State persisted in `localStorage` under key `gnockpit-collapsed`.

## Common Tasks

### Adding a new RPC data source
1. Add the fetch method to `node/client.go`
2. Add types to `node/types.go`
3. Call it in `web/server.go: fetchSnapshot()`
4. Broadcast it in the publish loop
5. Handle the WebSocket message in `web/index.html`

### Adding a new doctor check
Add to the `runDoctor()` function in `index.html`. Push to the `items` array with `{level: 'crit'|'warn'|'ok', title: string, detail: string, action: string}`.

### Adding a new diagnose check
Add to `handleDiagnose()` in `server.go`. Append to `report.Checks` with `DiagCheck{Name, Status, Detail}`.

### Adding a new peer table column
1. Add `<th>` in the header row
2. Add the cell creation in the validator loop (after `// Section 1: Validators`)
3. Add the cell for non-validator peers (after `// Section 2: Non-validator peers`)
4. Update all `colSpan` values
5. If sortable, add `data-sort="key"` to `<th>` and handle in the sort function

## Testing
```bash
go test ./...
```

Tests are minimal — focused on JSON parsing and name registry logic. The web UI is tested manually.

## Deployment

Typical systemd service:
```ini
[Service]
ExecStart=/usr/local/bin/gnockpit web \
  --rpc http://127.0.0.1:26657 \
  --data-dir /path/to/gnoland-data \
  --service chainname.service \
  --port 8080 --addr 127.0.0.1 --interval 5s
```

Usually behind a reverse proxy (Caddy/nginx) for TLS.
