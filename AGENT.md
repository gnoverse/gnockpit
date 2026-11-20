# Agent Guide — gnockpit

## What is this?

gnockpit is a real-time monitoring dashboard for gno.land validator nodes. It's a single Go binary (`gnockpit [flags]`, no subcommands) that connects to one or more gnoland RPC endpoints (`-rpc`, repeatable), consolidates their views, and serves a live web dashboard, with web-push/PWA alerts and external (Shoutrrr) notifications.

## Architecture

```text
main.go          — CLI entry point (single command: starts the web dashboard)
node/            — RPC client + types (pure data layer, no UI)
  client.go      — HTTP client for Tendermint RPC (/status, /net_info, /validators, etc.)
  sources.go     — Sources: an ordered set of RPC endpoints polled together; best-source pick for global data + peer union/dedup (MergePeers)
  ip.go          — public-IP validation (IsPublicIP): only public IPs are ever geolocated or displayed
  types.go       — snapshot and dashboard types (Status, Peer, Validator, Snapshot, SigningStats, etc.)
  validators.go  — NameRegistry: maps validator addresses ↔ monikers, persists to JSON
  geoip.go       — DB-IP City Lite: IP → coordinates + country (auto-downloaded, monthly)
  asn.go         — DB-IP ASN Lite: IP → ASN + cloud provider (auto-downloaded, monthly)
  abci.go        — ABCI queries: realm renders (valoper names, profiles), valoper state via vm/qeval, the halt params
  rpcerror.go    — HTTPStatusError, RPCError, IsRefusal
  signing.go     — per-validator signing over the live window (computeSigning): missed, its nil / other-block split, vote delay
  median.go      — tm2's voting-power-weighted median time (MedianTime), shared with blocks/record.go
  batch.go       — JSON-RPC batches (one POST, several calls); HeightError
  chain.go       — Commit/Precommit, SetMember, BlockEvent and their reads: commits (one, or a batch), validator sets with priorities, block events
blocks/          — block history since genesis: per-height facts in SQLite, filled from the primary source (shares the push DB handle)
  store.go       — schema, block rows (absences, round proposers, flags), the counters Write keeps beside them (days, gaps, absence runs, vote delays), meta (chain id, floor, one-time fills); each address once (blocks_validators), the rest by id (blocks_events keeps text addresses), no index beside the keys
  days.go        — blocks_days: per UTC day and validator, absences by kind, proposals expected / missed
  proposals.go   — a block's proposals expected and missed (proposalCounts); round-0 blocks by aggregate in the one-time fill
  gaps.go        — blocks_gaps: restarts (RestartGap, 2 min, between blocks b and b+1)
  runs.go        — blocks_absence_runs: each validator's runs of consecutive absences (absent or other block)
  history.go     — ValidatorHistory: a validator's days, outages, recoveries and scoring-window summary; the scoring constants
  keys.go        — an operator's signing keys over time (OperatorKeys), a key's operator (OperatorOf), Known
  valsets.go     — validator sets by hash; address → id
  windows.go     — missed-block windows and proposals expected / missed: first block by bisecting heights, the first (partial) day's absences and late blocks + the per-day counters, one read transaction
  delays.go      — daily vote-delay histograms (bucket edges)
  changes.go     — validator set changes: their heights (blocks_valset_changes, kept by Write; filled by one scan while `valset_changes_filled` is unset), each read with its diff against the set below (added, removed, re-weighted, rotations from events)
  events.go      — set-change events: stored, read from /block_results at H−2, event gaps
  record.go      — a commit + its validator set → a Record: absence kinds, vote delays from tm2's weighted median time
  proposer.go    — port of tm2's proposer-priority increment: the expected proposer of each round
  tracker.go     — fill loop (Data Flow #7)
  reads.go       — the fill's rate-limited reads, record building, round proposers + flags
  progress.go    — Progress, fill rate
eta/             — block ETAs: sampled block times + estimator + halt tracking (shares the push DB handle)
  store.go       — eta_block_times (every 100th height), chain id, backfill floor, first sighting of each halt
  model.go       — pure estimator: pace of the last 3000 blocks; 80/95% ranges from the predictor's past errors (30-day lookback)
  tracker.go     — background loop on the primary source: backfill newest-first (~4 req/s, honours 429 Retry-After), halt params
web/             — Web dashboard + HTTP API
  server.go      — HTTP/WebSocket server, background data fetcher, system info collector
  status.go      — /api/status: health + curated network state / recent blocks / peer + validator columns
  stats.go       — /api/stats: missed-block windows, provider/country aggregates, set health, Nakamoto
  eta.go         — /api/eta: ETA of a height, height at a time, or the scheduled halt (model from eta.Tracker)
  history.go     — /api/history: the block history's reach, the blocks of a height range (≤ 1,000), daily vote delays (≤ 31 days), set changes (≤ 100 a page)
  validator.go   — /api/validator: one validator's page data (Validator history below)
  api.go         — serveJSON, errorBody: the JSON API's response writing
  reminder.go    — /api/push/reminder; schedule(): the head, the scheduled halt and time-left estimates the lead-time alerts check
  push/          — browser push and Shoutrrr: alert detection, devices (one per push endpoint), the notification config file (notifyconfig.go: parse; notifyroute.go: per-target senders, reload on change), chain alerts and their sent state (schedule.go), ETA reminders (reminders.go)
  analytics.go   — Optional third-party analytics snippet (--analytics), injected before </body>; off by default
  index.html     — Single-page dashboard (embedded via go:embed, vanilla JS, no framework)
```

