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

| Flag                      | Default                    | Description                                                                                                                                                                                                                                                                                                                                                               |
| ------------------------- | -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `-rpc`                    | `http://127.0.0.1:26657`   | Tendermint RPC endpoint; repeatable to consolidate several sources (flag order = precedence)                                                                                                                                                                                                                                                                              |
| `-names`                  | `/tmp/gnockpit-names.json` | Persistent name registry                                                                                                                                                                                                                                                                                                                                                  |
| `-port`                   | `8080`                     | Web server port                                                                                                                                                                                                                                                                                                                                                           |
| `-addr`                   | `0.0.0.0`                  | Bind address                                                                                                                                                                                                                                                                                                                                                              |
| `--read-header-timeout`   | `10s`                      | How long a client may take to send a request's headers before gnockpit closes its connection unanswered; `0` for none, a negative value refused                                                                                                                                                                                                                           |
| `-interval`               | `5s`                       | Dashboard refresh interval                                                                                                                                                                                                                                                                                                                                                |
| `-v`, `--verbose`         | false                      | Verbose HTTP logging: each request to the node                                                                                                                                                                                                                                                                                                                            |
| `--db-path`               | `/tmp/gnockpit.db`         | SQLite database for push notifications, the block history, block-ETA history and the published rankings; keep it on a persistent path (see Push Notifications, Block history, Block ETA, Ranking)                                                                                                                                                                         |
| `--history-pace`          | `1s`                       | Wait between the batches that fill the block history, back to genesis and up to the head after downtime; `0` for none (see Block history)                                                                                                                                                                                                                                 |
| `--history-batch`         | `100`                      | Commits asked in one JSON-RPC batch while filling the block history; halved until restart when the first `-rpc` refuses a batch, for 10 minutes when one times out; at `1`, the backfill never completes at a node's oldest block (see Block history)                                                                                                                     |
| `--chain-stuck-secs`      | `30`                       | Seconds without a new block before the chain-stuck alert fires                                                                                                                                                                                                                                                                                                            |
| `--notify-config`         | (none)                     | Notification config file (JSON): the Shoutrrr URLs and the alerts each receives, reread when it changes; see External Notifications                                                                                                                                                                                                                                       |
| `--send-timeout`          | `10s`                      | How long a push notification or webhook send may take before it is given up                                                                                                                                                                                                                                                                                               |
| `--redact-secret`         | false                      | Strip notification URL secrets from logged Shoutrrr errors; see External Notifications                                                                                                                                                                                                                                                                                    |
| `--public-url`            | (none)                     | This gnockpit's public URL, appended to each external notification; the ranking alert links `<URL>/?view=ranking`                                                                                                                                                                                                                                                         |
| `--max-missed-in-a-row`   | `10`                       | Consecutive blocks a validator must miss to be flagged down (alert fires + counted inactive); it recovers after signing that many in a row                                                                                                                                                                                                                                |
| `--geoip-db`              | `/tmp/gnockpit-geoip.mmdb` | DB-IP City Lite mmdb powering the network map + country columns; auto-downloaded and refreshed monthly. Empty disables it.                                                                                                                                                                                                                                                |
| `--asn-db`                | `/tmp/gnockpit-asn.mmdb`   | DB-IP ASN Lite mmdb powering cloud-provider detection; auto-downloaded and refreshed monthly. Empty disables it.                                                                                                                                                                                                                                                          |
| `--hide-sources`          | false                      | Hide the configured source nodes from the peers list                                                                                                                                                                                                                                                                                                                      |
| `--chain-name`            | (chain-id)                 | Display name for the chain in titles and the app icon; defaults to the chain-id (e.g. `test13` for chain-id `test-13`)                                                                                                                                                                                                                                                    |
| `--link`                  | (none)                     | Extra header link button, `"Title\|URL"`; repeatable                                                                                                                                                                                                                                                                                                                      |
| `--status-link`           | (none)                     | Header link with a live status dot, `"Title\|URL"`; repeatable. BetterStack status pages only: the dot reflects `<URL>/index.json`                                                                                                                                                                                                                                        |
| `--analytics`             | (off)                      | Load a third-party analytics script in the dashboard; only `simple-analytics` is supported. See Analytics.                                                                                                                                                                                                                                                                |
| `--analytics-domain`      | (none)                     | Serve the analytics script and its no-JS pixel from a custom domain (e.g. `sa.example.com`) instead of the provider's own hosts; needs `--analytics`. See Analytics.                                                                                                                                                                                                      |
| `--ranking`               | false                      | Compute and serve the daily validator ranking at `/api/ranking`, from the block history; see Ranking                                                                                                                                                                                                                                                                      |
| `--ranking-exclude`       | (none)                     | Operators left out of the ranking's order: operator addresses as the chain writes them (lowercase, their bech32 checksum checked), comma-separated; repeatable, each occurrence adding entries; spaces around an entry are trimmed; a malformed or empty entry stops start-up. The list is published in `/api/ranking`. Needs `--ranking`: set without it, start-up stops |
| `--ranking-hide-excluded` | false                      | Tell the ranking's readers to hide the excluded operators; `/api/ranking` lists them either way. Needs `--ranking`: set without it, start-up stops                                                                                                                                                                                                                        |

## Analytics

gnockpit ships with **no analytics and no third-party origins at all**. The
dashboard is one HTML file and its scripts, all served by the binary. Nothing about your node, your
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

**Per-device settings:** Each browser/device has its own subscription, kept across saves (the server knows it by its push endpoint): saving again never sends an alert twice. Unchecking every alert ends the browser's push subscription, unless the device holds an ETA reminder or an alert the menu does not show: a validator's that left the set, or any validator's while the menu cannot read the validators. Changing notification settings on mobile does not affect desktop.

