package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/web"
	"github.com/gnoverse/gnockpit/web/push"
)

const (
	defaultRPC      = "http://127.0.0.1:26657"
	defaultInterval = 5 * time.Second
)

var (
	flagRPC             string
	flagVerbose         bool
	flagInterval        time.Duration
	flagWebPort         int
	flagWebAddr         string
	flagDBPath          string
	flagChainStuckSecs  int
	flagMissedBlocksPct int
	flagDataDir         string
	flagGenesisPath     string
	flagNamesPath       string
	flagService         string
	flagContainer       string
	flagNotifyURLs      stringSlice
	flagPublicURL       string
)

// stringSlice is a flag.Value that accumulates one entry per occurrence,
// so -notify can be passed multiple times.
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ",") }

func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func usage() {
	fmt.Fprint(flag.CommandLine.Output(), `gnockpit — live web dashboard for a single gno.land validator node.

It reads from one node through several sources:
  -rpc        the node's Tendermint RPC (required): block height, sync state,
              validator set, consensus votes, peers, and signing stats. This is
              most of the dashboard.
  -genesis    genesis.json (auto-detected): validator names and genesis time.
  -data-dir   the node's data directory (auto-detected): on-disk DB sizes, shown
              on the "node down" panel.
  -service /  how to reach the node's process — a systemd unit or a docker
  -container  container. Streams its logs to refresh the dashboard the instant
              consensus moves, and reads node uptime and memory. Optional.

Usage:
  gnockpit [flags]

Flags:
`)
	flag.PrintDefaults()
}

func main() {
	flag.Usage = usage

	flag.StringVar(&flagRPC, "rpc", defaultRPC, "Tendermint RPC endpoint of the gno.land node to monitor")
	flag.BoolVar(&flagVerbose, "verbose", false, "log HTTP requests to the node")
	flag.BoolVar(&flagVerbose, "v", false, "log HTTP requests to the node (shorthand)")
	flag.DurationVar(&flagInterval, "interval", defaultInterval, "dashboard refresh interval")
	flag.StringVar(&flagGenesisPath, "genesis", "", "path to genesis.json (auto-detected); provides validator names and genesis time")
	flag.StringVar(&flagDataDir, "data-dir", "", "gnoland data directory (auto-detected); used for on-disk DB sizes on the node-down panel")
	flag.StringVar(&flagNamesPath, "names", "/tmp/gnockpit-names.json", "path to the persistent validator-name registry")
	flag.StringVar(&flagService, "service", "", "systemd service name (auto-detected from chain-id); streams logs for live refresh and reads node uptime/memory")
	flag.StringVar(&flagContainer, "container", "", "docker container name (mutually exclusive with -service); streams logs for live refresh and reads node uptime/memory")
	flag.StringVar(&flagWebAddr, "addr", "0.0.0.0", "web server bind address")
	flag.IntVar(&flagWebPort, "port", 8080, "web server port")
	flag.StringVar(&flagDBPath, "db-path", "/tmp/gnockpit.db", "SQLite path for web-push subscriptions and VAPID keys; use a persistent path so keys survive reboots")
	flag.IntVar(&flagChainStuckSecs, "chain-stuck-secs", 30, "seconds without a new block before the chain-stuck alert fires")
	flag.IntVar(&flagMissedBlocksPct, "missed-blocks-pct", 5, "percent of blocks missed in the signing window before the validator-missing-blocks alert fires")
	flag.Var(&flagNotifyURLs, "notify", "Shoutrrr notification URL, repeatable (e.g. discord://token@id)")
	flag.StringVar(&flagPublicURL, "public-url", "", "public URL of this gnockpit instance, appended to external notification messages")

	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument: %s\n\n", flag.Arg(0))
		flag.Usage()
		os.Exit(2)
	}

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := newContext()
	defer cancel()

	c := newClient()
	addr := fmt.Sprintf("%s:%d", flagWebAddr, flagWebPort)
	srv := web.NewServer(c, addr, flagInterval)
	srv.DataDir = findDataDir()

	if flagService != "" && flagContainer != "" {
		return fmt.Errorf("-service and -container are mutually exclusive")
	}
	srv.Backend = buildBackend(flagService, flagContainer, srv)

	db, err := push.OpenDB(flagDBPath)
	if err != nil {
		return fmt.Errorf("open push database: %w", err)
	}
	defer db.Close()

	pushMgr, err := push.NewManager(db, flagChainStuckSecs, flagMissedBlocksPct)
	if err != nil {
		return fmt.Errorf("init push manager: %w", err)
	}
	if len(flagNotifyURLs) > 0 {
		if err := pushMgr.SetNotifyURLs(flagNotifyURLs); err != nil {
			return fmt.Errorf("invalid notify URL: %w", err)
		}
	}
	if flagPublicURL != "" {
		pushMgr.SetPublicURL(flagPublicURL)
	}
	srv.MissedBlocksPct = flagMissedBlocksPct
	srv.PushManager = pushMgr

	return srv.Run(ctx)
}

