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
	flagNamesPath       string
	flagNotifyURLs      stringSlice
	flagPublicURL       string
	flagGeoIPPath       string
	flagLinks           stringSlice
	flagStatusLinks     stringSlice
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
	flag.StringVar(&flagNamesPath, "names", "/tmp/gnockpit-names.json", "path to the persistent validator-name registry")
	flag.StringVar(&flagWebAddr, "addr", "0.0.0.0", "web server bind address")
	flag.IntVar(&flagWebPort, "port", 8080, "web server port")
	flag.StringVar(&flagDBPath, "db-path", "/tmp/gnockpit.db", "SQLite path for web-push subscriptions and VAPID keys; use a persistent path so keys survive reboots")
	flag.IntVar(&flagChainStuckSecs, "chain-stuck-secs", 30, "seconds without a new block before the chain-stuck alert fires")
	flag.IntVar(&flagMissedBlocksPct, "missed-blocks-pct", 5, "percent of blocks missed in the signing window before the validator-missing-blocks alert fires")
	flag.Var(&flagNotifyURLs, "notify", "Shoutrrr notification URL, repeatable (e.g. discord://token@id)")
	flag.StringVar(&flagPublicURL, "public-url", "", "public URL of this gnockpit instance, appended to external notification messages")
	flag.StringVar(&flagGeoIPPath, "geoip-db", "/tmp/gnockpit-geoip.mmdb", "path for the DB-IP City Lite mmdb powering the network map; auto-downloaded and refreshed monthly. Empty disables the map.")
	flag.Var(&flagLinks, "link", `extra header link button, "Title|URL", repeatable`)
	flag.Var(&flagStatusLinks, "status-link", `header link with a live status dot, "Title|URL" (BetterStack status pages only for now — the dot reflects <URL>/index.json), repeatable`)

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

	c := newClient(ctx)
	addr := fmt.Sprintf("%s:%d", flagWebAddr, flagWebPort)
	srv := web.NewServer(c, addr, flagInterval)

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
	srv.ChainStuckSecs = flagChainStuckSecs
	srv.PushManager = pushMgr

	for _, spec := range flagLinks {
		l, err := web.ParseLink(spec)
		if err != nil {
			return err
		}
		srv.Links = append(srv.Links, l)
	}
	for _, spec := range flagStatusLinks {
		l, err := web.ParseLink(spec)
		if err != nil {
			return err
		}
		srv.StatusLinks = append(srv.StatusLinks, web.NewStatusLink(l))
	}

	// Opt-in, token-gated notify-test API. Sourced from the environment so the
	// secret doesn't show up in `ps`. Empty = feature disabled.
	srv.NotifyTestToken = os.Getenv("GNOCKPIT_NOTIFY_TEST_TOKEN")

	if flagGeoIPPath != "" {
		srv.GeoIP = node.NewGeoIP(flagGeoIPPath, func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		})
	}

	return srv.Run(ctx)
}

func newClient(ctx context.Context) *node.Client {
	c := node.NewClient(flagRPC, 10*time.Second)
	c.Names = node.NewNameRegistryWithPersist(flagNamesPath)
	if flagVerbose {
		c.LogFn = func(method, url string, status int, dur time.Duration, err error) {
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s %s -> %v (%dms)\n", method, url, err, dur.Milliseconds())
			} else {
				fmt.Fprintf(os.Stderr, "%s %s -> %d (%dms)\n", method, url, status, dur.Milliseconds())
			}
		}
	}
	// Validator names and the genesis time come from the node's /genesis
	// endpoint, streamed and parsed only up to the validators array. Failure is
	// non-fatal: persisted names and live peer discovery still apply.
	if err := c.SeedNamesFromGenesis(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not seed validator names from genesis RPC: %v\n", err)
	}
	return c
}

func newContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}
