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

# Consolidate several sources (e.g. seed nodes) — repeat -rpc; flag order sets precedence
gnockpit \
  -rpc http://seed-1:26657 \
  -rpc http://seed-2:26657 \
  -rpc http://seed-3:26657
```

Passing `-rpc` more than once consolidates several endpoints into one overview:
global chain state (height, validators, consensus, signing) is read from the
freshest reachable source each cycle, while peers are unioned across all sources
and deduplicated by node ID — so connecting to a few well-peered seed nodes
surfaces far more of the network than any single node sees. The configured
endpoints appear in the peer list flagged `(source)`. When conflicting values
appear for the same peer, the earlier `-rpc` in flag order wins.

## Flags

| Flag                    | Default                    | Description                                                                                                                                                          |
| ----------------------- | -------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `-rpc`                  | `http://127.0.0.1:26657`   | Tendermint RPC endpoint; repeatable to consolidate several sources (flag order = precedence)                                                                         |
| `-names`                | `/tmp/gnockpit-names.json` | Persistent name registry                                                                                                                                             |
| `-port`                 | `8080`                     | Web server port                                                                                                                                                      |
| `-addr`                 | `0.0.0.0`                  | Bind address                                                                                                                                                         |
| `-interval`             | `5s`                       | Dashboard refresh interval                                                                                                                                           |
| `-v`                    | false                      | Verbose HTTP logging                                                                                                                                                 |
| `--db-path`             | `/tmp/gnockpit.db`         | SQLite database for push notifications, missed-block history and block-ETA history (see Push Notifications, Block ETA)                                               |
| `--chain-stuck-secs`    | `30`                       | Seconds without a new block before the chain-stuck alert fires                                                                                                       |
| `--notify`              | (none)                     | Shoutrrr notification URL (repeatable); see External Notifications                                                                                                   |
| `--redact-secret`       | false                      | Strip notification URL secrets from logged Shoutrrr errors; see External Notifications                                                                               |
| `--max-missed-in-a-row` | `10`                       | Consecutive blocks a validator must miss to be flagged down (alert fires + counted inactive); it recovers after signing that many in a row                           |
| `--geoip-db`            | `/tmp/gnockpit-geoip.mmdb` | DB-IP City Lite mmdb powering the network map + country columns; auto-downloaded and refreshed monthly. Empty disables it.                                           |
| `--asn-db`              | `/tmp/gnockpit-asn.mmdb`   | DB-IP ASN Lite mmdb powering cloud-provider detection; auto-downloaded and refreshed monthly. Empty disables it.                                                     |
| `--hide-sources`        | false                      | Hide the configured source nodes from the peers list                                                                                                                 |
| `--chain-name`          | (chain-id)                 | Display name for the chain in titles and the app icon; defaults to the chain-id (e.g. `test13` for chain-id `test-13`)                                               |
| `--link`                | (none)                     | Extra header link button, `"Title\|URL"`; repeatable                                                                                                                 |
| `--status-link`         | (none)                     | Header link with a live status dot, `"Title\|URL"`; repeatable. BetterStack status pages only for now: the dot reflects `<URL>/index.json`                           |
| `--analytics`           | (off)                      | Load a third-party analytics script in the dashboard; only `simple-analytics` is supported. See Analytics.                                                           |
| `--analytics-domain`    | (none)                     | Serve the analytics script and its no-JS pixel from a custom domain (e.g. `sa.example.com`) instead of the provider's own hosts; needs `--analytics`. See Analytics. |

## Analytics

gnockpit ships with **no analytics and no third-party origins at all**. The
dashboard is a single self-contained HTML file: nothing about your node, your
peers or your visitors leaves the browser unless you turn it on.

