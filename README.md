# gnockpit

Real-time gno.land validator node monitoring web dashboard.

## Install

```bash
go install github.com/gnoverse/gnockpit@latest
```

## Usage

```bash
# Basic — connects to local gnoland RPC on default port
gnockpit

# Full options
gnockpit \
  -rpc http://127.0.0.1:26657 \
  -data-dir /path/to/gnoland-data \
  -genesis /path/to/genesis.json \
  -service gnoland.service \
  -port 8080 \
  -addr 0.0.0.0 \
  -interval 5s
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-rpc` | `http://127.0.0.1:26657` | RPC endpoint URL |
| `-data-dir` | auto-detect | gnoland data directory |
| `-genesis` | auto-detect | Path to genesis.json |
| `-service` | auto-detect | Systemd service name |
| `-names` | `/tmp/gnockpit-names.json` | Persistent name registry |
| `-port` | `8080` | Web server port |
| `-addr` | `0.0.0.0` | Bind address |
| `-interval` | `5s` | Dashboard refresh interval |
| `-v` | false | Verbose HTTP logging |
| `--db-path` | `/tmp/gnockpit.db` | SQLite database for push notifications (see Push Notifications) |
| `--chain-stuck-secs` | `30` | Seconds without a new block before the chain-stuck alert fires |

## Name Registry

gnockpit maps validator addresses to human-readable monikers for display in the dashboard. It builds this registry automatically from two sources:

- **Genesis file** — validator names are seeded at startup from `genesis.json`
- **Peer RPC queries** — when gnockpit queries a peer's `/status` endpoint, it records their address + moniker

Discoveries are persisted to the file specified by `-names` (default: `/tmp/gnockpit-names.json`) and survive restarts.

You can also pre-populate or edit the file manually to assign names to validators gnockpit hasn't discovered yet:

```json
{
  "g1a1b2c3d4e5f6...": "my-validator",
  "g1x9y8z7w6v5u4...": "peer-node-alpha"
}
```

## Push Notifications

gnockpit can send browser push notifications for validator monitoring alerts. The bell icon appears in the top-right of the dashboard — it is only shown on HTTPS or localhost (service workers require a secure context).

**HTTPS requirement:** Service workers require a secure context. Plain HTTP deployments will not show push options. If running behind a reverse proxy, ensure TLS is terminated at the proxy and gnockpit is accessed via `https://`.

**Flags:**

- `--db-path` (default `/tmp/gnockpit.db`) — SQLite database storing VAPID keys and push subscriptions. Set to a persistent path in production (e.g. `/var/lib/gnockpit/gnockpit.db`). **If VAPID keys are lost (database deleted or `/tmp` cleared on reboot), all existing browser subscriptions become invalid and users must re-subscribe.**
- `--chain-stuck-secs` (default `30`) — Seconds without a new block before the chain-stuck alert fires.

**Available alerts:**

- **Validator missing blocks** — fires when a validator misses more than N% of blocks in the recent signing window
- **Chain stuck** — fires when no new block is produced for N seconds
- **Node unreachable** — fires when gnockpit cannot reach a monitored node's RPC
- **Node out of sync** — fires when a node reports it is catching up

**Per-device settings:** Each browser/device has its own independent subscription. Changing notification settings on mobile does not affect desktop.

## Cluster Mode

Monitor multiple validator nodes from a single dashboard.

### Setup

```bash
# 1. On the hub server — create the database and tokens
gnockpit token create moul-val-01 --db-path /var/lib/gnockpit/hub.db
gnockpit token create gnocore-val --db-path /var/lib/gnockpit/hub.db

# 2. Start the hub server
gnockpit server --port 8080 --db-path /var/lib/gnockpit/hub.db

# 3. On each validator node — run a probe
gnockpit probe --server wss://gnockpit.example.com --token <token> --rpc http://127.0.0.1:26657
```

### Commands

| Command | Description |
|---------|-------------|
| `gnockpit server` | Start cluster hub server |
| `gnockpit probe` | Push snapshots to hub |
| `gnockpit token create <name>` | Generate probe token |
| `gnockpit token list` | List all tokens |
| `gnockpit token revoke <name>` | Revoke a token |
| `gnockpit doctor` | One-shot diagnostics |
| `gnockpit doctor --server <url> --token <t>` | Cluster health report |
| `gnockpit doctor --server <url> --token <t> --live` | Live monitoring |

### Cluster Dashboard

The hub serves a cluster-aware dashboard with:

- **Overview** — cards per probe with status, chain, height, peers
- **Health** — automated issue detection (height drift, staleness, version mismatch, etc.)
- **Compare** — side-by-side view of two probes

### Health Checks

- Height drift (>5 blocks behind)
- Probe staleness (>30s without data)
- Probe disconnected
- Consensus round mismatch
- Validator set divergence
- Chain ID mismatch
- Version mismatch

## Features

- Live WebSocket dashboard with real-time updates
- Consensus monitoring — height, round, step, prevotes, precommits per validator
- Peer management — validator detection, health status, RPC reachability
- Signing stats — per-validator sign rate and proposer speed over last 100 blocks
- Doctor diagnostics — deadlock detection, split-height alerts, gossip analysis
- Block history — recent blocks with signing visualization
- Cluster mode — multi-node monitoring with hub/probe architecture

## License

MIT
