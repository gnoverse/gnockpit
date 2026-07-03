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
  -port 8080 \
  -addr 0.0.0.0 \
  -interval 5s
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-rpc` | `http://127.0.0.1:26657` | RPC endpoint URL |
| `-names` | `/tmp/gnockpit-names.json` | Persistent name registry |
| `-port` | `8080` | Web server port |
| `-addr` | `0.0.0.0` | Bind address |
| `-interval` | `5s` | Dashboard refresh interval |
| `-v` | false | Verbose HTTP logging |
| `--db-path` | `/tmp/gnockpit.db` | SQLite database for push notifications (see Push Notifications) |
| `--chain-stuck-secs` | `30` | Seconds without a new block before the chain-stuck alert fires |
| `--notify` | (none) | Shoutrrr notification URL (repeatable); see External Notifications |
| `--missed-blocks-pct` | `5` | Percentage of missed blocks before the validator alert fires |
| `--geoip-db` | `/tmp/gnockpit-geoip.mmdb` | DB-IP City Lite mmdb powering the network map + country columns; auto-downloaded and refreshed monthly. Empty disables it. |
| `--asn-db` | `/tmp/gnockpit-asn.mmdb` | DB-IP ASN Lite mmdb powering cloud-provider detection; auto-downloaded and refreshed monthly. Empty disables it. |

## Name Registry

gnockpit maps validator addresses to human-readable monikers for display in the dashboard. It builds this registry automatically from two sources:

- **Genesis (RPC)** — validator names and the genesis time are seeded at startup by streaming the node's `/genesis` endpoint, reading only the head of the document (the large `app_state` is never downloaded)
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

## External Notifications

gnockpit can forward alerts to Discord, Telegram, Slack, and other services via [Shoutrrr](https://github.com/containrrr/shoutrrr) notification URLs. Use the `--notify` flag (repeatable) to add destinations:

```bash
gnockpit \
  --notify "discord://token@webhookid" \
  --notify "telegram://token@telegram?chats=@channel"
```

Alerts are sent to all configured URLs whenever a state transition occurs (firing or recovery). The same alerts that trigger browser push notifications also trigger external notifications.

**Common Shoutrrr URL formats:**

| Service | URL Format |
|---------|------------|
| Discord | `discord://token@webhookid` |
| Telegram | `telegram://token@telegram?chats=@channel,chatid` |
| Slack | `slack://token-a/token-b/token-c` |
| Email | `smtp://user:pass@host:port/?from=X&to=Y` |
| Generic webhook | `generic+https://example.com/webhook` |

See the [Shoutrrr documentation](https://containrrr.dev/shoutrrr/latest/) for the full list of supported services and URL formats.

## Network Map & Cloud Providers

gnockpit geolocates each connected peer's IP and surfaces it on an
equirectangular world map, a per-country peer list, and Country/Provider columns
in the peer and validator tables. Two [DB-IP Lite](https://db-ip.com) databases
power this, auto-downloaded and refreshed monthly (CC-BY-4.0):

- **City Lite** (`--geoip-db`) → coordinates + country.
- **ASN Lite** (`--asn-db`) → autonomous system → cloud provider (AWS, GCP,
  Hetzner, OVH, …), falling back to the raw AS-org name for unknown hosts.

Country/provider is known only for nodes gnockpit is directly connected to;
validators are matched to a connected peer, so validators the node isn't peered
with show no country/provider. Set either flag to empty to disable that lookup.

## Missed-block history

Per-block validator signing is recorded forward into the SQLite database
(`--db-path`) as blocks are observed — never backfilled — enabling missed-block
counts over 1h / 24h / 7d / 30d / total windows (surfaced in the validators
table and `/api/stats`). History older than 31 days is pruned. Windows only
cover data recorded since gnockpit started; `/api/stats` exposes `recorded_since`
so consumers know the coverage.

## HTTP API

All endpoints are CORS-open JSON, for building external badges/dashboards.

| Endpoint | Description |
|----------|-------------|
| `/api/status` | Health summary (`status`/`chain`/`height`/`reason`/`time`) plus live network state, the last 100 blocks with signing status, and per-peer / per-validator column data. |
| `/api/stats` | Aggregate stats: per-validator missed-block windows, cloud-provider and country aggregates, validator-set health, and the Nakamoto coefficient. |
| `/api` | Raw internal snapshot dump (unstable shape; for debugging). |
| `/badge.svg` | SVG status badge. |

## Features

- Live WebSocket dashboard with real-time updates
- Consensus monitoring — height, round, step, prevotes, precommits per validator
- Peer management — validator detection, health status, RPC reachability
- Network map — IP-geolocated peers, per-country list, Country + cloud-Provider columns
- Signing stats — per-validator sign rate + proposer speed (last 100 blocks) and missed-block counts over 1h/24h/7d/30d/total
- HTTP API — CORS-open `/api/status` and `/api/stats` for external badges/dashboards
- Alerts — browser push (PWA) and external notifications (Discord, Telegram, …) for chain-stuck and missed-blocks
- Block history — recent blocks with signing visualization

## License

MIT
