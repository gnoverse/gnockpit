# Agent Guide — gnockpit

## What is this?

gnockpit is a real-time monitoring dashboard for gno.land validator nodes. It's a single Go binary (`gnockpit [flags]`, no subcommands) that connects to one or more gnoland RPC endpoints (`-rpc`, repeatable), consolidates their views, and serves a live web dashboard, with web-push/PWA alerts and external (Shoutrrr) notifications.

## Architecture

```
main.go          — CLI entry point (single command: starts the web dashboard)
node/            — RPC client + types (pure data layer, no UI)
  client.go      — HTTP client for Tendermint RPC (/status, /net_info, /validators, etc.)
  sources.go     — Sources: an ordered set of RPC endpoints polled together; best-source pick for global data + peer union/dedup (MergePeers)
  ip.go          — public-IP validation (IsPublicIP): only public IPs are ever geolocated or displayed
  types.go       — All data types (Status, Peer, Validator, Snapshot, SigningStats, etc.)
  validators.go  — NameRegistry: maps validator addresses ↔ monikers, persists to JSON
  geoip.go       — DB-IP City Lite: IP → coordinates + country (auto-downloaded, monthly)
  asn.go         — DB-IP ASN Lite: IP → ASN + cloud provider (auto-downloaded, monthly)
history/         — SQLite store of per-block validator signing (shares the push DB handle)
  store.go       — RecordBlocks / MissedInWindow / MissedByWindows / Prune; missed-block windows
web/             — Web dashboard + HTTP API
  server.go      — HTTP/WebSocket server, background data fetcher, system info collector
  status.go      — /api/status: health + curated network state / recent blocks / peer + validator columns
  stats.go       — /api/stats: missed-block windows, provider/country aggregates, set health, Nakamoto
  index.html     — Single-page dashboard (embedded via go:embed, vanilla JS, no framework)
```

## Key Concepts

### Data Flow
1. `server.go` runs a **publish loop** every N seconds (default 5s)
2. Each cycle calls `fetchSnapshot()` which polls every configured source (`node/sources.go: Poll`): global chain data (status, validators, consensus, signing) comes from the freshest reachable source; peers are unioned across all reachable sources and deduped by node ID (`MergePeers`) before their RPCs are probed. All sources down → `snap.Error` drives the "connecting to sources" banner.
3. The snapshot is broadcast to all connected WebSocket clients as typed messages: `status`, `peers`, `votes`, `checks`, `signing`
4. `index.html` receives these messages and updates the DOM in-place
5. Each cycle also geolocates peers (`geoip`) and resolves their cloud provider (`asn`) — using each peer's resolved public IP only (peers with no usable public IP are geolocated to nothing and bucketed "Unknown" on the map) — and records the recent blocks' missing-validator sets into the `history` store (forward-only, deduped by height). This powers the network map, the Country/Provider columns, and per-validator missed-block windows (1h/24h/7d/30d/total). Validators inherit the country/provider of their correlated peer (matched via `ValAddress`), so validators no source is peered with have none.

### NameRegistry (validators.go)
Maps validator addresses to human-readable monikers. This is critical because Tendermint RPC only returns addresses, not names. Discovery happens through:
- **Genesis (RPC)** — validator names and the genesis time are seeded at startup by streaming the node's `/genesis` endpoint, parsing only the head (the large `app_state` is never downloaded)
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

This is a live 100-block window. Longer-term **missed-block counts** come from the
`history` store instead (Data Flow #5): it records each block's missing set forward
and aggregates over 1h/24h/7d/30d/total via `MissedByWindows`. History is
forward-only (never backfilled) and pruned past 31 days.

### HTTP API (CORS-open JSON)
- `/api/status` (status.go) — health summary (retrocompat `status`/`chain`/`height`/`reason`/`time`) plus `network`, `recent_blocks` (last 100), and per-`peers`/`validators` column data.
- `/api/stats` (stats.go) — per-validator missed-block windows, provider + country aggregates, validator-set health, Nakamoto coefficient.
- `/api` (server.go) — raw `Snapshot` dump (unstable shape; debugging).
- Also: `/badge.svg`, `/ws`, `/api/boot`, `/api/push/*`, `/api/notify/*`.

## Important Patterns

### No hardcoded values
Everything is auto-detected or configurable via flags. The tool works on any gno.land chain without code changes:
- Chain ID → from RPC `/status`
- Genesis time + validator names → streamed from RPC `/genesis` (head only)
- Validator names → also discovered dynamically from peers

### Single HTML file
`web/index.html` is a complete SPA with no build step. Vanilla JS, CSS variables for dark theme, no external dependencies. It's embedded in the binary via `go:embed`.

### WebSocket protocol
Messages are JSON with `{type: string, data: any}`. Types: `snapshot` (full state on connect), `update` (batched periodic), and individual `status`, `peers`, `votes`, `checks`, `signing`, `time`.

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
ExecStart=/usr/local/bin/gnockpit \
  --rpc http://127.0.0.1:26657 \
  --port 8080 --addr 127.0.0.1 --interval 5s
```

Usually behind a reverse proxy (Caddy/nginx) for TLS.
