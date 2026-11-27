package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gnolang/gno/tm2/pkg/crypto"
	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/eta"
	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/presence"
	"github.com/gnoverse/gnockpit/ranking"
	"github.com/gnoverse/gnockpit/web"
	"github.com/gnoverse/gnockpit/web/push"
)

const (
	defaultRPC               = "http://127.0.0.1:26657"
	defaultInterval          = 5 * time.Second
	defaultReadHeaderTimeout = 10 * time.Second
)

var (
	flagRPCs              stringSlice
	flagVerbose           bool
	flagInterval          time.Duration
	flagWebPort           int
	flagWebAddr           string
	flagReadHeaderTimeout time.Duration
	flagDBPath            string
	flagHistoryPace       time.Duration
	flagHistoryBatch      int
	flagChainStuckSecs    int
	flagMaxMissedInARow   int
	flagNamesPath         string
	flagNotifyConfig      string
	flagSendTimeout       time.Duration
	flagRedactSecret      bool
	flagPublicURL         string
	flagGeoIPPath         string
	flagASNPath           string
	flagLinks             stringSlice
	flagStatusLinks       stringSlice
	flagHideSources       bool
	flagChainName         string
	flagAnalytics         string
	flagAnalyticsDomain   string

	flagRanking             bool
	flagRankingExclude      operatorList
	flagRankingHideExcluded bool
)

// stringSlice is a flag.Value that accumulates one entry per occurrence, for
// the repeatable flags (-rpc, -link, -status-link).
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ",") }

func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// nonNegativeDuration is a flag.Value for a duration of 0 or more: a negative
// one is refused as the flags are parsed.
type nonNegativeDuration time.Duration

func (d *nonNegativeDuration) String() string {
	if d == nil {
		return ""
	}
	return time.Duration(*d).String()
}

func (d *nonNegativeDuration) Set(v string) error {
	parsed, err := time.ParseDuration(v)
	if err != nil {
		return err
	}
	if parsed < 0 {
		return fmt.Errorf("%s is below 0", parsed)
	}
	*d = nonNegativeDuration(parsed)
	return nil
}

// nonNegativeDurationVar defines a duration flag on fs as fs.DurationVar does,
// but one refusing a negative value. A back-quoted word in usage names the
// value in the flags' help.
func nonNegativeDurationVar(fs *flag.FlagSet, p *time.Duration, name string, value time.Duration, usage string) {
	*p = value
	fs.Var((*nonNegativeDuration)(p), name, usage)
}

// operatorList is a flag.Value for a comma-separated list of operator g1
// addresses, surrounding spaces trimmed; each occurrence adds its entries.
// An entry is an address as the chain writes it, the only form a ranking's
// operator matches: its bech32 checksum holds, and it is lowercase.
type operatorList []string

func (l *operatorList) String() string { return strings.Join(*l, ",") }

func (l *operatorList) Set(v string) error {
	var entries []string
	for _, entry := range strings.Split(v, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return errors.New("an empty entry")
		}
		if addr, err := crypto.AddressFromString(entry); err != nil || addr.String() != entry {
			return fmt.Errorf("%q is no g1 address", entry)
		}
		entries = append(entries, entry)
	}
	*l = append(*l, entries...)
	return nil
}

// checkRankingFlags refuses a ranking flag set while the ranking is off: it
// would do nothing.
func checkRankingFlags(on bool, exclude operatorList, hideExcluded bool) error {
	if on {
		return nil
	}
	if len(exclude) > 0 {
		return errors.New("--ranking-exclude needs --ranking")
	}
	if hideExcluded {
		return errors.New("--ranking-hide-excluded needs --ranking")
	}
	return nil
}

// publicURLLimit is the most bytes of a public URL: a Discord message quotes
// it with a path in its link line, and the ranking in a field's value, which
// holds at most 1,024 characters.
const publicURLLimit = 512

