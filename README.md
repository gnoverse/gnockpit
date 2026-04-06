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
| `--notify` | (none) | Shoutrrr notification URL (repeatable); see External Notifications |
| `--missed-blocks-pct` | `5` | Percentage of missed blocks before the validator alert fires |

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

## External Notifications

gnockpit can forward alerts to Discord, Telegram, Signal, Slack, and other services via [Shoutrrr](https://github.com/containrrr/shoutrrr) notification URLs. Use the `--notify` flag (repeatable) to add destinations:

```bash
gnockpit web \
  --notify "discord://token@webhookid" \
  --notify "telegram://token@telegram?chats=@channel" \
  --notify "generic://signal-api:8080/v2/send?template=json&disabletls=yes&$number=%2B1234567890&$recipient=%2B0987654321"
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
| Signal (via signal-cli-rest-api) | `generic://signal-api:8080/v2/send?template=json&disabletls=yes&$number=%2Bsender&$recipient=%2Btarget` |

See the [Shoutrrr documentation](https://containrrr.dev/shoutrrr/latest/) for the full list of supported services and URL formats.

**Signal:** Requires a [signal-cli-rest-api](https://github.com/bbernhard/signal-cli-rest-api) sidecar. See [gnockpit-compose](https://github.com/gnoverse/gnockpit-compose) for a ready-made Docker Compose setup.

## Docker

The image is available on GitHub Container Registry:

```bash
docker pull ghcr.io/gnoverse/gnockpit:latest
```

### Volumes

| Mount | Required | Description |
|-------|----------|-------------|
| `/var/run/docker.sock` | Yes (container mode) | Docker socket to access gnoland container logs via `docker logs` / `docker inspect` |
| Genesis / data directory | Recommended | Mount gnoland's data directory so gnockpit can read `genesis.json` and compute disk usage |
| Persistent data directory | Recommended | For `-names` and `-db-path` — defaults write to `/tmp` which is lost on restart |

### Example

```bash
docker run -d \
  -p 8080:8080 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v /path/to/gnoland-data:/gnoland-data:ro \
  -v gnockpit-data:/data \
  ghcr.io/gnoverse/gnockpit:latest \
  web \
  -container gnoland \
  -rpc http://gnoland:26657 \
  -data-dir /gnoland-data \
  -names /data/names.json \
  -db-path /data/gnockpit.db
```

### Notes

- The `-container` flag is required — the default systemd backend does not work inside containers.
- The image includes the Docker CLI so `docker logs` / `docker inspect` work when the socket is mounted.
- `/proc` access (system stats, process memory) works natively in Linux containers. If monitoring a gnoland container running on the same host, memory stats require `--pid=host` or will reflect gnockpit's own container.
- The `-rpc` URL should use the gnoland container hostname if both are on the same Docker network (e.g., `http://gnoland:26657`).

## Features

- Live WebSocket dashboard with real-time updates
- Consensus monitoring — height, round, step, prevotes, precommits per validator
- Peer management — validator detection, health status, RPC reachability
- Signing stats — per-validator sign rate and proposer speed over last 100 blocks
- Doctor diagnostics — deadlock detection, split-height alerts, gossip analysis
- Block history — recent blocks with signing visualization

## License

MIT
