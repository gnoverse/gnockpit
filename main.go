package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/web"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	var (
		rpc       = flag.String("rpc", "http://127.0.0.1:26657", "RPC endpoint URL")
		port      = flag.Int("port", 8080, "web server port")
		addr      = flag.String("addr", "0.0.0.0", "web server bind address")
		interval  = flag.Duration("interval", 5*time.Second, "dashboard refresh interval")
		verbose   = flag.Bool("v", false, "verbose HTTP logging")
		dataDir   = flag.String("data-dir", "", "gnoland data directory (auto-detected if empty)")
		genesis   = flag.String("genesis", "", "path to genesis.json (auto-detected if empty)")
		names     = flag.String("names", "/tmp/gnockpit-names.json", "path to persistent name registry")
		service   = flag.String("service", "", "systemd service name (auto-detected from chain-id)")
		container = flag.String("container", "", "docker container name (mutually exclusive with -service)")
	)
	flag.Parse()

	c := node.NewClient(*rpc, 10*time.Second)
	c.Names = node.NewNameRegistryWithPersist(*names)
	if g := findGenesis(*genesis, *dataDir); g != "" {
		c.Names.SeedFromGenesis(g)
	}
	if *verbose {
		c.LogFn = func(method, url string, status int, dur time.Duration, err error) {
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s %s -> %v (%dms)\n", method, url, err, dur.Milliseconds())
			} else {
				fmt.Fprintf(os.Stderr, "%s %s -> %d (%dms)\n", method, url, status, dur.Milliseconds())
			}
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	listenAddr := fmt.Sprintf("%s:%d", *addr, *port)
	srv := web.NewServer(c, listenAddr, *interval)
	srv.DataDir = findDataDir(*dataDir, *genesis)
	srv.GenesisPath = findGenesis(*genesis, *dataDir)

	if *service != "" && *container != "" {
		return fmt.Errorf("-service and -container are mutually exclusive")
	}
	srv.Backend = buildBackend(*service, *container, srv)

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

func findGenesis(genesisPath, dataDir string) string {
	if genesisPath != "" {
		return genesisPath
	}
	candidates := []string{}
	if dataDir != "" {
		candidates = append(candidates, dataDir+"/config/genesis.json")
	}
	candidates = append(candidates,
		"gnoland-data/config/genesis.json",
		"../gnoland-data/config/genesis.json",
	)
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

func findDataDir(dataDir, genesisPath string) string {
	if dataDir != "" {
		return dataDir
	}
	g := findGenesis(genesisPath, dataDir)
	if g != "" {
		for i := len(g) - 1; i >= 0; i-- {
			if g[i] == '/' {
				dir := g[:i]
				for j := len(dir) - 1; j >= 0; j-- {
					if dir[j] == '/' {
						return dir[:j]
					}
				}
			}
		}
	}
	return ""
}
