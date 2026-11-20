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

| Flag                    | Default                    | Description                                                                                                                                                                                                                                           |
| ----------------------- | -------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `-rpc`                  | `http://127.0.0.1:26657`   | Tendermint RPC endpoint; repeatable to consolidate several sources (flag order = precedence)                                                                                                                                                          |
| `-names`                | `/tmp/gnockpit-names.json` | Persistent name registry                                                                                                                                                                                                                              |
| `-port`                 | `8080`                     | Web server port                                                                                                                                                                                                                                       |
| `-addr`                 | `0.0.0.0`                  | Bind address                                                                                                                                                                                                                                          |
| `-interval`             | `5s`                       | Dashboard refresh interval                                                                                                                                                                                                                            |
| `-v`                    | false                      | Verbose HTTP logging                                                                                                                                                                                                                                  |
| `--db-path`             | `/tmp/gnockpit.db`         | SQLite database for push notifications, the block history and block-ETA history; keep it on a persistent path (see Push Notifications, Block history, Block ETA)                                                                                      |
| `--history-pace`        | `1s`                       | Wait between the batches that fill the block history, back to genesis and up to the head after downtime; `0` for none (see Block history)                                                                                                             |
| `--history-batch`       | `100`                      | Commits asked in one JSON-RPC batch while filling the block history; halved until restart when the first `-rpc` refuses a batch, for 10 minutes when one times out; at `1`, the backfill never completes at a node's oldest block (see Block history) |
| `--chain-stuck-secs`    | `30`                       | Seconds without a new block before the chain-stuck alert fires                                                                                                                                                                                        |
| `--notify-config`       | (none)                     | Notification config file (JSON): the Shoutrrr URLs and the alerts each receives, reread when it changes; see External Notifications                                                                                                                   |
| `--send-timeout`        | `10s`                      | How long a push notification or webhook send may take before it is given up                                                                                                                                                                           |
| `--redact-secret`       | false                      | Strip notification URL secrets from logged Shoutrrr errors; see External Notifications                                                                                                                                                                |
| `--max-missed-in-a-row` | `10`                       | Consecutive blocks a validator must miss to be flagged down (alert fires + counted inactive); it recovers after signing that many in a row                                                                                                            |
| `--geoip-db`            | `/tmp/gnockpit-geoip.mmdb` | DB-IP City Lite mmdb powering the network map + country columns; auto-downloaded and refreshed monthly. Empty disables it.                                                                                                                            |
| `--asn-db`              | `/tmp/gnockpit-asn.mmdb`   | DB-IP ASN Lite mmdb powering cloud-provider detection; auto-downloaded and refreshed monthly. Empty disables it.                                                                                                                                      |
| `--hide-sources`        | false                      | Hide the configured source nodes from the peers list                                                                                                                                                                                                  |
| `--chain-name`          | (chain-id)                 | Display name for the chain in titles and the app icon; defaults to the chain-id (e.g. `test13` for chain-id `test-13`)                                                                                                                                |
| `--link`                | (none)                     | Extra header link button, `"Title\|URL"`; repeatable                                                                                                                                                                                                  |
| `--status-link`         | (none)                     | Header link with a live status dot, `"Title\|URL"`; repeatable. BetterStack status pages only for now: the dot reflects `<URL>/index.json`                                                                                                            |
| `--analytics`           | (off)                      | Load a third-party analytics script in the dashboard; only `simple-analytics` is supported. See Analytics.                                                                                                                                            |
| `--analytics-domain`    | (none)                     | Serve the analytics script and its no-JS pixel from a custom domain (e.g. `sa.example.com`) instead of the provider's own hosts; needs `--analytics`. See Analytics.                                                                                  |

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

- **Validator missing blocks** — per validator: fires when it misses N blocks in a row, and recovers once it signs N in a row (`--max-missed-in-a-row`)
- **Chain stuck** — fires when no new block is produced for N seconds, and recovers with the next block
- **Validator set changes** (`valset_changed`) — once per change: who joined, left, changed power or rotated its key, and the set's size before and after. A rotation is told as one once the change's events are read; before, it reads as a validator leaving and another joining. A change found while catching up after downtime says when it happened; the backfill of older blocks never alerts.
- **New coordinated halt** (`halt_scheduled`) — once per halt height the chain schedules and per device or webhook (one added while a halt is pending gets it at the next refresh), with its minimum version when the chain names one, and the time to it when estimated.
- **Coordinated halt approaching** (`halt_approaching`) — once per halt, N blocks or N minutes before it, as each device picks. A lead in minutes needs the block-time estimate; without one, it fires at the block before the halt at the latest.