// buildBackend constructs the appropriate RuntimeBackend based on CLI flags.
// If neither flag is set, a SystemdBackend with lazy auto-detection from the chain-id is used.
func buildBackend(service, container string, srv *web.Server) web.RuntimeBackend {
	if container != "" {
		return &web.DockerBackend{ContainerName: container}
	}
	// SystemdBackend: static name if -service given, lazy from chain-id otherwise.
	var nameFn func() string
	if service == "" {
		nameFn = func() string {
			snap := srv.GetSnapshot()
			if snap != nil && snap.Status != nil && snap.Status.NodeInfo.Network != "" {
				return snap.Status.NodeInfo.Network + ".service"
			}
			return ""
		}
	}
	return web.NewSystemdBackend(service, nameFn)
}

// findGenesis tries common genesis.json locations relative to data-dir or cwd.
func findGenesis() string {
	if flagGenesisPath != "" {
		return flagGenesisPath
	}
	candidates := []string{}
	if flagDataDir != "" {
		candidates = append(candidates, flagDataDir+"/config/genesis.json")
	}
	// Relative to cwd
	candidates = append(candidates,
		"gnoland-data/config/genesis.json",
		"../gnoland-data/config/genesis.json",
	)
	// Walk up looking for */gnoland-data/config/genesis.json
	entries, _ := os.ReadDir(".")
	for _, e := range entries {
		if e.IsDir() {
			candidates = append(candidates, e.Name()+"/gnoland-data/config/genesis.json")
		}
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// findDataDir returns the data directory, auto-detecting if needed.
func findDataDir() string {
	if flagDataDir != "" {
		return flagDataDir
	}
	// Try to find from genesis path
	g := findGenesis()
	if g != "" {
		// genesis is at <datadir>/config/genesis.json
		for i := len(g) - 1; i >= 0; i-- {
			if g[i] == '/' {
				dir := g[:i] // .../config
				for j := len(dir) - 1; j >= 0; j-- {
					if dir[j] == '/' {
						return dir[:j] // .../gnoland-data
					}
				}
			}
		}
	}
	return ""
}

func newClient() *node.Client {
	c := node.NewClient(flagRPC, 10*time.Second)
	c.Names = node.NewNameRegistryWithPersist(flagNamesPath)
	if g := findGenesis(); g != "" {
		c.Names.SeedFromGenesis(g)
	}
	if flagVerbose {
		c.LogFn = func(method, url string, status int, dur time.Duration, err error) {
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s %s -> %v (%dms)\n", method, url, err, dur.Milliseconds())
			} else {
				fmt.Fprintf(os.Stderr, "%s %s -> %d (%dms)\n", method, url, status, dur.Milliseconds())
			}
		}
	}
	return c
}

func newContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}