// checkPublicURL refuses a public URL that is not empty or an absolute http
// or https URL of at most publicURLLimit bytes with a host, no whitespace and
// a port that is absent or in 1-65535: every external notification carries
// it as a link, which must open from a chat.
func checkPublicURL(s string) error {
	if s == "" {
		return nil
	}
	if len(s) > publicURLLimit {
		return fmt.Errorf("--public-url is %d bytes: want at most %d", len(s), publicURLLimit)
	}
	refused := fmt.Errorf("--public-url %q: want an absolute http or https URL, such as https://gnockpit.example.com", s)
	if strings.IndexFunc(s, unicode.IsSpace) >= 0 {
		return refused
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return refused
	}
	if strings.HasSuffix(u.Host, ":") {
		return refused
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return refused
		}
	}
	return nil
}

func usage() {
	fmt.Fprint(flag.CommandLine.Output(), `gnockpit — live web dashboard for gno.land validator nodes.

Usage:
  gnockpit [flags]

Flags:
`)
	flag.PrintDefaults()
}

// registerFlags defines gnockpit's flags on fs, each setting its flag* variable.
func registerFlags(fs *flag.FlagSet) {
	fs.Var(&flagRPCs, "rpc", fmt.Sprintf("Tendermint RPC endpoint to query; repeatable to consolidate several sources. Default %s", defaultRPC))
	fs.BoolVar(&flagVerbose, "verbose", false, "log HTTP requests to the node")
	fs.BoolVar(&flagVerbose, "v", false, "log HTTP requests to the node (shorthand)")
	fs.DurationVar(&flagInterval, "interval", defaultInterval, "dashboard refresh interval")
	fs.StringVar(&flagNamesPath, "names", "/tmp/gnockpit-names.json", "path to the persistent validator-name registry")
	fs.StringVar(&flagWebAddr, "addr", "0.0.0.0", "web server bind address")
	fs.IntVar(&flagWebPort, "port", 8080, "web server port")
	nonNegativeDurationVar(fs, &flagReadHeaderTimeout, "read-header-timeout", defaultReadHeaderTimeout, "how long a client may take to send a request's headers before its connection is closed unanswered, a `duration` of 0 or more; 0 for none")
	fs.StringVar(&flagDBPath, "db-path", "/tmp/gnockpit.db", "SQLite database for push notifications, the block history, block-ETA history, peer presence and the published rankings; keep it on a persistent path so they survive reboots")
	fs.DurationVar(&flagHistoryPace, "history-pace", blocks.DefaultPace, "wait between the batches that fill the block history from the first -rpc, back to genesis and up to the head after downtime; 0 for none")
	fs.IntVar(&flagHistoryBatch, "history-batch", blocks.DefaultBatch, "commits asked in one JSON-RPC batch while filling the block history; halved until restart when the first -rpc refuses a batch, for 10 minutes when one times out")
	fs.IntVar(&flagChainStuckSecs, "chain-stuck-secs", 30, "seconds without a new block before the chain-stuck alert fires")
	fs.IntVar(&flagMaxMissedInARow, "max-missed-in-a-row", 10, "consecutive blocks a validator must miss to be flagged down (alert fires + counted inactive) and must then sign to recover")
	fs.DurationVar(&flagSendTimeout, "send-timeout", push.DefaultSendTimeout, "how long a push notification or webhook send may take before it is given up")
	fs.StringVar(&flagNotifyConfig, "notify-config", "", "notification config file (JSON): the Shoutrrr URLs and the alerts each receives; reread when it changes")
	fs.BoolVar(&flagRedactSecret, "redact-secret", false, "strip notification URL secrets (tokens, webhook IDs) from logged Shoutrrr errors; off by default: errors are logged verbatim")
	fs.StringVar(&flagPublicURL, "public-url", "", fmt.Sprintf("public URL of this gnockpit instance, an absolute http or https URL of at most %d bytes: the link each external notification carries, on Discord its \"Open in Gnockpit ↗\" line", publicURLLimit))
	fs.StringVar(&flagGeoIPPath, "geoip-db", "/tmp/gnockpit-geoip.mmdb", "path for the DB-IP City Lite mmdb powering the network map; auto-downloaded and refreshed monthly. Empty disables the map.")
	fs.StringVar(&flagASNPath, "asn-db", "/tmp/gnockpit-asn.mmdb", "path for the DB-IP ASN Lite mmdb powering cloud-provider detection; auto-downloaded and refreshed monthly. Empty disables it.")
	fs.Var(&flagLinks, "link", `extra header link button, "Title|URL", repeatable`)
	fs.Var(&flagStatusLinks, "status-link", `header link with a live status dot, "Title|URL" (BetterStack status pages only for now — the dot reflects <URL>/index.json), repeatable`)
	fs.BoolVar(&flagHideSources, "hide-sources", false, "hide the configured source nodes from the peers list")
	fs.StringVar(&flagChainName, "chain-name", "", "display name for the chain in titles and the app icon; defaults to the chain-id (e.g. \"test13\" for chain-id \"test-13\")")
	fs.StringVar(&flagAnalytics, "analytics", "", "load a third-party analytics script in the dashboard; only \"simple-analytics\" is supported. Off by default: a self-hosted dashboard reports to nobody unless you ask it to.")
	fs.StringVar(&flagAnalyticsDomain, "analytics-domain", "", "serve the analytics script and its no-JS pixel from this custom domain (e.g. sa.example.com) instead of the provider's own hosts, which ad blockers list; needs --analytics")
	fs.BoolVar(&flagRanking, "ranking", false, "compute and serve the daily validator ranking at /api/ranking, from the block history; off by default")
	flagRankingExclude = nil // a fresh parse starts from no operators
	fs.Var(&flagRankingExclude, "ranking-exclude", "operators left out of the ranking's order, as a comma-separated list of operator g1 addresses; the ranking publishes the list in the API. Needs --ranking.")
	fs.BoolVar(&flagRankingHideExcluded, "ranking-hide-excluded", false, "tell the ranking's readers to hide the excluded operators; the API lists them either way. Needs --ranking.")
}