`--analytics simple-analytics` injects the [Simple Analytics](https://www.simpleanalytics.com)
snippet at the end of `<body>`:

```bash
gnockpit --analytics simple-analytics
```

That loads `scripts.simpleanalyticscdn.com/latest.js` (async, so it never blocks
first paint) plus the `queue.simpleanalyticscdn.com/noscript.gif` fallback pixel
for visitors without JavaScript. Simple Analytics is cookie-free and collects no
personal data, so the dashboard still needs no consent banner. Pageviews land
under whatever hostname the browser is on, so add that hostname in the Simple
Analytics dashboard.

This is off by default on purpose: a gnockpit usually watches its operator's own
validator, and a private infrastructure dashboard should not report to a third
party that the operator did not choose. Only enable it on an instance you publish.

An unknown provider name is rejected at startup rather than silently ignored.

### A custom domain

Ad blockers list Simple Analytics' own hosts, so a visitor running one is never
counted. With a [custom domain](https://docs.simpleanalytics.com/bypass-ad-blockers)
set up in Simple Analytics, `--analytics-domain` serves the script and the pixel
from it instead:

```bash
gnockpit --analytics simple-analytics --analytics-domain sa.example.com
```

That loads `https://sa.example.com/latest.js` and
`https://sa.example.com/noscript.gif`. The value is a bare hostname: no scheme,
path or port. `--analytics-domain` without `--analytics` is rejected at startup,
since it would collect nothing.

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

- **Validator missing blocks** — fires when a validator misses N blocks in a row, and recovers once it signs N in a row (`--max-missed-in-a-row`)
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

A failed send is logged with Shoutrrr's error, which can hold the URL's secrets: a network failure quotes the service's request URL (Discord: `https://discord.com/api/webhooks/<id>/<token>`), and some services echo a malformed token. `--redact-secret` strips them before they are logged or returned. Each configured URL becomes `<scheme>:<fingerprint>`, as in `/api/notify/targets`, and every other URL keeps only its scheme. Every piece of 6+ characters of a configured URL becomes `[redacted]` wherever it appears. That also blanks the URL's hostnames in DNS errors, and any word that matches a piece, e.g. `telegram` in `invalid [redacted] token`. It is off by default, which keeps errors verbatim.

**Common Shoutrrr URL formats:**

| Service         | URL Format                                        |
| --------------- | ------------------------------------------------- |
| Discord         | `discord://token@webhookid`                       |
| Telegram        | `telegram://token@telegram?chats=@channel,chatid` |
| Slack           | `slack://token-a/token-b/token-c`                 |
| Email           | `smtp://user:pass@host:port/?from=X&to=Y`         |
| Generic webhook | `generic+https://example.com/webhook`             |

See the [Shoutrrr documentation](https://containrrr.dev/shoutrrr/latest/) for the full list of supported services and URL formats.

## Network Map & Cloud Providers

gnockpit geolocates each connected peer's IP and surfaces it on an
equirectangular world map, a per-country peer list, and Country/Provider columns
in the peer and validator tables. Two [DB-IP Lite](https://db-ip.com) databases
power this, auto-downloaded and refreshed monthly (CC-BY-4.0):

- **City Lite** (`--geoip-db`) → coordinates + country.
- **ASN Lite** (`--asn-db`) → autonomous system → cloud provider (AWS, GCP,
  Hetzner, OVH, …), falling back to the raw AS-org name for unknown hosts.

Only public IPs are geolocated; peers that expose no usable public address are
grouped under an "Unknown" entry in the country and provider lists. Country and
provider are known only for nodes one of the sources is connected to; validators
are matched to a connected peer, so validators no source is peered with show
none — connecting more sources widens coverage. Set either flag to empty to
disable that lookup.

A configured source reached over `localhost` or a private address has no public
IP of its own; gnockpit then uses its own public egress IP for that source's map
location, which is accurate when gnockpit runs alongside the node.

## Missed-block history

Per-block validator signing is recorded forward into the SQLite database
(`--db-path`) as blocks are observed — never backfilled — enabling missed-block
counts over 1h / 24h / 7d / 30d / total windows (surfaced in the validators
table and `/api/stats`). History older than 31 days is pruned. Windows only
cover data recorded since gnockpit started; `/api/stats` exposes `recorded_since`
so consumers know the coverage.

## Block ETA

gnockpit estimates when a block height will be produced, and which height the
chain will reach at a given time, with a margin and the drift from the first
estimate. It is built for coordinated upgrades: picking a halt height for a
target time, then counting down to it.

- **Dashboard** — **Block ETA** in the header opens a planner (height → time or
  time → height). **Show countdown** puts the target in the page link
  (`/?eta=<height>&since=<RFC 3339 time>&label=<text>`), and anyone opening the
  link sees the same card: countdown, ETA in UTC and local time, 80% and 95%
  ranges, drift from the original estimate, the ETA over time, and the ETA at the
  pace of the last hour, 24 h, 7 days, 30 days and the whole history. Its compact
  view keeps one line and the ranges as a bar. Nothing is stored server-side; ✕
  removes the target from the link.
- **Scheduled halt** — gnockpit reads the halt governance sets on chain
  (`node:p:halt_height` and `node:p:halt_min_version`; a params query, answered
  even with `rpc.allow_code_execution = false`). While a halt is ahead of the
  chain, a **Next halt** card shows it to every visitor, with the estimate from
  when gnockpit first saw the halt as the original. A link targeting the halt
  height shows the halt card instead of its own.
- **Estimate** — the ETA extrapolates the pace of the last 3,000 blocks. The
  ranges are where the actual time fell for 80% and 95% of the predictions this
  method made over the last 30 days at the same distance ahead: they widen with
  the distance, and lean late when the chain tends to slow down. An estimate past
  a scheduled halt does not include the upgrade downtime, and says so.
- **History** — the time of every 100th block is backfilled into `--db-path`
  from the first `-rpc`, newest first, at about four requests a second, pausing on
  HTTP 429 for as long as `Retry-After` asks (a minute without one); a chain of
  1.6M blocks takes a bit over an hour. The backfill is complete once it reaches
  the first block, or the oldest one the node serves. The ranges appear once
  enough history is in. Pointing gnockpit at another chain drops the stored
  history.

## HTTP API

All endpoints are CORS-open JSON, for building external badges/dashboards.

| Endpoint      | Description                                                                                                                                                                                                                                                                                                                                                                                    |
| ------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `/api/status` | Health summary (`status`/`chain`/`height`/`reason`/`time`) plus live network state, the last 100 blocks with signing status, and per-peer / per-validator column data, including `name`, `country`, `city`, `lat`/`lon` and cloud `provider`. A validator's location and provider are its matched peer's. Unknown fields are omitted; when only one of `lat`/`lon` is present, the other is 0. |
| `/api/stats`  | Aggregate stats: per-validator missed-block windows, cloud-provider and country aggregates, validator-set health, and the Nakamoto coefficient.                                                                                                                                                                                                                                                |
| `/api/eta`    | Block ETAs: when a height is expected, which height a time reaches, or the scheduled halt (see below).                                                                                                                                                                                                                                                                                         |
| `/api`        | Raw internal snapshot dump (unstable shape; for debugging).                                                                                                                                                                                                                                                                                                                                    |
| `/badge.svg`  | SVG status badge.                                                                                                                                                                                                                                                                                                                                                                              |

### `/api/eta`

The query picks the question; times are RFC 3339 (write `Z`, or encode a `+`
offset as `%2B`) and answers are UTC to the second.

| Query               | Answer                                                                                                                                                                 |
| ------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `?height=H`         | When block H is expected: `target` (`height`, `blocks_left`), `eta`, `seconds_left`, `range_80` and `range_95`, `pace` and `scenarios`.                                |
| `?height=H&since=T` | The same, plus `original` (the estimate as it stood at T) and `trail` (the estimate over time since T).                                                                |
| `?time=T`           | Which height the chain reaches at T, a time after the newest block: `estimate` (`at`, `height`, `range_80`, `range_95`), `pace`, and `scenarios` with a `height` each. |
| none                | The halt scheduled on chain, answered as `?height=<halt>&since=<first seen>`; `"halt": null` and the current `pace` when none is.                                      |

| Field                  | Shape                                                                                                                                                                       |
| ---------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `range_80`, `range_95` | `from`/`to`: times in height mode, heights in `estimate`. The actual outcome fell in it for 80% / 95% of past predictions at the same distance.                             |
| `pace`                 | `seconds_per_block`, `over_blocks` (about the last 3,000)                                                                                                                   |
| `scenarios[]`          | `pace_of` (`last_hour`, `last_24h`, `last_7d`, `last_30d`, `all_history`; only spans the history covers), `seconds_per_block`, and `eta` or `height`                        |
| `original`             | `as_of` and `height` of the last sampled block (every 100th) at or before T, the `eta` estimated from it, and `drift_seconds`: how much later (positive) the current ETA is |
| `trail[]`              | up to 200 of `at`, `height`, `eta`, `range_80`: the estimate from each sampled block since T                                                                                |
| `halt`                 | `height`, `min_version`, `first_seen`, or `null`                                                                                                                            |

Once H is produced, `eta` is the block's time, also in `reached_at`, `seconds_left`
is 0, and `range_80`, `range_95`, `pace` and `scenarios` are left out; `original`
then measures the drift of the actual time. Every successful answer also carries
`chain`, `now` (`height`, `block_time`) and `history` (`from`, `samples`,
`complete`: the backfill reached the first block the node serves); `warning`
flags a scheduled halt before the target. The ranges are left out while the
history is too short, and `original` and `trail` when T predates it. Errors come
as `{"error": "…"}`: 400 for a malformed query, a height more than 100 years
ahead at the current pace, a time not after the newest block, or a `since` in
the future; 503 until gnockpit has a chain status and enough history for a
pace, or when the first `-rpc` does not serve a produced block (it is behind, or
does not keep blocks that old); 502 when that RPC cannot give a produced block's
time.

```bash
curl -s 'http://localhost:8080/api/eta?height=1651200&since=2026-11-16T13:47:26Z'
curl -s 'http://localhost:8080/api/eta?time=<RFC 3339 time after the newest block>'
```

## Features

- Live WebSocket dashboard with real-time updates
- Multi-source consolidation — query several RPC endpoints; peers unioned & deduped, freshest source drives chain state
- Consensus monitoring — height, round, step, prevotes, precommits per validator
- Peer management — validator detection, health status, RPC reachability
- Network map — IP-geolocated peers, per-country list, Country + cloud-Provider columns
- Signing stats — per-validator blocks missed in the last 100 + proposer speed, and missed-block counts over 1h/24h/7d/30d/total
- Block ETA — when a height will be produced (or which height at a given time), with 80%/95% ranges and drift from the original estimate; countdown cards for shared links and for the halt scheduled on chain
- HTTP API — CORS-open `/api/status`, `/api/stats` and `/api/eta` for external badges, dashboards and scripts
- Alerts — browser push (PWA) and external notifications (Discord, Telegram, …) for chain-stuck and missed-blocks
- Block history — recent blocks with signing visualization

## License

MIT
