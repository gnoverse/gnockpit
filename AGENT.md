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
  batch.go       — JSON-RPC batches (one POST, several calls); HeightError
  blockhistory.go — commits (one, or a batch), validator sets with priorities, block events: the block history's and the live window's reads
blocks/          — block history since genesis: per-height facts in SQLite, filled from the primary source (shares the push DB handle)
  store.go       — blocks, validator sets, absences (absent / nil / other block), round proposers, flags, set events + event gaps, daily vote-delay histograms; each address once (blocks_validators), the rest by id; missed-block windows
  record.go      — a commit + its validator set → a Record: absence kinds, vote delays from tm2's weighted median time
  proposer.go    — port of tm2's proposer-priority increment: the expected proposer of each round
  tracker.go     — fill loop (Data Flow #7)
eta/             — block ETAs: sampled block times + estimator + halt tracking (shares the push DB handle)
  store.go       — eta_block_times (every 100th height), chain id, backfill floor, first sighting of each halt
  model.go       — pure estimator: pace of the last 3000 blocks; 80/95% ranges from the predictor's past errors (30-day lookback)
  tracker.go     — background loop on the primary source: backfill newest-first (~4 req/s, honours 429 Retry-After), halt params
web/             — Web dashboard + HTTP API
  server.go      — HTTP/WebSocket server, background data fetcher, system info collector
  status.go      — /api/status: health + curated network state / recent blocks / peer + validator columns
  stats.go       — /api/stats: missed-block windows, provider/country aggregates, set health, Nakamoto
  eta.go         — /api/eta: ETA of a height, height at a time, or the scheduled halt (model from eta.Tracker)
  history.go     — /api/history: the block history's reach, the blocks of a height range (≤ 1,000), daily vote delays (≤ 31 days)
  analytics.go   — Optional third-party analytics snippet (--analytics), injected before </body>; off by default
  index.html     — Single-page dashboard (embedded via go:embed, vanilla JS, no framework)
```

## Key Concepts

### Data Flow

1. `server.go` runs a **publish loop** every N seconds (default 5s)
2. Each cycle calls `fetchSnapshot()` which polls every configured source (`node/sources.go: Poll`): global chain data (status, validators, consensus, signing) comes from the freshest reachable source; peers are unioned across all reachable sources and deduped by node ID (`MergePeers`) before their RPCs are probed. All sources down → `snap.Error` drives the "connecting to sources" banner.
3. The snapshot is broadcast to all connected WebSocket clients as typed messages: `status`, `peers`, `votes`, `checks`, `signing`
4. `index.html` receives these messages and updates the DOM in-place
5. Each cycle also geolocates peers (`geoip`) and resolves their cloud provider (`asn`) — using each peer's resolved public IP only (peers with no usable public IP are geolocated to nothing and bucketed "Unknown" on the map) — and refreshes the missed-24h counts from the `blocks` store (#7). This powers the network map and the Country/Provider columns. Validators inherit the country/provider of their correlated peer (matched via `ValAddress`), so validators no source is peered with have none.
6. Separately, `eta.Tracker` (started in `main.go`) runs its own loop on the primary source: every minute, and back to back while backfilling, it reads the chain head, drops the stored history if the chain id changed, reads the halt params, and fetches the missing every-100th block times newest-first. `/api/eta` combines its model with the snapshot's newest block; the dashboard's ETA cards poll `/api/eta` every 10 s and tick the countdown locally in between. The link card's target lives only in the page link (`?eta=&since=&label=`); the halt card's comes from the chain params; the "original" estimate is recomputed from the stored history as of `since`.
7. Separately, `blocks.Tracker` (started in `main.go`) fills the block history from the primary source. Deltas worth knowing:
    - Each pass stores the heights up to head − 1 (the head's commit is final only once the next block carries it), then one batch below the stored range, newest first; `--history-pace` between passes while backfilling, 5 s once complete.
    - A non-canonical commit, or a refusal within 1,000 blocks of the head (a lagging load-balanced backend), ends that direction quietly until the next pass; a refusal further down sets the floor, re-checked once per start.
    - A batch refused whole halves `--history-batch` for good; one timing out, for 10 minutes. HTTP 429 waits `Retry-After` on every read.
    - One `/validators` per new `validators_hash`; the sets at h−1 and h for a block finalized after round 0 and every 1,000th block (expected round proposers, from the port of tm2's proposer priorities).
    - Set-change events from `/block_results` at H−2; a failed read becomes an event gap, read again every 10 minutes.
    - `Progress.Rate`: heights/s over the last 5 min, for `/api/history` and Network State's header (share filled, time left).

### NameRegistry (validators.go)

Maps validator addresses to human-readable monikers. This is critical because Tendermint RPC only returns addresses, not names. Discovery happens through:

- **Genesis (RPC)** — validator names and the genesis time are seeded at startup by streaming the node's `/genesis` endpoint, parsing only the head (the large `app_state` is never downloaded)
- **Peer RPC queries** — when we query a peer's `/status`, we get their validator_info.address + moniker
- **Node-ID verification** — on shared IPs (multiple nodes same IP), the RPC response's node_info.id is checked against the peer's P2P node-id before trusting the mapping
- **Correlation** — unmatched validators are matched to unmatched peers heuristically

The registry persists to a JSON file (`--names` flag) so discoveries survive restarts.

### Signing Stats (client.go: GetSigningStats)

Fetches the last N blocks (default 100), extracts:

- Who signed each block (from `/commit`'s precommits, which finalize that height; one for the block or for nil signs it, an empty slot or one for another block misses it, as in the block history)
- Who proposed each block (from `header.proposer_address`)
- Block timestamps → compute per-proposer average block time
- Sign rate per validator (signed/total as percentage)

This is a live 100-block window; the commits the node marks `canonical` (final)
are kept in memory, so a refresh fetches one or two (the new head, and the one
before it, now final). Longer-term **missed-block counts** come from the
`blocks` store (Data Flow #7) via `MissedByWindows`.

### HTTP API (CORS-open JSON)

- `/api/status` (status.go) — health summary (retrocompat `status`/`chain`/`height`/`reason`/`time`) plus `network`, `recent_blocks` (last 100), and per-`peers`/`validators` column data.
- `/api/stats` (stats.go) — per-validator missed-block windows, provider + country aggregates, validator-set health, Nakamoto coefficient.
- `/api/eta` (eta.go) — `?height=` (+`since` for original/drift/trail), `?time=`, or none for the on-chain halt.
- `/api/history` (history.go) — the block history's reach (`filled`); `?from=&to=` (≤ 1,000 heights): blocks with absent / nil / other-block members, round proposers, flags, set changes, and the validator sets; `?days=N` (≤ 31): daily vote-delay histograms.
- `/api` (server.go) — raw `Snapshot` dump (unstable shape; debugging).
- Also: `/badge.svg`, `/ws`, `/api/boot`, `/api/push/*`, `/api/notify/*`.

## Important Patterns

### No hardcoded values

Everything is auto-detected or configurable via flags. The tool works on any gno.land chain without code changes:

- Chain ID → from RPC `/status`
- Genesis time + validator names → streamed from RPC `/genesis` (head only)
- Validator names → also discovered dynamically from peers

### RPC errors

`rpcGet` returns a `*node.HTTPStatusError` (with `RetryAfter`) for a non-2xx answer; `rpcGetJSON` wraps a `*node.RPCError` for a JSON-RPC `error` envelope, which gno sends with HTTP 200 (e.g. a height out of range). `node.IsRefusal` tells a node refusing a call (JSON-RPC error) from one failing to answer (transport error, HTTP error status). Decode RPC results through `rpcGetJSON` so neither passes silently.

### Single HTML file

`web/index.html` is a complete SPA with no build step. Vanilla JS, CSS variables for dark theme, no external dependencies. It's embedded in the binary via `go:embed`.

The **only** thing that may add an external origin to that page is `--analytics`
(`web/analytics.go`), which splices a provider snippet in just above `</body>` as the page is
served. It is off by default and must stay that way: most gnockpits watch their operator's own
validator, so the default dashboard has to report to nobody. `--analytics-domain` moves that
provider's script and pixel to a custom domain and adds no origin of its own. Keep the page
self-contained otherwise.

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

Go tests cover the RPC parsing, the stores, the trackers (fake sources) and the HTTP handlers. The web UI is tested manually.

## Deployment

Typical systemd service:

```ini
[Service]
ExecStart=/usr/local/bin/gnockpit \
  --rpc http://127.0.0.1:26657 \
  --port 8080 --addr 127.0.0.1 --interval 5s
```

Usually behind a reverse proxy (Caddy/nginx) for TLS.