The last three are one-off events: no recovery follows. What was sent to whom is kept in `--db-path`, so a restart sends nothing twice. Names in parentheses are the ones the notification config file (External Notifications) uses.

**Per-device settings:** Each browser/device has its own subscription, kept across saves (the server knows it by its push endpoint): saving again never sends an alert twice. Unchecking every alert ends the browser's push subscription, unless the device holds an ETA reminder. Changing notification settings on mobile does not affect desktop.

**ETA reminder:** where push works, the Block ETA card a link opens has a **notify** button: it reminds this device N blocks or N minutes before the card's height, once, and a tap on the notification opens the card. A lead in minutes without a block-time estimate fires once the height is reached. A device holds one reminder; setting one on another height replaces it, and the menu says so. A lead already met is refused. A page opened without a link (the installed app always opens at `/`) shows the reminded card, and closing the card removes its reminder. The page uses `/api/push/reminder`: `GET ?id=` reads the device's reminder (204 when it has none), `PUT` sets it and answers the device's `id` (409 when the lead is already met), `DELETE ?id=` removes it and says whether the device stays (`device_kept`: it goes when it has no alert left).

## External Notifications

gnockpit can forward alerts to Discord, Telegram, Slack and other services
through [Shoutrrr](https://github.com/containrrr/shoutrrr) URLs, each receiving
the alerts it lists. They are set in a JSON file passed with `--notify-config`:

```json
{
    "targets": [
        {
            "url": "discord://token@webhookid",
            "alerts": [
                "chain_stuck",
                "valset_changed",
                "halt_scheduled",
                { "halt_approaching": "1h" },
                { "validator_missing_blocks": ["g1…"] }
            ]
        },
        {
            "url": "telegram://token@telegram?chats=@channel",
            "alerts": [
                { "halt_approaching": "100 blocks" },
                "validator_missing_blocks"
            ]
        }
    ]
}
```

- `alerts` names each alert the target receives, at least one: `chain_stuck`,
  `validator_missing_blocks`, `valset_changed`, `halt_scheduled` and
  `halt_approaching` (see Push Notifications). A target receives a listed
  alert's recovery too.
- `halt_approaching` needs a lead: whole minutes (`"30m"`, `"1h"`, `"1h30m"`)
  or blocks, always plural (`"1 blocks"`, `"100 blocks"`).
- `halt_scheduled` and `halt_approaching` are sent once per target and halt. A
  target is known by its URL: one whose token changes is a new target, and a
  changed lead does not send again.
- `validator_missing_blocks` alone follows every validator; with a list of
  addresses, those only.
- The file is reread when it changes, at the next refresh (`-interval`): a
  webhook or an alert added takes effect without a restart. A file gnockpit
  refuses (bad JSON, an unknown key or alert, a bad option, data after the
  object, a URL Shoutrrr cannot use) or cannot read is logged once, and the
  previous config stays in force until a good one loads; at start, it stops
  gnockpit.
- A send that takes longer than `--send-timeout` (10 s by default) is given up
  and logged, for webhooks and browser pushes alike.
- The file holds the webhooks' secrets: keep it readable by gnockpit only.

A failed send is logged with Shoutrrr's error, which can hold the URL's secrets: a network failure quotes the service's request URL (Discord: `https://discord.com/api/webhooks/<id>/<token>`), and some services echo a malformed token. `--redact-secret` strips them before they are logged or returned. Each target's URL becomes `<scheme>:<fingerprint>`, as in `/api/notify/targets` (which also lists each target's alerts), and every other URL keeps only its scheme. Every piece of 6+ characters of a target's URL becomes `[redacted]` wherever it appears. That also blanks the URL's hostnames in DNS errors, and any word that matches a piece, e.g. `telegram` in `invalid [redacted] token`. It is off by default, which keeps errors verbatim.

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

## Block history

gnockpit records every block since genesis in the SQLite database
(`--db-path`; on a temporary path, a reboot means filling it again). It takes
about 100 bytes a block on onyx (56 validators): about 130 MB for 1.25M blocks.

- **Recorded** — each block's time, proposer and round, the validator set that
  signed it, and the members of that set that did not sign it for the block:
  absent, or a precommit for nil or for another block. Also each validator's
  vote delays (how far its precommit's time is from the commit's
  voting-power-weighted median time), as daily histograms; the expected
  proposer of each round of a block finalized after round 0, and a flag when a
  block's proposer is not the expected one or cannot be checked (checked on
  those blocks and on every 1,000th); and the events behind each validator set change.
  Kept beside them, per UTC day and validator: the absences by kind and the
  proposals expected and missed; the restarts (two consecutive blocks over 2
  minutes apart); and each validator's runs of consecutive absences (absent or
  other block).
