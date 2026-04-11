package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"time"

	"database/sql"

	"github.com/gnoverse/gnockpit/hub"
	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/web"
	"github.com/gnoverse/gnockpit/web/push"
	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"
)

const (
	defaultRPC      = "http://127.0.0.1:26657"
	defaultInterval = 5 * time.Second
)

var (
	flagRPC        string
	flagVerbose    bool
	flagJSON       bool
	flagFollow     bool
	flagInterval   time.Duration
	flagTimeout    time.Duration
	flagWebPort       int
	flagWebAddr       string
	flagDBPath         string
	flagChainStuckSecs int
	flagMissedBlocksPct int
	flagDataDir    string
	flagGenesisPath string
	flagNamesPath   string
	flagService     string
	flagContainer   string
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "gnockpit",
		Short: "gno.land validator node monitoring tool",
	}

	root.PersistentFlags().StringVar(&flagRPC, "rpc", defaultRPC, "RPC endpoint URL")
	root.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "log HTTP requests")
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "JSON output")
	root.PersistentFlags().BoolVarP(&flagFollow, "follow", "f", false, "live updating mode")
	root.PersistentFlags().DurationVar(&flagInterval, "interval", defaultInterval, "refresh interval for follow mode")
	root.PersistentFlags().StringVar(&flagDataDir, "data-dir", "", "gnoland data directory (auto-detected if empty)")
	root.PersistentFlags().StringVar(&flagGenesisPath, "genesis", "", "path to genesis.json (auto-detected if empty)")
	root.PersistentFlags().StringVar(&flagNamesPath, "names", "/tmp/gnockpit-names.json", "path to persistent name registry")
	root.PersistentFlags().StringVar(&flagService, "service", "", "systemd service name (auto-detected from chain-id)")
	root.PersistentFlags().StringVar(&flagContainer, "container", "", "docker container name (mutually exclusive with -service)")

	root.AddCommand(statusCmd())
	root.AddCommand(peersCmd())
	root.AddCommand(networkCmd())
	root.AddCommand(consensusCmd())
	root.AddCommand(votesCmd())
	root.AddCommand(checkCmd())
	root.AddCommand(infoCmd())
	root.AddCommand(webCmd())
	root.AddCommand(serverCmd())
	root.AddCommand(probeCmd())
	root.AddCommand(tokenCmd())

	return root
}

// rpcPort extracts the port from the --rpc URL for peer queries.
func rpcPort() string {
	// Parse port from flagRPC (e.g., "http://127.0.0.1:26661" → "26661")
	for i := len(flagRPC) - 1; i >= 0; i-- {
		if flagRPC[i] == ':' {
			port := flagRPC[i+1:]
			// Strip trailing path
			for j := 0; j < len(port); j++ {
				if port[j] == '/' {
					return port[:j]
				}
			}
			return port
		}
	}
	return "26657"
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

func runLoop(ctx context.Context, fn func(ctx context.Context)) {
	if !flagFollow {
		fn(ctx)
		return
	}
	ticker := time.NewTicker(flagInterval)
	defer ticker.Stop()
	fn(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fmt.Print("\033[2J\033[H")
			fn(ctx)
		}
	}
}

func printJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

// --- status ---

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Quick node status (block height, sync, time)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := newContext()
			defer cancel()
			c := newClient()
			runLoop(ctx, func(ctx context.Context) {
				status, err := c.GetStatus(ctx)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					return
				}
				if flagJSON {
					printJSON(status)
					return
				}
				fmt.Printf("block:     %s\n", status.SyncInfo.LatestBlockHeight)
				fmt.Printf("time:      %s\n", status.SyncInfo.LatestBlockTime)
				fmt.Printf("catching:  %v\n", status.SyncInfo.CatchingUp)
				fmt.Printf("moniker:   %s\n", status.NodeInfo.Moniker)
				fmt.Printf("network:   %s\n", status.NodeInfo.Network)
				if flagFollow {
					fmt.Printf("\nupdated:   %s\n", time.Now().Format("15:04:05"))
				}
			})
			return nil
		},
	}
}

// --- peers ---

func peersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peers",
		Short: "Connected peers list with validator status",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := newContext()
			defer cancel()
			c := newClient()
			runLoop(ctx, func(ctx context.Context) {
				peers, err := c.GetNetInfo(ctx)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					return
				}
				validators, _ := c.GetValidators(ctx)
				enriched := node.QueryAllPeers(ctx, peers, rpcPort(), flagTimeout, false, validators, c.LogFn, c.Names)
				sort.Slice(enriched, func(i, j int) bool {
					return enriched[i].Moniker < enriched[j].Moniker
				})

				if flagJSON {
					printJSON(enriched)
					return
				}

				valCount := 0
				for _, p := range enriched {
					if p.Role == "val" || p.Role == "val?" {
						valCount++
					}
				}

				fmt.Printf("peers: %d (%d validators)\n\n", len(enriched), valCount)
				fmt.Printf("%-22s %-16s %-6s  %s\n", "MONIKER", "IP", "ROLE", "HEIGHT")
				fmt.Printf("%-22s %-16s %-6s  %s\n", "-------", "--", "----", "------")
				for _, p := range enriched {
					height := p.Height
					if height == "" {
						height = "-"
					}
					role := p.Role
					if role == "" {
						role = "full"
					}
					fmt.Printf("%-22s %-16s %-6s  %s\n", p.Moniker, p.RemoteIP, role, height)
				}
				if flagFollow {
					fmt.Printf("\nupdated: %s\n", time.Now().Format("15:04:05"))
				}
			})
			return nil
		},
	}
	cmd.Flags().DurationVar(&flagTimeout, "timeout", 3*time.Second, "per-peer query timeout")
	return cmd
}

// --- network ---

func networkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "network",
		Short: "Peers with validator status and block heights",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := newContext()
			defer cancel()
			c := newClient()
			runLoop(ctx, func(ctx context.Context) {
				status, err := c.GetStatus(ctx)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					return
				}
				peers, err := c.GetNetInfo(ctx)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					return
				}
				validators, _ := c.GetValidators(ctx)
				enriched := node.QueryAllPeers(ctx, peers, rpcPort(), flagTimeout, false, validators, c.LogFn, c.Names)
				sort.Slice(enriched, func(i, j int) bool {
					return enriched[i].Moniker < enriched[j].Moniker
				})

				if flagJSON {
					printJSON(enriched)
					return
				}

				fmt.Printf("=== network . %s ===\n\n", status.NodeInfo.Network)
				fmt.Printf("us: block %s\n\n", status.SyncInfo.LatestBlockHeight)
				fmt.Printf("%-22s %-16s %-6s  %s\n", "MONIKER", "IP", "ROLE", "HEIGHT")
				fmt.Printf("%-22s %-16s %-6s  %s\n", "-------", "--", "----", "------")
				for _, p := range enriched {
					role := p.Role
					if role == "" {
						role = "full"
					}
					height := p.Height
					if height == "" {
						height = "timeout"
					}
					fmt.Printf("%-22s %-16s %-6s  %s\n", p.Moniker, p.RemoteIP, role, height)
				}
				if flagFollow {
					fmt.Printf("\nupdated: %s\n", time.Now().Format("15:04:05"))
				}
			})
			return nil
		},
	}
	cmd.Flags().DurationVar(&flagTimeout, "timeout", 3*time.Second, "per-peer query timeout")
	return cmd
}

// --- consensus ---

func consensusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "consensus",
		Short: "Full consensus view with peer heights and rounds",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := newContext()
			defer cancel()
			c := newClient()
			runLoop(ctx, func(ctx context.Context) {
				cs, _, err := c.GetConsensusState(ctx)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					return
				}
				status, err := c.GetStatus(ctx)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					return
				}
				peers, err := c.GetNetInfo(ctx)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					return
				}
				validators, _ := c.GetValidators(ctx)
				enriched := node.QueryAllPeers(ctx, peers, rpcPort(), flagTimeout, true, validators, c.LogFn, c.Names)
				sort.Slice(enriched, func(i, j int) bool {
					return enriched[i].Moniker < enriched[j].Moniker
				})

				valCount := len(validators)
				needed := (valCount*2/3) + 1

				if flagJSON {
					printJSON(map[string]interface{}{
						"height":     cs.Height,
						"round":      cs.Round,
						"step":       cs.Step,
						"proposer":   c.Names.NameWithUs(cs.Proposer),
						"validators": valCount,
						"needed":     needed,
						"our_height": status.SyncInfo.LatestBlockHeight,
						"peers":      enriched,
					})
					return
				}

				fmt.Printf("=== consensus . %s ===\n\n", status.NodeInfo.Network)
				fmt.Printf("height: %s  round: %s  step: %s\n", cs.Height, cs.Round, cs.Step)
				fmt.Printf("proposer: %s\n", c.Names.NameWithUs(cs.Proposer))
				fmt.Printf("validators: %d (need %d for consensus)\n\n", valCount, needed)

				moniker := status.NodeInfo.Moniker
				if moniker == "" {
					moniker = "(local)"
				}
				fmt.Printf("%-22s %-16s %-6s %7s  %s\n", "MONIKER", "IP", "ROLE", "HEIGHT", "ROUND")
				fmt.Printf("%-22s %-16s %-6s %7s  %s\n", "-------", "--", "----", "------", "-----")
				fmt.Printf("%-22s %-16s %-6s %7s  %s\n", moniker+" (us)", "(local)", "val", status.SyncInfo.LatestBlockHeight, cs.Round+"/"+cs.Step)
				for _, p := range enriched {
					role := p.Role
					if role == "" {
						role = "full"
					}
					height := p.Height
					if height == "" {
						height = "timeout"
					}
					round := "-"
					if p.Round != "" && p.Step != "" && p.Round != "-" {
						round = p.Round + "/" + p.Step
					}
					fmt.Printf("%-22s %-16s %-6s %7s  %s\n", p.Moniker, p.RemoteIP, role, height, round)
				}
				if flagFollow {
					fmt.Printf("\nupdated: %s\n", time.Now().Format("15:04:05"))
				}
			})
			return nil
		},
	}
	cmd.Flags().DurationVar(&flagTimeout, "timeout", 3*time.Second, "per-peer query timeout")
	return cmd
}

// --- votes ---

func votesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "votes",
		Short: "Quick vote check (prevote/precommit per validator)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := newContext()
			defer cancel()
			c := newClient()
			runLoop(ctx, func(ctx context.Context) {
				cs, _, err := c.GetConsensusState(ctx)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					return
				}

				report := node.VotesReport{
					Height:     cs.Height,
					Round:      cs.Round,
					Step:       cs.Step,
					Proposer:   c.Names.NameWithUs(cs.Proposer),
					Validators: cs.Votes,
					Timestamp:  time.Now(),
				}

				if flagJSON {
					printJSON(report)
					return
				}

				fmt.Printf("h=%s r=%s s=%s  proposer=%s\n\n", report.Height, report.Round, report.Step, report.Proposer)
				fmt.Printf("  %-4s %-22s %-44s %8s %10s\n", "#", "NAME", "ADDRESS", "PREVOTE", "PRECOMMIT")
				fmt.Printf("  %-4s %-22s %-44s %8s %10s\n", "-", "----", "-------", "-------", "---------")
				for _, v := range report.Validators {
					pv := colorBool(v.Prevoted)
					pc := colorBool(v.Precommit)
					fmt.Printf("  [%d]  %-22s %-44s %s %s\n", v.Index, v.Name, v.Address, pv, pc)
				}
				if flagFollow {
					fmt.Printf("\nupdated: %s\n", time.Now().Format("15:04:05"))
				}
			})
			return nil
		},
	}
}

func colorBool(v bool) string {
	if v {
		return "\033[32m    YES\033[0m"
	}
	return "\033[31m     no\033[0m"
}

// --- check ---

func checkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Verify genesis hash and apphash at block 2",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := newContext()
			defer cancel()
			c := newClient()

			var checks []node.CheckResult

			appHash, err := c.GetBlockAppHash(ctx, 2)
			if err != nil || appHash == "" || appHash == "null" {
				checks = append(checks, node.CheckResult{
					Name:    "apphash",
					Status:  "n/a",
					Message: "block 2 not yet reached",
				})
			} else {
				checks = append(checks, node.CheckResult{
					Name:    "apphash",
					Status:  "ok",
					Got:     appHash,
					Message: fmt.Sprintf("block 2 app_hash: %s", appHash),
				})
			}

			if flagJSON {
				printJSON(checks)
				return nil
			}

			for _, ch := range checks {
				status := ch.Status
				switch status {
				case "ok":
					status = "\033[32mok\033[0m"
				case "mismatch":
					status = "\033[31mMISMATCH\033[0m"
				case "n/a":
					status = "\033[33mn/a\033[0m"
				}
				fmt.Printf("%-10s [%s] %s\n", ch.Name, status, ch.Message)
			}
			return nil
		},
	}
}