func main() {
	flag.Usage = usage

	registerFlags(flag.CommandLine)

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

	// Validated before anything is opened or polled, so a typo fails at
	// startup rather than silently collecting nothing.
	analytics, err := web.ParseAnalytics(flagAnalytics, flagAnalyticsDomain)
	if err != nil {
		return err
	}

	if err := checkRankingFlags(flagRanking, flagRankingExclude, flagRankingHideExcluded); err != nil {
		return err
	}

	if err := checkPublicURL(flagPublicURL); err != nil {
		return err
	}

	sources := newSources(ctx)
	addr := fmt.Sprintf("%s:%d", flagWebAddr, flagWebPort)
	srv := web.NewServer(sources.Primary(), addr, flagInterval)
	srv.Sources = sources

	db, err := push.OpenDB(flagDBPath)
	if err != nil {
		return fmt.Errorf("open push database: %w", err)
	}
	defer db.Close()

	// The block history shares the same SQLite connection as the push store,
	// filled back to genesis from the primary source; the tracker stops with
	// ctx.
	blockStore, err := blocks.NewStore(ctx, db.SQL())
	if err != nil {
		return fmt.Errorf("init block history: %w", err)
	}
	blockTracker, err := blocks.NewTracker(ctx, blockStore, sources.Primary(), blocks.Config{Pace: flagHistoryPace, Batch: flagHistoryBatch})
	if err != nil {
		return fmt.Errorf("init block history tracker (-history-pace %s, -history-batch %d): %w", flagHistoryPace, flagHistoryBatch, err)
	}
	srv.Blocks = blockStore
	srv.BlockFill = blockTracker

	// So do the block times behind block ETAs, backfilled from the primary
	// source; the tracker stops with ctx.
	etaStore, err := eta.NewStore(ctx, db.SQL())
	if err != nil {
		return fmt.Errorf("init block ETA store: %w", err)
	}
	tracker := eta.NewTracker(etaStore, sources.Primary())
	srv.ETA = tracker
	go tracker.Run(ctx)

	// Peer presence shares the connection too; publish is its only user.
	presenceStore, err := presence.NewStore(ctx, db.SQL())
	if err != nil {
		return fmt.Errorf("init peer presence: %w", err)
	}
	srv.Presence = presenceStore

	pushMgr, err := push.NewManager(db, flagChainStuckSecs, flagMaxMissedInARow)
	if err != nil {
		return fmt.Errorf("init push manager: %w", err)
	}
	pushMgr.SetRedactSecrets(flagRedactSecret)
	if flagSendTimeout <= 0 {
		return fmt.Errorf("invalid -send-timeout %s: want more than 0", flagSendTimeout)
	}
	pushMgr.SetSendTimeout(flagSendTimeout)
	if flagNotifyConfig != "" {
		if err := pushMgr.LoadNotifyConfig(flagNotifyConfig); err != nil {
			return fmt.Errorf("invalid -notify-config: %w", err)
		}
	}
	if flagPublicURL != "" {
		pushMgr.SetPublicURL(flagPublicURL)
	}
	// The block history tells the set changes it stores going up: it starts
	// once the push manager it tells them to exists.
	names := sources.Primary().Names
	blockTracker.OnValsetChange(func(c blocks.ValsetChange) { pushMgr.NotifyValsetChange(c, names.Name) })
	go blockTracker.Run(ctx)
	srv.MaxMissedInARow = flagMaxMissedInARow
	srv.ChainStuckSecs = flagChainStuckSecs
	srv.HideSources = flagHideSources
	srv.ChainName = flagChainName
	srv.Analytics = analytics
	srv.ReadHeaderTimeout = flagReadHeaderTimeout
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
	if flagASNPath != "" {
		srv.ASN = node.NewASN(flagASNPath, func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		})
	}

	if flagRanking {
		days, err := ranking.NewDayStore(ctx, db.SQL())
		if err != nil {
			return fmt.Errorf("init ranking days: %w", err)
		}
		pub := &ranking.Publisher{
			Blocks: blockStore,
			Fill:   blockTracker,
			Days:   days,
			Profiles: func() (ranking.Profiles, bool) {
				m, ok := srv.Valopers()
				return ranking.Profiles(m), ok
			},
			Names:   srv.Moniker,
			Exclude: flagRankingExclude,
			Now:     time.Now,
			OnPublish: func(cur ranking.Published, prev *ranking.Published) {
				pushMgr.NotifyRanking(cur.Chain+":"+cur.Day, ranking.DigestOf(cur, prev))
			},
		}
		srv.Ranking = pub
		srv.RankingHideExcluded = flagRankingHideExcluded
		go pub.Run(ctx)
	}

	return srv.Run(ctx)
}

func newSources(ctx context.Context) *node.Sources {
	if len(flagRPCs) == 0 {
		flagRPCs = stringSlice{defaultRPC}
	}
	names := node.NewNameRegistryWithPersist(flagNamesPath)
	sources := node.NewSources(flagRPCs, 10*time.Second, names, func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	})
	sources.LookupEgressIP()
	if flagVerbose {
		sources.SetRequestLogger(func(method, url string, status int, dur time.Duration, err error) {
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s %s -> %v (%dms)\n", method, url, err, dur.Milliseconds())
			} else {
				fmt.Fprintf(os.Stderr, "%s %s -> %d (%dms)\n", method, url, status, dur.Milliseconds())
			}
		})
	}
	// Validator names and the genesis time come from the primary node's /genesis
	// endpoint, streamed and parsed only up to the validators array. Failure is
	// non-fatal: persisted names and live peer discovery still apply.
	if err := sources.Primary().SeedNamesFromGenesis(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not seed validator names from genesis RPC: %v\n", err)
	}
	return sources
}

func newContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}
