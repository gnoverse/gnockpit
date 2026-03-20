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

## Features

- Live WebSocket dashboard with real-time updates
- Consensus monitoring — height, round, step, prevotes, precommits per validator
- Peer management — validator detection, health status, RPC reachability
- Signing stats — per-validator sign rate and proposer speed over last 100 blocks
- Doctor diagnostics — deadlock detection, split-height alerts, gossip analysis
- Block history — recent blocks with signing visualization

## License

MIT