**Saving:** the menu saves through `/api/push/subscribe` (`POST`): the device's push keys (`endpoint`, `p256dh`, `auth`) and its `alerts`, which replace the ones it holds; the answer is the device's `id` (201 for a new device), empty once the device has no alert and no reminder left. `keep` lists the alert types whose stored alerts the save leaves as they are: while the menu cannot read the validators (`/api/push/entities` gives no answer, an error, or its 503 while gnockpit has read no consensus state: at start, or while every source is down or its consensus read fails), it saves with `"keep": ["validator_missing_blocks"]`, so the device keeps the validator alerts the menu cannot show. A kept type must be one of `chain_stuck`, `validator_missing_blocks`, `valset_changed`, `halt_scheduled` and `halt_approaching` (400 `unknown alert "<type>"` otherwise), and none the same save sends in `alerts` (400 `<type> is kept and saved at once`). Beside `keep`, `shown` lists the addresses of the validators the menu showed, which it sends once it read them: the device's `validator_missing_blocks` alerts on any other validator stay as stored unless the save names it. The menu shows the set `/api/push/entities` listed when it opened, so an alert on a validator that left the set survives a save and still holds when it rejoins, and a set that changes while the menu is open changes nothing. Each entry must be non-empty (400 `shown: "" is no validator address` otherwise), and a save keeping `validator_missing_blocks` shows none (400 `validator_missing_blocks is kept and shown at once`). An empty `shown` is accepted: like a save with neither `shown` nor `keep`, it shows no validator, so the save leaves every stored validator alert it does not name.

**Test push:** once the device is saved, the menu's **Test** button sends it a notification through `/api/push/test` (`POST {"id": "<device id>"}`). The answer is 200 once the device's push service takes the push; 400 when the body names no `id`; 404 when the server does not know the device (the menu then asks to save again); 405 to a method other than `POST`; 500 when the server cannot read the device or build its push (a stored key that is not a P-256 point); 502 when its push service refuses the push or cannot be reached, the push service's answer going to gnockpit's log; 503 when push is not configured, which the shipped binary never answers, as it always sets push up.

**ETA reminder:** where push works, the Block ETA card a link opens has a **notify** button while it counts down to its height or holds this device's reminder: it reminds this device N blocks or N minutes before the card's height, once, and a tap on the notification opens the card. A lead in minutes without a block-time estimate fires once the height is reached. A device holds one reminder; setting one on another height replaces it, and the menu says so. A lead already met is refused. A page opened without a link (the installed app always opens at `/`) shows the reminded card, and closing the card removes its reminder. The page uses `/api/push/reminder`: `GET ?id=` reads the device's reminder (204 when it has none), `PUT` sets it and answers the device's `id` (409 `#N is already reached`, or `#N is <lead> away or less` when the lead is already met; a lead reads `100 blocks`, `1 block`, `90m` or `1h`), `DELETE ?id=` removes it and says whether the device stays (`device_kept`: it goes when it has no alert left).

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
  `validator_missing_blocks`, `valset_changed`, `halt_scheduled`,
  `halt_approaching` (see Push Notifications) and `ranking`. A target receives
  the recovery of `chain_stuck` and `validator_missing_blocks` too; the
  others, `ranking` included, are one-off events with none.
- `ranking` is sent once a day, after the day's ranking is published (needs
  `--ranking`; see Ranking), once per target and day: a restart sends nothing
  again. After a restart, today's day, once stored, is served as stored when
  its formula and exclusion list match; with another list or formula,
  today's ranking is computed again and replaces it, with no second alert that
  day. It is for targets only: a
  browser device refuses it (400 `unknown alert "ranking"`), and it takes no
  options. The message holds:
    - the title: the ranking's window and formula version;
    - one line per ranked operator: its rank (operators sharing a rank repeat
      the number), name (its short address without one), score to one
      decimal, rounded down as the Ranking view writes it, and change since
      the previous published ranking: `▲n` or `▼n` places, `=`, or `new` for
      an operator not ranked in it;
    - `Not ranked yet: <n>` when n is above 0; excluded operators are neither
      listed nor counted;
    - `Rank changes not shown: <reason>` instead of the changes, when there is
      no earlier ranking (`no earlier ranking`), the formula version differs
      (`formula v<N> since the previous ranking`) or the exclusion list differs
      (`exclusion list changed since the previous ranking`);
    - with `--public-url`, a last line linking the ranking view:
      `--public-url` followed by `/?view=ranking`.
- `halt_approaching` needs a lead: whole minutes (`"30m"`, `"1h"`, `"1h30m"`)
  or blocks (`"1 block"`, `"100 blocks"`; `"1 blocks"` reads too).
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
- A message's text has a zero-width space after each `@` and `<`, its link
  aside: a name a peer, the genesis or a profile gives, such as `@everyone`,
  `<@id>` or `<!channel>`, mentions no one.

The `ranking` message holds at most 2,000 characters (Unicode code points, the
zero-width spaces counted), so a longer ranking goes as several messages, split
at line breaks. For example:

```text
🔔 gnoland-1 — Validator ranking, 2026-10-27 – 2026-11-25 UTC (formula v1)
#1 Alice 99.0 ▲1
#1 Bob 99.0 =
#3 Carol 97.5 ▼2
#4 Dave 90.0 new
Not ranked yet: 2
https://gnockpit.example.com/?view=ranking
```

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
  alerts; the [validator page](#validator-page); the ranking (see
  [Ranking](#ranking)); and [`/api/history`](#apihistory). While the history fills,
  Network State's header shows how much is filled and, once the server has
  measured the fill rate, the time left.

## Validator page

A click on a validator's name (the validators card's Live, Timeline or Ranking
view, or a phone card) opens its page over the dashboard; `/?val=<operator or
signing address>` opens it directly. Back, Esc, × or the backdrop close it, and
**⧉ link** copies its link, which names the operator once gnockpit knows it.
The page reads [`/api/validator`](#apivalidator).

| Section    | What                                                                                                                                                                                                         | Source                                                                                   | Scored                                                                                                                            |
| ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| Status     | in the set since (a key rotation is no break once its events are read) or not in it since, "since before" the history's first day when its join is older; power and share; country, with its flag · provider | the node's current set; the block history's set changes for since; its matched peer      | no                                                                                                                                |
| Profile    | description (its markdown rendered, links opening in a new tab), server type, operator, signing address and key, keep running, last rotation                                                                 | the valopers realm, read every 15 min; keep running and last rotation through `vm/qeval` | no                                                                                                                                |
| Uptime     | per UTC day, of the blocks it was in the set for: signed (a nil vote signs), absent, other block; outages (10+ in a row absent or for another block, recovery runs left out)                                 | every block's commit                                                                     | signed ÷ blocks in the set, recovery runs left out of both (50 %), and outages (20 %), over the 30 complete UTC days before today |
| Proposals  | per UTC day: the rounds it was the expected proposer of until the block was made, and those it missed                                                                                                        | block headers and each round's expected proposer                                         | missed ÷ expected on the heights it signed, restart heights left out (10 %), over the same window                                 |
| Recovery   | after a restart, how long it took to sign again; a sample only when it was in the set and signing before, and signs again; one still absent shows as pending                                                 | restarts and absences                                                                    | the median of its last 3 samples within the 180 complete UTC days before today (20 %)                                             |
| Vote delay | its precommits' delay from each commit's voting-power-weighted median time                                                                                                                                   | the daily vote-delay histograms; the live window for the last 100 blocks                 | no                                                                                                                                |
| Timeline   | joins, leaves, power changes, key rotations, outages, recoveries and a pending one, newest first                                                                                                             | set changes and their events, absences, restarts                                         | its outages and recoveries                                                                                                        |

**Scored** is the rule and weight the validator scoring gives a datum. The
scored figures apply the scoring's filters: uptime and outages leave recovery
runs out, and proposals count only the heights it signed, restart heights left
out; the daily charts count every height. A scored value is green where its sub-score is full, orange partial, red zero: signed
≥ 99.9 / 95 to 99.9 / ≤ 95 %; outages 0 / 1 to 9 / 10 or more; proposals missed
none / under 5 % / 5 % or more; recovery ≤ 10 min / 10 min to 24 h / ≥ 24 h.
The band reads the value, as the Ranking view colours it, and the figure is
never written better than the value: signed rounded down (99.895 % reads
99.89 %, orange), the missed share and the recovery up (600.4 s reads
10 min 01 s, orange).

On a wide screen the charts share one day axis (at least 8 px a day), scroll
together and open on the newest day; on a phone, Uptime and Proposals are
calendars. Each bar or day has a popover with its numbers.

Every tooltip in gnockpit is one popover, in two forms: text (the column headers,
every ⓘ and the buttons' hints) and rows (a title, then label / value rows, with a colour swatch
where a colour means something), for the Recent Blocks boxes, the address and
node ID cells, the peer RPC ✓, the provider's IP, the map's dots, the Missed last
100 split, every chart and the Ranking view's values. Hover on a desktop, tap on a phone.

## Ranking

gnockpit ranks the community validator operators once per UTC day by a fixed
formula, from chain data only, so that anyone with the chain's blocks can
recompute every rank. `/api/ranking` serves each operator's raw inputs beside
its sub-scores, with the window and the formula version.

### What the ranking reads

- **Chain data only,** read over a node's RPC: each block's header (time,
  proposer, round) and commit, the validator sets with their proposer
  priorities (`/validators?height=`), the set-change events in block results
  (`ValidatorAdded`, `ValidatorRemoved`, `ValoperRotated`) and the valoper
  profiles. Nothing from the tx-indexer, no vote timestamps, no arrival times.
- **Operators, not keys:** an operator's profile and the set-change events link
  its signing keys to it (a rotation applied at height H gives the blocks up to
  H+1 to the old key, from H+2 to the new one). A key no chain record links is
  ranked alone, under its signing address, never linked by moniker. The
  ranking waits until a refresh reads the valopers realm's whole list and
  every operator it lists has a profile, read then or by an earlier refresh:
  gnockpit reads them every 15 minutes, an operator whose read fails keeping
  its last profile, and logs the operators it could not read. A profile that
  never reads, or a list that never reads whole, holds the ranking.
- **Per block, per member of its set:** signed when its precommit in the commit
  is for the block or for nil; absent when its slot is empty or its precommit
  is for another block.
- **Window:** the 30 complete UTC days before the day ranked, a block belonging
  to the UTC day of its header time. Every block weighs the same; voting power
  plays no part.
- **As of the window's end:** the ranking reads each operator's blocks up to
  the window's last height, and the time of the next block, the day ranked's
  first, which times that height. A recovery not ended within the window is pending: it
  gives no sample, and its run still counts as a recovery run. A run of
  absences going on at the window's end ends there. Keys are linked to
  operators by the valoper profiles, read as the ranking is computed, and
  every stored set-change event: the published keys may list a key rotated
  in after the window, which adds nothing to a score. So two gnockpits publish
  the same ranks and scores, unless a valoper profile or a key link changed
  between their computations; an operator without a profile is named by the
  moniker each gnockpit knows its node by.
- **Minimum:** an operator is ranked once its block time in the set within the
  window reaches 168 hours: the sum, over its heights in the set, of each
  one's time to the next block. Until then it is not ranked yet.
- **Restarts:** two consecutive blocks b and b+1 whose header times are more
  than 2 minutes apart; b is the restart height.
- **Recovery samples,** at each restart, for an operator in the set at b−1 and
  b: h is the first height from b it signed, and the sample is
  time(block h+1) − time(block b+1), 0 when it signed height b itself, the
  height decided across the restart. It gets none when its run of absences up
  to h−1 held 10 heights or more up to b−1: it was already in an outage. That
  run, its recovery run, leaves uptime and outages for up to 24 hours of block
  time after block b+1; past that, the rest counts as absences and as an
  outage. The recovery sub-score takes the last 3 samples within the 180
  complete UTC days before the day ranked, whatever the window.
- **Proposals,** at each height h committed at round r: the expected proposer
  of each round 0 to r is recomputed from the proposer priorities.
  `/validators?height=h` returns the set after round 0's proposer was chosen
  and charged, so round 0's proposer is the member charged between the sets at
  h−1 and h, and round r ≥ 1's is the proposer after r increments of the set
  at h. k is the earliest of those rounds whose proposer is the header's
  `proposer_address`. An operator is expected at the rounds 0 to k it was the
  proposer of, and missed those before k. Only the heights it signed count (a
  precommit for another block does not), and restart heights b never do.
- **Proposals self-check:** a block of the window with no such k, or whose
  round 0 proposer cannot be recomputed, disables the proposals sub-score for
  every operator; the ranking lists those heights.

### Sub-scores and score

Each sub-score runs from 0 to 1, linear between the anchors of its mapping and
flat past either end:

| Sub-score | Weight | Raw value                                                                                                                                                                   | Mapping                                          |
| --------- | ------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------ |
| Uptime    | 50     | signed ÷ blocks in the set, the recovery runs' heights left out of both                                                                                                     | ≤ 95 % → 0, ≥ 99.9 % → 1                         |
| Outages   | 20     | runs of 10 or more consecutive absences, recovery runs left out; a run open at the window's start is clipped to the window, and counts once 10 of its heights are inside it | 0 → 1, ≥ 10 → 0                                  |
| Recovery  | 20     | the median of the recovery samples (of two, their mean), to the ms, truncated                                                                                               | ≤ 10 min → 1, 1 h → 0.66, 6 h → 0.33, ≥ 24 h → 0 |
| Proposals | 10     | missed ÷ expected proposals                                                                                                                                                 | 0 % → 1, ≥ 5 % → 0                               |

The score is the sum of each sub-score times its weight, 100 for full marks,
shown to one decimal. A sub-score without data passes its weight to the others
in proportion to theirs: no counted block for uptime (all its blocks in
recovery runs), no recovery sample, no proposal expected, or proposals disabled
by the self-check. Without a recovery sample, the weights are 62.5,
25 and 12.5.

### Order and shared ranks

Five keys order the ranked operators, each deciding only between operators
equal on the keys preceding it:

1. the score, unrounded, higher first;
2. the raw uptime, higher first;
3. the longest absence, shorter first: the longest run of absences, in blocks,
   clipped to the window, recovery runs left out, runs under 10 blocks
   included;
4. the median recovery time, shorter first, an operator without a sample after
   every operator with one;
5. the missed-proposal share, lower first, 0 with no proposal expected; left
   out while the self-check disables proposals, since counts it cannot trust
   decide no order either.

The score, the uptime and the missed share compare exactly, as fractions of
the counts (the mappings' anchors are decimals), never as rounded percentages
or floating-point sums. Operators equal on all five keys (on the first four
while proposals are disabled) share a rank, and the next rank skips
accordingly (1, 1, 3). The operator address, in byte order, only orders the
listing within a shared rank.

### Who is ranked

- **The set:** only the operators in the validator set at the window's last
  height. One that left the set earlier is not ranked, whatever its hours in
  it; its record stays on its validator page.
- **Exclusions:** the deployment's exclusion list names operators left out of
  the order, the gno.land team's and close partners' validators, so that only
  community validators are ranked. The excluded are scored the same way and
  published with their values and no rank.
- **Listing:** the ranked operators in order, then the excluded by score, then
  those not ranked yet by hours in the set, longest first.
- Two gnockpits reading the same chain, with the same exclusion list, publish
  the same ranks and scores, unless a valoper profile or a key link changed
  between their computations.

### Formula version

Every constant of the formula is fixed in code, never a flag: the weights, the
mappings, the 168 hours, the 10-block outage, the 2-minute restart, the
24-hour recovery cap, the 30-day window, the 180 days and 3 samples of
recovery. `ranking.FormulaVersion` (1) names them: a change to any of them
bumps it, and each published ranking carries the version it was computed with.

### Recomputing a rank

`/api/ranking` serves the published ranking: its window (first and last UTC
day and height), its formula version, its exclusion list (`exclusions`), the
heights that disabled the proposals sub-score, and per operator its raw inputs (blocks in the set and in recovery
runs; signed, nil, absent and other-block counts; each outage; the longest
absence; each recovery sample and their median; proposals expected and missed)
beside its sub-scores, weights and points. The formula turns those inputs into
every score and rank, and a node's RPC serves the blocks, their results, the
validator sets and the valoper profiles (`/abci_query`) to recount the inputs
themselves.

### Ranking view

With `--ranking`, the validators card offers a third view, **Ranking**, beside
**Live** and **Timeline**. The dashboard learns from `/api/boot` whether the
ranking is on, so the card opens on a saved or requested Ranking view once the
first answer says it is. `/?view=ranking` opens the dashboard on it, as `?view=live`
and `?view=timeline` open the others, over the view the browser saved; a
`?view=` is never saved; a click on the switch saves its view and takes
`?view=` out of the address, so a reload opens the view clicked. On a server without `--ranking`, `?view=ranking` opens Live. The view
reads `/api/ranking` when it shows, then every minute:

- on the meta line, the ranking's window, heights and formula version, that
  it is updated daily after 00:00 UTC (as soon as the day's first block is stored),
  and **How is this ranked?**, which opens a dialog on how anyone can recompute
  the ranking (its data, formula, window and exclusion list, with the `curl`
  line reading `/api/ranking` from the dashboard's own address), what it
  measures and how it breaks ties, why that is fair, what it does not score,
  and its calibration;
- a row per operator, a card on a screen up to 768 px wide: its rank, repeated
  for operators sharing it; its name, a click on which opens its validator
  page; its score, to one decimal; then its uptime, to 2 decimals, outages,
  recovery median, rounded up to the unit it is written in (the second under
  an hour, the minute to 48 h, the hour beyond), and missed proposals
  (missed / expected),
  each in its sub-score's colour (green full marks, orange partial, red zero)
  followed by its points, to one decimal, or `—` without a sub-score, whose
  weight the others share. No figure is written better than its value: the
  score, the uptime and the points are rounded down (99.96 reads 99.9,
  99.895 % reads 99.89 %), the recovery median up (600.4 s reads
  10 min 01 s), and the points read their weight (to one decimal, a tie
  rounded up) only at full marks;
- the excluded operators in a separate group headed by a note, with their
  values and no rank; with `--ranking-hide-excluded`, only their count, on the
  meta line;
- the operators not ranked yet, last, with their hours in the set out of the
  168 needed.

Every metric's header has a popover saying what it counts, its mapping and its
weight; the rank's gives the order and its ties, the name's how keys link to
operators, the score's the weights. On a phone, a card's rank and its values'
labels carry their columns' popovers. Every value has one going from the
chain's counts that `/api/ranking` publishes to its sub-score and its points,
rounded as the cells are, never better than the value (an uptime, a sub-score
to 3 decimals, points and a score down; a missed share and a recovery time up;
a weight to one decimal, a tie rounded up), and says why a value without a
sub-score has none.
A score's lists its four parts and the operators sharing its rank; the hidden
count's names the operators hidden; a not-ranked-yet operator's gives about the
day it would be ranked from if it stays in the set.

A line takes the place of the list when it is empty: every operator excluded
and hidden, or no block in the window. While the block history lacks what the
ranking needs, the view says how much of the chain is stored and, once the
fill rate is measured, at most how long the rest takes (counted to genesis),
or, once the blocks are stored, that it is reading the validator set's
changes; on a node that keeps no block back to the recovery span's start, the
lowest height it keeps; with the history ready and no ranking published yet,
that it is being computed. A read that fails or gets no answer within 10 s
says so in a line of its own, the ranking shown kept, and the next read tries
again.

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

The JSON endpoints below are CORS-open, for building external
badges/dashboards; `/ws` is a WebSocket open to any origin, and `/badge.svg` a
CORS-open SVG image.

| Endpoint         | Description                                                                                                                                                                                                          |
| ---------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `/api/status`    | Health summary (`status`/`chain`/`height`/`reason`/`time`) plus live network state, the last 100 blocks with signing status, and per-peer / per-validator column data; see [`/api/status`](#apistatus).              |
| `/api/stats`     | Aggregate stats: per-validator missed-block windows, cloud-provider and country aggregates, validator-set health, and the Nakamoto coefficient; see [`/api/stats`](#apistats).                                       |
| `/api/eta`       | Block ETAs: when a height is expected, which height a time reaches, or the scheduled halt; see [`/api/eta`](#apieta).                                                                                                |
| `/api/history`   | The block history: how far back it reaches, the blocks of a range of heights with the validators that did not sign each for it, daily vote delays, and the validator set changes; see [`/api/history`](#apihistory). |
| `/api/validator` | One validator's page data: status, keys, profile, timeline, daily series, recoveries, scoring-window summary and live figures; see [`/api/validator`](#apivalidator).                                                |
| `/api/ranking`   | The daily validator ranking: its state, and the latest day published with every operator's inputs, sub-scores and rank; 404 while `--ranking` is off; see [`/api/ranking`](#apiranking).                             |
| `/ws`            | The dashboard's live feed (unstable shape, following the dashboard): a WebSocket of JSON messages, open to any origin; see [`/ws`](#ws).                                                                             |
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
time. Without a query, the error of a scheduled halt's estimate also carries
`halt`.

```bash
curl -s 'http://localhost:8080/api/eta?height=1651200&since=2026-11-16T13:47:26Z'
curl -s 'http://localhost:8080/api/eta?time=<RFC 3339 time after the newest block>'
```

### `/api/status`

Each peer and validator has `name`, and where its IP resolves, `country`,
`city`, `lat`/`lon` and cloud `provider`; a validator's are its matched peer's.
Unknown fields are omitted; `lat` and `lon` come together, only when the IP is
located, and one of them can be 0 (the equator, the prime meridian). Each peer
also has `node_id`, `n_peers` (its own peer count, omitted when its RPC did not
give one or gives 0) and `reachable` (its RPC answered). Each validator also
has:

| Field                                            | Content                                                                                                |
| ------------------------------------------------ | ------------------------------------------------------------------------------------------------------ |
| `address`, `voting_power`, `spof`                | `spof`: it holds a third of the power or more; omitted for all while any validator's power is unknown. |
| `inactive`                                       | Present, true, while it is flagged down.                                                               |
| `missed_100`                                     | Blocks of the last 100 it missed: absent, or a vote for another block.                                 |
| `other_block_100`, `nil_100`                     | Of those, the votes for another block; and its nil votes, counted as signed.                           |
| `vote_delay_ms`                                  | The median delay of its precommits in the last 100 blocks; null without one.                           |
| `missed_24h`                                     | Blocks it missed in the last 24 h, from the block history.                                             |
| `missed_proposals_24h`, `expected_proposals_24h` | The proposals it missed in the last 24 h, of those it was expected to make.                            |

### `/api/stats`

`validators.nakamoto_coefficient` is the fewest validators whose voting power
exceeds a third of the set's; 0 when every voting power reads as a number and
none is above 0. It is null before the first snapshot, while the snapshot has
no validator set (its read failed, or every source is down), and while any
validator's voting power reads as no number.

Before the first snapshot the answer holds `time`; `validators` with every
count 0 and `nakamoto_coefficient`, `window_blocks` and `missed` null;
`providers` and `countries` null; and `peers` with both counts 0.

### `/api/history`

The query picks what comes back, and the queries combine. Times are RFC 3339
UTC to the millisecond.

| Query                 | Answer                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| none                  | `now` (the server's time the answer is as of, which the page reads ages by), `chain` and `filled`: `from` and `to` (the lowest and highest stored heights), `from_time` (the time of block `from`; left out while nothing is stored), `floor` (the oldest block the first `-rpc` serves, when it refused an older one), `complete` (`from` is 1, or the floor) and `rate` (heights added below `from` per second over the last 5 minutes; left out until measured, when the backfill has not moved in that window, and once complete).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `?from=H1&to=H2`      | The same, plus `blocks[]` and `valsets` for at most 1,000 heights. Heights outside `filled` are left out: a range the history does not hold gives an empty `blocks` and `valsets`. Each block has `height`, `time`, `proposer`, `round`, `txs`, `valset` (the hash of its validator set, a key of `valsets`) and the members that did not sign it for the block: `absent`, `nil` (a precommit for no block) and `other_block` (a precommit for another block). A block finalized after round 0 also has `rounds`: the expected proposer of rounds 0 to `round`, `""` where unknown. `flag` says why a block's proposer check failed, when it did (checked on those blocks and on every 1,000th). A block that opens a new validator set (its `valset` differs from the block below's) has `set_change` once its events are read: `source`, the block they were read from, and `events` (`type`, `operator`, `address` or `old_address` and `new_address`, `power`, left out for a removal or a rotation, whose events carry none, and `applied`); it is left out while that read is pending (retried every 10 minutes), when the source block holds no set event, and for good when the source block is below `floor`. `valsets` gives each set's members in set order: `address`, `pub_key` (base64) and `power`. |
| `?days=N`             | The same, plus `vote_delays` for the last N UTC days, today included (N from 1 to 31): `edges_ms`, the edges of the delay buckets in ms (one bucket more than edges: the first below the first edge, bucket i between edges i−1 and i, the last from the last edge up; a delay on an edge falls in the bucket farther from 0), and `days[]`, each with `day` (`YYYY-MM-DD`) and `validators[]`: `address`, `votes`, `mean_ms` and `buckets`. Days without a recorded vote are left out.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| `?changes=N&before=H` | The same, plus at most N (1 to 100) validator set changes below height H (the newest when left out), newest first, in `changes[]`, and `next_before`, the H that asks for the next page: set only when the page is full and a change, or the genesis set, lies below its last height. Each change has `height`, `time`, `genesis` (true for block 1's set, its members all added; left out otherwise), the members `added[]` and `removed[]` (`address`, `moniker` when gnockpit knows one, `power`), `repowered[]` (`address`, `moniker` when known, `before`, `after`), `rotated[]` (an applied `ValoperRotated` linking a removed and an added address: `operator`, `old_address`, `new_address`, `moniker` of the new one when known, `power`), `size_before`, `size_after`, `power_before` and `power_after` (total voting power), `events` (as in `set_change`) and `events_pending` (the events are not read yet).                                                                                                                                                                                                                                                                                                                                                                                          |

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

| Field                                            | Content                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| ------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `chain`, `now`, `address`, `operator`, `moniker` | `now` is the server's time the report is as of: the `summary` and `recoveries[]` windows end before its UTC day, while `days[]` includes that day's row when it has one. `address` is the one asked.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| `status`                                         | `in_set`; `since_height` and `since_time`: in the set, its latest join with no leave after (a key rotation is none), out of it, its latest leave; in the set with no join or leave stored, the stored history's first block with `since_before` true (it joined before); left out while the stored history contradicts the current set; `signing_address`, its key in the set; `power` and `share` of the set's total power, each null while unknown: `power` while its own reads as no number, `share` while any member's does. Left out until the node's current set is known.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `keys[]`                                         | Its signing keys: `address`, `from` (0: before the stored history) and `to` (left out: still its key). From the set events (a power change keeps a key's span), with the profile's signing key when no event names it; an unlinked key alone.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| `location`                                       | Its matched peer's `country`, `city` and `provider`; left out without one.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| `profile`                                        | Its valopers profile: `moniker`, `description`, `server_type`, `operator`, `signing_address`, `signing_pubkey`, and `keep_running` and `last_rotation_height` when the node answered `vm/qeval`. Left out without a profile.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `timeline[]`                                     | Newest first, each with `kind`, `height`, `time` and its kind's fields: `recovered` (`restart_height`, `duration_ms`; `height` is the first height signed again), `recovering` (`restart_height`, at the restart: not signed since), `outage` (`to`, `blocks`, `end_time`, `ongoing`), `power` (`before`, `after`), `rotated` (`old_address`, `new_address`), `joined`, `left` and `genesis` (`power`). A rotation reads as a `left` and a `joined` until its events are read.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| `vote_delay_edges_ms`                            | The vote-delay buckets' edges, as in `/api/history`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| `days[]`                                         | Each UTC day with a row, ascending: `day`, `in_set`, `signed`, `absent`, `nil`, `other_block`, `proposals_expected`, `proposals_missed`, `votes`, `vote_delay_mean_ms` and `vote_delay_buckets`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `recoveries[]`                                   | Its samples, ascending: a restart it was in the set and signing before, with fewer than 10 absences up to it. `restart_height` (b: block b+1 came over 2 minutes after block b), `restart_time` (block b+1's), `signed_at`, `duration_ms`, `pending` (block `signed_at`+1, which times it, is not stored yet; `signed_at` and `duration_ms` are 0) and `scored` (one of the last 3 of the 180 complete UTC days before today).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| `summary`                                        | Over the 30 complete UTC days before today (`first_day`, `last_day`): `in_set`, `signed`, `absent`, `nil`, `other_block` and `votes` summed over the days, `vote_delay_mean_ms` their mean, and `outages` (one counts once 10 of its heights are inside, recovery runs left out). `recovering` is the heights inside the window that belong to recovery runs (up to 24 h of block time after a restart's block b+1, decided per validator across its keys); uptime is `signed` ÷ (`in_set` − `recovering`). `longest_absence` is the longest run of absences inside the window, in heights, recovery runs left out, runs under 10 included. `in_set_ms` is the milliseconds of block time in the set over the window, 0 when the validator has no stored key or block in it. `proposals_expected` and `proposals_missed` count only the heights the validator signed, restart heights b left out, where each day's in `days[]` counts every height. `recovery`: the `samples` within the 180 complete UTC days before today, `median_ms` of the last 3 and `slowest_ms` of all. |
| `live`                                           | The validators table's: `vote_delay_ms` (null without one), `missed_100`, `nil_100`, `other_block_100`, `missed_24h`, `missed_proposals_24h` and `expected_proposals_24h`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |

Errors come as `{"error": "…"}`: 400 when `val` is not a g1 address, 404 for
one gnockpit knows nothing of, 500 when the database read fails.

```bash
curl -s 'http://localhost:8080/api/validator?val=g1u7g4cejjqsm44nha34hzpzu6df6a7egc9rpqw5'
```

### `/api/ranking`

`GET` only: any other method gets 405. With `--ranking` off the route answers
404 `{"error": "ranking disabled"}`. Otherwise it answers 200 with the ranking's
state and the latest day published. Times are RFC 3339 UTC. The formula and
the ranking's rules are in [Ranking](#ranking).

| Field           | Content                                                                                                                                                                                                                                                                                          |
| --------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `state`         | The ranking's readiness: `kind` is `filling` while the block history lacks what the ranking needs, `ready` once it holds it, `unavailable` when the node serves no block back to the start of the recovery span: the 180 complete UTC days before today, back to genesis on a younger chain.     |
| `hide_excluded` | `--ranking-hide-excluded`: whether readers should hide the excluded operators. The excluded are in `ranking` either way.                                                                                                                                                                         |
| `ranking`       | The latest day published for the chain, `null` until one is: a `ready` state can still have no day, while the valoper profiles are not whole (the last refresh missed a list page, or a listed operator has no profile read), the chain id is unknown, or today's first block is not stored yet. |

The `state` always carries `kind`, `stored_share`, `left_s` and
`events_pending`: 0, 0 and false outside `filling`. `floor` and `floor_time`
are left out unless `unavailable`. While `filling`, `stored_share` is the
stored heights' share of the chain's whole history (0 to 1), `left_s` the
seconds the fill needs to reach genesis at its rate, rounded up to whole
seconds (0 when unknown), and `events_pending` is true when the recovery span's
blocks (the 180 complete UTC days before today, back to genesis on a younger
chain) are stored and the events of a validator set change in it are not read
yet. While `unavailable`, `floor` is the lowest height the node serves and
`floor_time` its block's time.

The `ranking` holds:

| Field                                                  | Content                                                                                                                                              |
| ------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| `chain`, `day`                                         | The chain id and the UTC day (`YYYY-MM-DD`) the ranking is published for.                                                                            |
| `computed_at`                                          | When the ranking was computed.                                                                                                                       |
| `exclusions`                                           | The operator addresses of `--ranking-exclude`, sorted, each once, whether or not the validator set holds them.                                       |
| `formula`                                              | The formula version the ranking was computed with.                                                                                                   |
| `first_day`, `last_day`, `first_height`, `last_height` | The scoring window: its first and last UTC day, and its first and last block height.                                                                 |
| `proposals_disabled`                                   | The heights of the window whose proposals cannot be checked; one disables the proposals sub-score.                                                   |
| `entries`                                              | The operators in the validator set at `last_height`: the ranked in order, then the excluded by score, then those not ranked yet by hours in the set. |

Each entry holds:

| Field                                        | Content                                                                                                                                                                                                                                                                                                                                                                                           |
| -------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `operator`, `name`, `keys`                   | The operator address, or the signing address of a key no chain record links; its moniker; and its signing keys.                                                                                                                                                                                                                                                                                   |
| `status`                                     | `ranked`, `excluded` (scored, left out of the order) or `not_ranked_yet` (under 168 hours of block time in the set within the window).                                                                                                                                                                                                                                                            |
| `rank`                                       | The rank, 0 unless `ranked`; `tied_with` lists the operators sharing it.                                                                                                                                                                                                                                                                                                                          |
| `hours_in_set`, `score`                      | The block time in the set within the window, in hours, and the score, unrounded.                                                                                                                                                                                                                                                                                                                  |
| `uptime`, `outages`, `recovery`, `proposals` | The sub-scores, each with `sub` (0 to 1, `null` without data), `weight` (after redistribution; 0 when `sub` is `null`) and `points`.                                                                                                                                                                                                                                                              |
| `inputs`                                     | The raw figures: `in_set`, `recovering`, `signed`, `nil`, `absent` (empty slots) and `other_block` (precommits for another block), both absences, `outages` (each run with `first`, `last`, `first_time` and `end_time`), `longest_absence`, `recovery` (each sample with `restart_height`, `restart_time` and `duration_ms`), `recovery_median_ms`, `proposals_expected` and `proposals_missed`. |

```bash
curl -s http://localhost:8080/api/ranking
```

### `/ws`

The dashboard's own feed: its shape follows the dashboard, with no stability
promise. A client gets a `snapshot` message on connecting, once gnockpit has
polled its sources, then an `update` at each refresh (`-interval`). Both are
`{"type": …, "data": …}` with the same `data`:

| Field     | Content                                                                                                                                                                                                                                                                                              |
| --------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `time`    | When the sources were polled: `HH:MM:SS`, in the server's local time.                                                                                                                                                                                                                                |
| `status`  | The freshest source's `/status`; null while every source is down.                                                                                                                                                                                                                                    |
| `peers`   | Every source's peers, merged, located as in `/api/status`; `[]` when the sources see no peer, null while every source is down.                                                                                                                                                                       |
| `votes`   | The round in progress: `height`, `round`, `step`, `proposer` and each validator's votes and signing figures; null without a consensus state.                                                                                                                                                         |
| `checks`  | `apphash_last`, the freshest source's `val_address` and `val_pubkey`, `hide_sources`, and `system`: `node_time`, the server's time as the message is built (RFC 3339 UTC to the millisecond; the dashboard sets its clock by it and warns when the drift rounds to 3 s or more), and `genesis_time`. |
| `signing` | The signing of the last 100 blocks: `recent_blocks`, `validator_signing`, the active count and the margin; left out without it.                                                                                                                                                                      |

## Features

- Live WebSocket dashboard with real-time updates
- Multi-source consolidation — query several RPC endpoints; peers unioned & deduped, freshest source drives chain state
- Consensus monitoring — height, round, step, prevotes, precommits per validator
- Peer management — validator detection, health status, RPC reachability
- Network map — IP-geolocated peers, per-country list, Country + cloud-Provider columns
- Signing stats — per-validator blocks missed in the last 100 (absent or other block; nil votes shown apart, counted as signed), vote delay, proposals missed in the last 24 h, and missed-block counts over 1h/24h/7d/30d/total
- Validator page — a validator's status, valopers profile, uptime, proposals, recoveries after restarts, vote delay and timeline, with what the scoring counts (`/api/validator`)
- Validator ranking — the community validator operators ranked once a day by a fixed formula anyone can recompute from chain data, shown as the validators card's Ranking view (`--ranking`, `/api/ranking`)
- Block ETA — when a height will be produced (or which height at a given time), with 80%/95% ranges and drift from the original estimate; countdown cards for shared links and for the halt scheduled on chain
- HTTP API — CORS-open `/api/status`, `/api/stats`, `/api/eta`, `/api/history`, `/api/validator` and `/api/ranking` for external badges, dashboards and scripts
- Alerts — browser push (PWA) and external notifications (Discord, Telegram, …, each webhook its own alerts) for chain-stuck, missed blocks, validator set changes and coordinated halts, a per-device reminder before an ETA card's height, and a daily validator ranking for webhooks (`ranking`)
- Recent blocks — the last 100 blocks with signing visualization
- Block history — every block since genesis: proposer, round, the validators that did not sign it for the block and how, vote delays, expected round proposers and validator set changes, shown as a timeline (`/api/history`)

## Development

Requirements: Go, and Node 24 (`web/.nvmrc`), for `make test` and `make typecheck`.

`make test` runs the Go tests, which need no browser. A few check the page's scripts with `node` and fail, naming it, when it is not on `PATH`.

The binary serves the page's modules from `/js/` with `Cache-Control: no-cache` and a content ETag: a browser revalidates each on load, downloads only one that changed, and so runs the scripts of the deploy that served the page.

The page tests drive the dashboard in Chromium at desktop and tablet widths, and in WebKit as an iPhone and at desktop width, against the real server filled with chain data built in Go. They sit behind the `page` build tag. Install the Playwright driver and the two browsers once, at the version `go.mod` pins:

```bash
make install-browsers
```

On Linux it also installs the browsers' system packages (`--with-deps`), which needs root: run as another user, it uses `sudo`, or `su` when `sudo` is missing. The flag comes from `BROWSER_INSTALL_FLAGS`: on a distribution Playwright does not support, or with the packages installed otherwise, install the browsers alone:

```bash
make install-browsers BROWSER_INSTALL_FLAGS=
```

Then vet and run them:

```bash
make test-page
```

It runs the whole suite every time: Go's test cache knows nothing of the browsers, so it never reports a cached pass.

CI runs the suite on macOS (the `page` job, on `macos-26`). On Linux, the phone profile's tap tests fail: Linux's WebKit turns a tap into a click only where an element, not the document, listens to clicks, and the page listens on the document. An iPhone's WebKit and macOS's turn such a tap into a click.

A failed page test leaves a Playwright trace and a screenshot in `$PAGE_ARTIFACTS`, or in `gnockpit-page` under the system's temporary directory (`$TMPDIR`, else `/tmp`) when it is unset. Open a trace with the Playwright CLI:

```bash
go run github.com/mxschmitt/playwright-go/cmd/playwright show-trace "${PAGE_ARTIFACTS:-${TMPDIR:-/tmp}/gnockpit-page}/TestPageHead_desktop.zip"
```

### Type check

The dashboard's script is the ES modules in `web/js/`, with the typedefs of the server's answers in `web/types/`. `make typecheck` checks them with TypeScript 7.0.2, which `web/package-lock.json` pins, with every strict flag. It needs Node 24 (`web/.nvmrc`): `npm ci` refuses an older one, naming the version it requires. It installs `web/node_modules` itself (`npm ci`), prints nothing on success and exits non-zero on a finding. CI runs it as the `typecheck` job.

```bash
make typecheck
```

## License

MIT