- **Fill** — gnockpit follows the chain head from the first `-rpc` and, in
  between, fills the older blocks newest first, in JSON-RPC batches of
  `--history-batch` commits with `--history-pace` between them (also between
  the batches that catch up with the head after downtime): at the
  defaults (100 commits, 1 s), at most about 100 blocks a second, so a chain of
  1.6M blocks takes at least 4.5 hours. A batch the node refuses whole halves
  the batch size until restart; one that times out, for 10 minutes. HTTP 429 pauses for as long as `Retry-After` asks (a
  minute without one). The history is complete once it reaches block 1, or the
  oldest block the node serves: one it served right before refusing the block
  below in the same answer, well below the head, seen in two answers. gnockpit
  asks below that block again at each start, in case the node changed. A node
  that stays behind the head for minutes is logged. Each batch of two commits
  or more below the stored blocks opens with the lowest one, asked again. A
  block refused first in its answer (that block, or any block in a batch of
  one), well below the head, marks no oldest block (a backend far behind
  refuses it too); refused twice in a row, it pauses the backfill, asked again
  every 10 minutes, logged once. So with a batch of one commit
  (`--history-batch 1`, or a node taking one call a batch), the backfill pauses at the
  node's oldest block rather than completing. Pointing gnockpit at another
  chain drops the history.
