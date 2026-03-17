# gnockpit

Real-time gno.land validator node monitoring dashboard.

![gnockpit](https://img.shields.io/badge/gno.land-validator_cockpit-blue)

## Features

- **Live dashboard** with WebSocket real-time updates
- **Consensus monitoring** — height, round, step, prevotes, precommits per validator
- **Peer management** — validator detection, health status, RPC reachability
- **Signing stats** — per-validator sign rate and proposer speed over last 100 blocks
- **Doctor diagnostics** — deadlock detection, split-height alerts, gossip analysis
- **Block history** — recent blocks with signing visualization
- **CLI tools** — status, peers, network, consensus, votes commands

## Install

```bash
go install github.com/gnoverse/gnockpit@latest
```

## Usage

### Web Dashboard

```bash
# Basic — connects to local gnoland RPC on default port
gnockpit web

# Full options
gnockpit web \
  --rpc http://127.0.0.1:26657 \
  --data-dir /path/to/gnoland-data \
  --genesis /path/to/genesis.json \
  --service gnoland.service \
  --port 8080 \
  --addr 0.0.0.0 \
  --interval 5s
```

### CLI Commands

```bash
gnockpit status              # Quick node status
gnockpit peers               # Connected peers with validator detection
gnockpit network             # Full network view
gnockpit consensus           # Consensus state with peer heights
gnockpit votes               # Prevote/precommit per validator
gnockpit check               # Verify genesis and app hash
gnockpit info                # Full node report
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--rpc` | `http://127.0.0.1:26657` | RPC endpoint URL |
| `--data-dir` | auto-detect | gnoland data directory |
| `--genesis` | auto-detect | Path to genesis.json |
| `--service` | auto-detect | Systemd service name |
| `--names` | `/tmp/gnockpit-names.json` | Persistent name registry |
| `--port` | `8080` | Web server port |
| `--interval` | `5s` | Dashboard refresh interval |
| `-v` | false | Verbose HTTP logging |
| `-f` | false | Follow mode (CLI) |
| `--json` | false | JSON output (CLI) |

## Auto-detection

gnockpit auto-detects most configuration:
- **Chain ID** — from RPC `/status`
- **Service name** — from chain ID (e.g., `gnoland1` → `gnoland1.service`)
- **Genesis path** — searches `--data-dir/config/genesis.json` and nearby directories
- **Validator names** — discovered from peer RPC queries and genesis file
- **Gno source** — from `GNOROOT` env or common paths

## License

MIT
