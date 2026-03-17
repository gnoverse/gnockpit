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

## Features

- Live WebSocket dashboard with real-time updates
- Consensus monitoring — height, round, step, prevotes, precommits per validator
- Peer management — validator detection, health status, RPC reachability
- Signing stats — per-validator sign rate and proposer speed over last 100 blocks
- Doctor diagnostics — deadlock detection, split-height alerts, gossip analysis
- Block history — recent blocks with signing visualization

## License

MIT