## Key Concepts

### Data Flow

1. `server.go` runs a **publish loop** every N seconds (default 5s)
2. Each cycle calls `fetchSnapshot()` which polls every configured source (`node/sources.go: Poll`): global chain data (status, validators, consensus, signing) comes from the freshest reachable source; peers are unioned across all reachable sources and deduped by node ID (`MergePeers`) before their RPCs are probed. All sources down → `snap.Error` drives the "connecting to sources" banner.
   The publish loop also rereads the notification config file when it changed, and, once the snapshot is set, sends the lead-time alerts (`notifySchedule` → `push.Manager.EvaluateSchedule`: a new halt and its approach, once per device or target, recorded in `push_sent`; the reminders whose lead is met, then dropped, their device kept). Every push or webhook send is bounded by `--send-timeout`.
3. The snapshot is broadcast to all connected WebSocket clients as typed messages: `status`, `peers`, `votes`, `checks`, `signing`
4. `index.html` receives these messages and updates the DOM in-place
5. Each cycle also geolocates peers (`geoip`) and resolves their cloud provider (`asn`) — using each peer's resolved public IP only (peers with no usable public IP are geolocated to nothing and bucketed "Unknown" on the map) — and refreshes the missed-24h counts and the 24 h proposals expected / missed from the `blocks` store (#7, `refreshMissed`). This powers the network map and the Country/Provider columns. Validators inherit the country/provider of their correlated peer (matched via `ValAddress`), so validators no source is peered with have none.
6. Separately, `eta.Tracker` (started in `main.go`) runs its own loop on the primary source: every minute, and back to back while backfilling, it reads the chain head, drops the stored history if the chain id changed, reads the halt params, and fetches the missing every-100th block times newest-first. `/api/eta` combines its model with the snapshot's newest block; the dashboard's ETA cards poll `/api/eta` every 10 s and tick the countdown locally in between. The link card's target lives in the page link (`?eta=&since=&label=`), or in the device's reminder (`push_reminders`) for a page opened without one; the halt card's comes from the chain params; the "original" estimate is recomputed from the stored history as of `since`.
7. Separately, `blocks.Tracker` (started in `main.go`) fills the block history from the primary source. Deltas worth knowing:
    - Each pass: heights up to head − 1 (the head's commit is final only once the next block carries it), then one batch below the stored range; `--history-pace` between batches while backfilling or catching up, 5 s once complete. A non-canonical commit, or a commit refused within 1,000 blocks of the head (a lagging backend), ends that direction quietly; logged after 2 minutes. A short batch timing out ends the pass.
    - Floor: set when two answers serve a height and refuse the one below, over 1,000 below the head (a downward batch of 2+ calls opens with the lowest stored height as that witness). A refusal first in its answer (the witness, or a batch of 1) sets no floor; twice in a row at one height, over 1,000 below the head, it pauses the backfill for 10 minutes, logged once; a served downward answer resets it. So a batch of 1 (flag, or a source taking 1 call a batch) never completes at a floor: it pauses there. `floor−1` is rechecked after the head at each start until an answer settles it (served clears the floor), never failing a pass.
    - A batch refused whole halves `--history-batch`, rounding up (2 comes before 1), until restart; one timing out, for 10 minutes. HTTP 429 waits `Retry-After` on every read.
    - Set changes: `Write` records a height whose set differs from the stored height below (settling the written height and the one above, so a batch edge is caught either way). After the upward loop, the tracker tells (`OnValsetChange`, wired to `push.Manager.NotifyValsetChange` in main.go) the changes above the `valset_told_to` watermark, which starts at the first stored high: the backfill and a new chain's first fill never tell.
    - `/validators` once per new `validators_hash`, plus h−1 and h for a block with round > 0 (rounds kept) and every 1,000th (check only); a set below refused twice, over 1,000 below the head, flags the block; a first refusal, or one nearer, fails the pass. Set-change events from `/block_results` at H−2; a failed read becomes an event gap, retried every 10 minutes, dropped when its events are below the floor.

### Validator history (blocks/history.go, web/validator.go)

- **Counters**, kept by `Write` with each record: `blocks_days` (day, validator → absent, nil_votes, other_block, proposals_expected, proposals_missed), `blocks_gaps` (b where block b+1's time is over `RestartGap` after b's) and `blocks_absence_runs` (validator, first, last). Each is filled once for blocks stored before it, while its meta key (`days_filled`, `proposals_filled`, `gaps_filled`, `runs_filled`) is unset; `Reset` sets them all.
- **Blocks in the set** are derived, never stored: `blocks_vote_delays.votes` (one per precommit present, nil and other block included) + absent.
- **Proposals**: k = the earliest round ≤ the commit round whose expected proposer is the header's; rounds 0..k expected, 0..k−1 missed. Unknown rounds or no match → the height counts nothing.
- **Recovery** after a restart at b, for the key in the set at b−1 and b: R = the run holding b, else the one ending at b−1; signed at h = R.Last+1 when R holds b, else b; a sample when fewer than 10 of R's heights are ≤ b−1, R does not start at the stored low (above 1), and the key is in the set at h (else none); pending while block h+1 is not stored; duration = time(h+1) − time(b+1). The last 3 samples of the 180-day window are `Scored`.
- **Outages**: runs of 10+ absences (absent or other block; a key's run and the next key's starting right after it, a rotation, are one), recovery runs left out up to 24 h of block time after block b+1 (past that, the rest when it holds 10+).
- **Scoring constants** live in code, never flags: `outageMin`, `recoveryRunCap`, `scoringDays` (30 complete UTC days before today), `recoveryDays` (180), `recoveryLast` (3) in history.go; `RestartGap` in gaps.go.
- **Identity**: `OperatorKeys` reads an operator's spans from set events (Added at H opens a key from H, or from 0 when the stored set at H−1 holds it: a power change, which keeps an open span; an applied rotation at H closes the old key at H−1 and opens the new one at H; Removed closes at H−1), plus the profile's key when no event names it. `/api/validator` resolves `?val=` to the operator it is, else `OperatorOf` (an event naming it), else a profile naming it as signing key, else the key alone (operator "") when `Known` or in the current set; else 404.
- **Valopers**: `valoperRefreshLoop` (every 15 min) → `refreshValopers` reads every profile's render and two `vm/qeval` reads (`KeepRunning`, `LastRotationHeight`) into `Server.valopers`, keyed by the operator the registry lists. A failed state read leaves `State` nil; a failed or unparsable render keeps the operator's last profile; each logged once a refresh.

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
- Block timestamps → the chain-wide average block time
- Per validator: missed (absent, or a precommit for another block), nil votes (signed), and the median delay of its precommits from each commit's median time (below), within the blocks it was in the set for

This is a live 100-block window; the commits the node marks `canonical` (final)
are kept in memory, so a refresh fetches only the heights not yet read final:
one or two when the window slid by one block. A precommit's delay is from block h+1's header time (tm2's weighted median of commit h), the newest commit's from `MedianTime`. Longer-term **missed-block counts** come from the
`blocks` store (Data Flow #7) via `MissedByWindows`.

### HTTP API (CORS-open JSON)

- `/api/status` (status.go) — health summary (retrocompat `status`/`chain`/`height`/`reason`/`time`) plus `network`, `recent_blocks` (last 100), and per-`peers`/`validators` column data.
- `/api/stats` (stats.go) — per-validator missed-block windows, provider + country aggregates, validator-set health, Nakamoto coefficient.
- `/api/eta` (eta.go) — `?height=` (+`since` for original/drift/trail), `?time=`, or none for the on-chain halt.
- `/api/history` (history.go) — the block history's reach (`filled`); `?from=&to=` (≤ 1,000 heights): blocks with absent / nil / other-block members, round proposers, flags, set changes, and the validator sets; `?days=N` (≤ 31): daily vote-delay histograms; `?changes=N&before=H` (≤ 100): set changes newest first with their diff, paged by `next_before`.
- `/api/validator` (validator.go, validator_timeline.go) — `?val=<g1 operator or signing key>`: status, keys, moniker, location, profile, timeline (with `recovering` for a pending recovery), vote-delay edges, days, recoveries (`scored`), summary, live; 400 malformed, 404 unknown, 503 history off. Not paged.
- `/api` (server.go) — raw `Snapshot` dump (unstable shape; debugging).
- Also: `/badge.svg`, `/ws`, `/api/boot`, `/api/push/*` (subscribe: one device per endpoint, `""` id once it has nothing left; `/api/push/reminder` in reminder.go), `/api/notify/*` (the config's targets and their alerts, a test send; `GNOCKPIT_NOTIFY_TEST_TOKEN`).

## Important Patterns

### No hardcoded values

Everything is auto-detected or configurable via flags. The tool works on any gno.land chain without code changes:

- Chain ID → from RPC `/status`
- Genesis time + validator names → streamed from RPC `/genesis` (head only)
- Validator names → also discovered dynamically from peers

### RPC errors

`rpcGet` returns a `*node.HTTPStatusError` (with `RetryAfter`) for a non-2xx answer; `rpcGetJSON` wraps a `*node.RPCError` for a JSON-RPC `error` envelope, which gno sends with HTTP 200 (e.g. a height out of range). `node.IsRefusal` tells a node refusing a call (JSON-RPC error) from one failing to answer (transport error, HTTP error status). Decode RPC results through `rpcGetJSON` (one call) or `rpcBatch` (a batch: a refusal of the whole batch is a wrapped `*RPCError`, a call's own error is in its answer) so neither passes silently.

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
2. Add types to `node/types.go` (or beside their read, as in `chain.go`)
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