// --- info ---

func infoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Full node report (identity + status + peers + checks)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := newContext()
			defer cancel()
			c := newClient()

			status, err := c.GetStatus(ctx)
			if err != nil {
				fmt.Printf("node not reachable: %v\n", err)
				return nil
			}

			if flagJSON {
				return infoJSON(ctx, c, status)
			}

			fmt.Printf("=== gnockpit . %s ===\n\n", status.NodeInfo.Moniker)

			fmt.Printf("--- identity ---\n")
			fmt.Printf("chain:    %s\n", status.NodeInfo.Network)
			fmt.Printf("moniker:  %s\n", status.NodeInfo.Moniker)
			fmt.Printf("address:  %s\n", status.ValidatorInfo.Address)
			fmt.Printf("p2p:      %s\n", status.NodeInfo.NetAddress)
			fmt.Println()

			fmt.Printf("--- status ---\n")
			fmt.Printf("block:    %s\n", status.SyncInfo.LatestBlockHeight)
			fmt.Printf("catching: %v\n", status.SyncInfo.CatchingUp)
			fmt.Printf("time:     %s\n", status.SyncInfo.LatestBlockTime)
			fmt.Println()

			fmt.Printf("--- peers ---\n")
			peers, err := c.GetNetInfo(ctx)
			if err == nil {
				sort.Slice(peers, func(i, j int) bool {
					return peers[i].Moniker < peers[j].Moniker
				})
				fmt.Printf("count:    %d\n", len(peers))
				for _, p := range peers {
					fmt.Printf("  %s (%s)\n", p.Moniker, p.RemoteIP)
				}
			}
			fmt.Println()

			fmt.Printf("--- checks ---\n")
			appHash, err := c.GetBlockAppHash(ctx, 2)
			if err != nil || appHash == "" || appHash == "null" {
				fmt.Printf("apphash:  \033[33mn/a\033[0m (block 2 not yet reached)\n")
			} else {
				fmt.Printf("apphash:  %s\n", appHash)
			}

			return nil
		},
	}
}

func infoJSON(ctx context.Context, c *node.Client, status *node.Status) error {
	snap := make(map[string]interface{})
	snap["chain_id"] = status.NodeInfo.Network
	snap["moniker"] = status.NodeInfo.Moniker
	snap["val_address"] = status.ValidatorInfo.Address
	snap["status"] = status

	peers, err := c.GetNetInfo(ctx)
	if err == nil {
		sort.Slice(peers, func(i, j int) bool { return peers[i].Moniker < peers[j].Moniker })
		snap["peers"] = peers
	}

	appHash, err := c.GetBlockAppHash(ctx, 2)
	if err == nil && appHash != "" && appHash != "null" {
		snap["apphash_b2"] = appHash
	}

	snap["timestamp"] = time.Now()
	printJSON(snap)
	return nil
}

// --- web ---

func webCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Start web dashboard with live updates",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := newContext()
			defer cancel()
			c := newClient()
			addr := fmt.Sprintf("%s:%d", flagWebAddr, flagWebPort)
			srv := web.NewServer(c, addr, flagInterval)
			srv.DataDir = findDataDir()
			srv.GenesisPath = findGenesis()
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
			srv.PushManager = pushMgr

			return srv.Run(ctx)
		},
	}
	cmd.Flags().IntVar(&flagWebPort, "port", 8080, "web server port")
	cmd.Flags().StringVar(&flagWebAddr, "addr", "0.0.0.0", "web server bind address")
	cmd.Flags().StringVar(&flagDBPath, "db-path", "/tmp/gnockpit.db",
		"SQLite database path for push notifications; use a persistent path in production so VAPID keys survive reboots")
	cmd.Flags().IntVar(&flagChainStuckSecs, "chain-stuck-secs", 30,
		"seconds without a new block before the chain-stuck alert fires")
	cmd.Flags().IntVar(&flagMissedBlocksPct, "missed-blocks-pct", 5,
		"percentage of blocks missed in the signing window before the validator-missing-blocks alert fires (e.g. 5 = 5%)")
	return cmd
}

// --- server (hub) ---

var (
	flagServerPort     int
	flagServerAddr     string
	flagServerDBPath   string
	flagProbeRateLimit int
)

func serverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Start cluster hub server (aggregates probe data)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := newContext()
			defer cancel()

			db, err := sql.Open("sqlite", flagServerDBPath)
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)

			tokens, err := hub.NewTokenStore(db)
			if err != nil {
				return err
			}

			addr := fmt.Sprintf("%s:%d", flagServerAddr, flagServerPort)
			h := hub.New(tokens, addr)
			h.MaxRate = flagProbeRateLimit

			return h.Run(ctx, web.Content)
		},
	}
	cmd.Flags().IntVar(&flagServerPort, "port", 8080, "server port")
	cmd.Flags().StringVar(&flagServerAddr, "addr", "0.0.0.0", "server bind address")
	cmd.Flags().StringVar(&flagServerDBPath, "db-path", "/tmp/gnockpit-hub.db", "SQLite database for tokens and probe metadata")
	cmd.Flags().IntVar(&flagProbeRateLimit, "probe-rate-limit", 1, "max snapshots/sec per probe (0=unlimited)")
	return cmd
}

// --- probe ---

var (
	flagProbeServer string
	flagProbeToken  string
	flagProbePort   int
)

func probeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "probe",
		Short: "Run as a probe, pushing snapshots to a hub server",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := newContext()
			defer cancel()
			c := newClient()

			// Build a snapshot fetcher reusing existing web.Server logic
			srv := web.NewServer(c, "", flagInterval)
			srv.DataDir = findDataDir()
			srv.GenesisPath = findGenesis()
			if flagService != "" && flagContainer != "" {
				return fmt.Errorf("-service and -container are mutually exclusive")
			}
			srv.Backend = buildBackend(flagService, flagContainer, srv)

			probe := &hub.ProbeClient{
				ServerURL: flagProbeServer + "/ws/probe",
				Token:     flagProbeToken,
				RPC:       c,
				Interval:  flagInterval,
			}
			return probe.Run(ctx, srv.FetchSnapshot)
		},
	}
	cmd.Flags().StringVar(&flagProbeServer, "server", "", "hub server URL (e.g. wss://gnockpit.example.com)")
	cmd.Flags().StringVar(&flagProbeToken, "token", "", "bearer token for hub authentication")
	cmd.MarkFlagRequired("server")
	cmd.MarkFlagRequired("token")
	cmd.Flags().IntVar(&flagProbePort, "port", 0, "optional local dashboard port (0=disabled)")
	return cmd
}

// --- token ---

func tokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage probe authentication tokens",
	}
	cmd.PersistentFlags().StringVar(&flagServerDBPath, "db-path", "/tmp/gnockpit-hub.db", "SQLite database path")

	cmd.AddCommand(&cobra.Command{
		Use:   "create [name]",
		Short: "Create a new probe token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := sql.Open("sqlite", flagServerDBPath)
			if err != nil {
				return err
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			store, err := hub.NewTokenStore(db)
			if err != nil {
				return err
			}
			token, err := store.Create(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Token for %q:\n%s\n\nSave this — it won't be shown again.\n", args[0], token)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all probe tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := sql.Open("sqlite", flagServerDBPath)
			if err != nil {
				return err
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			store, err := hub.NewTokenStore(db)
			if err != nil {
				return err
			}
			tokens, err := store.List()
			if err != nil {
				return err
			}
			if flagJSON {
				printJSON(tokens)
				return nil
			}
			fmt.Printf("%-4s %-20s %-20s %-20s %-20s\n", "ID", "NAME", "CREATED", "REVOKED", "LAST SEEN")
			for _, t := range tokens {
				revoked := "-"
				if t.RevokedAt != "" {
					revoked = t.RevokedAt
				}
				lastSeen := "-"
				if t.LastSeen != "" {
					lastSeen = t.LastSeen
				}
				fmt.Printf("%-4d %-20s %-20s %-20s %-20s\n", t.ID, t.Name, t.CreatedAt, revoked, lastSeen)
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "revoke [name]",
		Short: "Revoke a probe token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := sql.Open("sqlite", flagServerDBPath)
			if err != nil {
				return err
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			store, err := hub.NewTokenStore(db)
			if err != nil {
				return err
			}
			if err := store.Revoke(args[0]); err != nil {
				return err
			}
			fmt.Printf("Token %q revoked.\n", args[0])
			return nil
		},
	})

	return cmd
}