- **Used by** — the validators table's Missed 24h and Missed props 24h (both 0
  until the history holds 24 h), and `/api/stats`' 1h / 24h /
  7d / 30d / total windows (`recorded_since` says how far back they reach): an
  absence or a precommit for another block counts as missed, a precommit for
  nil as signed; the validators card's **Timeline** (beside **Live**): every
  validator set change, newest first, with who joined, left, changed power or
  rotated its key, read back to genesis as the history fills; the set-change
  alerts; the [validator page](#validator-page); and
  [`/api/history`](#apihistory). While the history fills,
  Network State's header shows how much is filled and, once the server has
  measured the fill rate, the time left.

## Validator page

A click on a validator's name (the validators card's Live or Timeline view, or
a phone card) opens its page over the dashboard; `/?val=<operator or signing
address>` opens it directly. Back, Esc, × or the backdrop close it, and
**⧉ link** copies its link, which names the operator once gnockpit knows it.
The page reads [`/api/validator`](#apivalidator).

| Section    | What                                                                                                                                                                                          | Source                                                                                   | Scored                                                                                           |
| ---------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| Status     | in the set since (a key rotation is no break once its events are read) or not in it since, "since before" the history's first day when its join is older; power and share; country · provider | the node's current set; the block history's set changes for since; its matched peer      | no                                                                                               |
| Profile    | description (its markdown rendered, links opening in a new tab), server type, operator, signing address and key, keep running, last rotation                                                  | the valopers realm, read every 15 min; keep running and last rotation through `vm/qeval` | no                                                                                               |
| Uptime     | per UTC day, of the blocks it was in the set for: signed (a nil vote signs), absent, other block; outages (10+ in a row absent or for another block, recovery runs left out)                  | every block's commit                                                                     | signed ÷ blocks in the set (50 %) and outages (20 %), over the 30 complete UTC days before today |
| Proposals  | per UTC day: the rounds it was the expected proposer of until the block was made, and those it missed                                                                                         | block headers and each round's expected proposer                                         | missed ÷ expected (10 %), over the same window                                                   |
| Recovery   | after a restart, how long it took to sign again; a sample only when it was in the set and signing before, and signs again; one still absent shows as pending                                  | restarts and absences                                                                    | the median of its last 3 samples within the 180 complete UTC days before today (20 %)            |
| Vote delay | its precommits' delay from each commit's voting-power-weighted median time                                                                                                                    | the daily vote-delay histograms; the live window for the last 100 blocks                 | no                                                                                               |
| Timeline   | joins, leaves, power changes, key rotations, outages, recoveries and a pending one, newest first                                                                                              | set changes and their events, absences, restarts                                         | its outages and recoveries                                                                       |

**Scored** is the rule and weight the validator scoring gives a datum. Outages
leave recovery runs out, as the scoring does; the other figures count every
height, the scoring's other filters (recovery runs out of uptime, proposals on
signed heights only, restart heights out) left to it. A scored
value is green where its sub-score is full, orange partial, red zero: signed
≥ 99.9 / 95 to 99.9 / ≤ 95 %; outages 0 / 1 to 9 / 10 or more; proposals missed
none / under 5 % / 5 % or more; recovery ≤ 10 min / 10 min to 24 h / ≥ 24 h.

On a wide screen the charts share one day axis (at least 8 px a day), scroll
together and open on the newest day; on a phone, Uptime and Proposals are
calendars. Each bar or day has a popover with its numbers.

Every tooltip in gnockpit is one popover, in two forms: text (the column headers,
every ⓘ and the buttons' hints) and rows (a title, then label / value rows, with a colour swatch
where a colour means something), for the Recent Blocks boxes, the address and
node ID cells, the peer RPC ✓, the provider's IP, the map's dots, the Missed last
100 split and every chart. Hover on a desktop, tap on a phone.

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
  view keeps one line and the ranges as a bar. Nothing is stored server-side
  unless **notify** sets a reminder (see Push Notifications), which keeps the
  card's height, start and label; ✕ removes the target from the link, and its
  reminder.
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

| Endpoint         | Description                                                                                                                                                                                                          |
| ---------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `/api/status`    | Health summary (`status`/`chain`/`height`/`reason`/`time`) plus live network state, the last 100 blocks with signing status, and per-peer / per-validator column data; see [`/api/status`](#apistatus).              |
| `/api/stats`     | Aggregate stats: per-validator missed-block windows, cloud-provider and country aggregates, validator-set health, and the Nakamoto coefficient.                                                                      |
| `/api/eta`       | Block ETAs: when a height is expected, which height a time reaches, or the scheduled halt; see [`/api/eta`](#apieta).                                                                                                |
| `/api/history`   | The block history: how far back it reaches, the blocks of a range of heights with the validators that did not sign each for it, daily vote delays, and the validator set changes; see [`/api/history`](#apihistory). |
| `/api/validator` | One validator's page data: status, keys, profile, timeline, daily series, recoveries, scoring-window summary and live figures; see [`/api/validator`](#apivalidator).                                                |
| `/api`           | Raw internal snapshot dump (unstable shape; for debugging).                                                                                                                                                          |
| `/badge.svg`     | SVG status badge.                                                                                                                                                                                                    |

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

### `/api/status`

Each peer and validator has `name`, and where its IP resolves, `country`,
`city`, `lat`/`lon` and cloud `provider`; a validator's are its matched peer's.
Unknown fields are omitted; when only one of `lat`/`lon` is present, the other
is 0. Each validator also has:

| Field                                            | Content                                                                      |
| ------------------------------------------------ | ---------------------------------------------------------------------------- |
| `address`, `voting_power`, `spof`                | `spof`: it holds a third of the power or more.                               |
| `inactive`                                       | Present, true, while it is flagged down.                                     |
| `missed_100`                                     | Blocks of the last 100 it missed: absent, or a vote for another block.       |
| `other_block_100`, `nil_100`                     | Of those, the votes for another block; and its nil votes, counted as signed. |
| `vote_delay_ms`                                  | The median delay of its precommits in the last 100 blocks; null without one. |
| `missed_24h`                                     | Blocks it missed in the last 24 h, from the block history.                   |
| `missed_proposals_24h`, `expected_proposals_24h` | The proposals it missed in the last 24 h, of those it was expected to make.  |

**Breaking:** the per-validator `avg_block_ms` (block time when it proposed) is
gone.

### `/api/history`

The query picks what comes back, and the queries combine. Times are RFC 3339
UTC to the millisecond.

| Query                 | Answer                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| none                  | `chain` and `filled`: `from` and `to` (the lowest and highest stored heights), `from_time` (the time of block `from`; left out while nothing is stored), `floor` (the oldest block the first `-rpc` serves, when it refused an older one), `complete` (`from` is 1, or the floor) and `rate` (heights added below `from` per second over the last 5 minutes; left out until measured, when the backfill has not moved in that window, and once complete).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| `?from=H1&to=H2`      | The same, plus `blocks[]` and `valsets` for at most 1,000 heights. Heights outside `filled` are left out: a range the history does not hold gives an empty `blocks` and `valsets`. Each block has `height`, `time`, `proposer`, `round`, `txs`, `valset` (the hash of its validator set, a key of `valsets`) and the members that did not sign it for the block: `absent`, `nil` (a precommit for no block) and `other_block` (a precommit for another block). A block finalized after round 0 also has `rounds`: the expected proposer of rounds 0 to `round`, `""` where unknown. `flag` says why a block's proposer check failed, when it did (checked on those blocks and on every 1,000th). A block that opens a new validator set (its `valset` differs from the block below's) has `set_change` once its events are read: `source`, the block they were read from, and `events` (`type`, `operator`, `address` or `old_address` and `new_address`, `power`, left out for a removal or a rotation, whose events carry none, and `applied`); it is left out while that read is pending (retried every 10 minutes), when the source block holds no set event, and for good when the source block is below `floor`. `valsets` gives each set's members in set order: `address`, `pub_key` (base64) and `power`. |
| `?days=N`             | The same, plus `vote_delays` for the last N UTC days, today included (N from 1 to 31): `edges_ms`, the edges of the delay buckets in ms (one bucket more than edges: the first below the first edge, bucket i between edges i−1 and i, the last from the last edge up; a delay on an edge falls in the bucket farther from 0), and `days[]`, each with `day` (`YYYY-MM-DD`) and `validators[]`: `address`, `votes`, `mean_ms` and `buckets`. Days without a recorded vote are left out.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| `?changes=N&before=H` | The same, plus at most N (1 to 100) validator set changes below height H (the newest when left out), newest first, in `changes[]`, and `next_before`, the H that asks for the next page: set when the page is full and does not end at block 1, so a full last page is followed by an empty one. Each change has `height`, `time`, `genesis` (true for block 1's set, its members all added; left out otherwise), the members `added[]` and `removed[]` (`address`, `moniker` when gnockpit knows one, `power`), `repowered[]` (`address`, `moniker` when known, `before`, `after`), `rotated[]` (an applied `ValoperRotated` linking a removed and an added address: `operator`, `old_address`, `new_address`, `moniker` of the new one when known, `power`), `size_before`, `size_after`, `power_before` and `power_after` (total voting power), `events` (as in `set_change`) and `events_pending` (the events are not read yet).                                                                                                                                                                                                                                                                                                                                                                               |

Errors come as `{"error": "…"}`: 400 for a malformed query (a range that is not
two heights from 1 with `to` at least `from`, or over 1,000 heights; `days`
outside 1–31; `changes` outside 1–100, or `before` not a height from 1 or given
without `changes`), 500 when the database read fails.

```bash
curl -s 'http://localhost:8080/api/history?from=1183000&to=1183010'
curl -s 'http://localhost:8080/api/history?days=7'
curl -s 'http://localhost:8080/api/history?changes=20'
```

### `/api/validator`

`?val=` is a g1 address: a validator's operator or one of its signing keys.
gnockpit resolves it, in this order, to the operator it is, the operator a set
event links it to as a signing key, or the operator whose valopers profile names
it; a key none of those link, but a stored or the current set holds, is answered
alone, with `operator` empty. The operator's keys are taken together. Times are
RFC 3339 UTC to the millisecond, days `YYYY-MM-DD`.

| Field                                     | Content                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| ----------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `chain`, `address`, `operator`, `moniker` | `address` is the one asked.                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `status`                                  | `in_set`; `since_height` and `since_time`: in the set, its latest join with no leave after (a key rotation is none), out of it, its latest leave; in the set with no join or leave stored, the stored history's first block with `since_before` true (it joined before); left out while the stored history contradicts the current set; `signing_address`, its key in the set; `power` and `share` of the set's total power. Left out until the node's current set is known.   |
| `keys[]`                                  | Its signing keys: `address`, `from` (0: before the stored history) and `to` (left out: still its key). From the set events (a power change keeps a key's span), with the profile's signing key when no event names it; an unlinked key alone.                                                                                                                                                                                                                                  |
| `location`                                | Its matched peer's `country`, `city` and `provider`; left out without one.                                                                                                                                                                                                                                                                                                                                                                                                     |
| `profile`                                 | Its valopers profile: `moniker`, `description`, `server_type`, `operator`, `signing_address`, `signing_pubkey`, and `keep_running` and `last_rotation_height` when the node answered `vm/qeval`. Left out without a profile.                                                                                                                                                                                                                                                   |
| `timeline[]`                              | Newest first, each with `kind`, `height`, `time` and its kind's fields: `recovered` (`restart_height`, `duration_ms`; `height` is the first height signed again), `recovering` (`restart_height`, at the restart: not signed since), `outage` (`to`, `blocks`, `end_time`, `ongoing`), `power` (`before`, `after`), `rotated` (`old_address`, `new_address`), `joined`, `left` and `genesis` (`power`). A rotation reads as a `left` and a `joined` until its events are read. |
| `vote_delay_edges_ms`                     | The vote-delay buckets' edges, as in `/api/history`.                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `days[]`                                  | Each UTC day with a row, ascending: `day`, `in_set`, `signed`, `absent`, `nil`, `other_block`, `proposals_expected`, `proposals_missed`, `votes`, `vote_delay_mean_ms` and `vote_delay_buckets`.                                                                                                                                                                                                                                                                               |
| `recoveries[]`                            | Its samples, ascending: a restart it was in the set and signing before, with fewer than 10 absences up to it. `restart_height` (b: block b+1 came over 2 minutes after block b), `restart_time` (block b+1's), `signed_at`, `duration_ms`, `pending` (block `signed_at`+1, which times it, is not stored yet; `signed_at` and `duration_ms` are 0) and `scored` (one of the last 3 of the 180 complete UTC days before today).                                                 |
| `summary`                                 | Over the 30 complete UTC days before today (`first_day`, `last_day`): `in_set` to `votes` summed over the days, `vote_delay_mean_ms` their mean, and `outages` (one counts once 10 of its heights are inside); `recovery`: the `samples` within the 180 complete UTC days before today, `median_ms` of the last 3 and `slowest_ms` of all.                                                                                                                                     |
| `live`                                    | The validators table's: `vote_delay_ms` (null without one), `missed_100`, `nil_100`, `other_block_100`, `missed_24h`, `missed_proposals_24h` and `expected_proposals_24h`.                                                                                                                                                                                                                                                                                                     |

Errors come as `{"error": "…"}`: 400 when `val` is not a g1 address, 404 for
one gnockpit knows nothing of, 500 when the database read fails.

```bash
curl -s 'http://localhost:8080/api/validator?val=g1u7g4cejjqsm44nha34hzpzu6df6a7egc9rpqw5'
```

## Features

- Live WebSocket dashboard with real-time updates
- Multi-source consolidation — query several RPC endpoints; peers unioned & deduped, freshest source drives chain state
- Consensus monitoring — height, round, step, prevotes, precommits per validator
- Peer management — validator detection, health status, RPC reachability
- Network map — IP-geolocated peers, per-country list, Country + cloud-Provider columns
- Signing stats — per-validator blocks missed in the last 100 (absent or other block; nil votes shown apart, counted as signed), vote delay, proposals missed in the last 24 h, and missed-block counts over 1h/24h/7d/30d/total
- Validator page — a validator's status, valopers profile, uptime, proposals, recoveries after restarts, vote delay and timeline, with what the scoring counts (`/api/validator`)
- Block ETA — when a height will be produced (or which height at a given time), with 80%/95% ranges and drift from the original estimate; countdown cards for shared links and for the halt scheduled on chain
- HTTP API — CORS-open `/api/status`, `/api/stats`, `/api/eta`, `/api/history` and `/api/validator` for external badges, dashboards and scripts
- Alerts — browser push (PWA) and external notifications (Discord, Telegram, …, each webhook its own alerts) for chain-stuck, missed blocks, validator set changes and coordinated halts, and a per-device reminder before an ETA card's height
- Recent blocks — the last 100 blocks with signing visualization
- Block history — every block since genesis: proposer, round, the validators that did not sign it for the block and how, vote delays, expected round proposers and validator set changes, shown as a timeline (`/api/history`)

## License

MIT
