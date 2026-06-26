package web

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// SystemdBackend implements RuntimeBackend using journalctl and systemctl.
type SystemdBackend struct {
	mu     sync.Mutex    // guards name
	name   string        // cached resolved name; empty until first successful resolution
	nameFn func() string // called to resolve name lazily; nil if name is static
}

// NewSystemdBackend creates a SystemdBackend. If staticName is non-empty it is used directly.
// Otherwise nameFn is called on each operation until it returns a non-empty value, after which
// the result is cached (cache-once). Falls back to "gnoland.service" when nameFn returns "".
func NewSystemdBackend(staticName string, nameFn func() string) *SystemdBackend {
	return &SystemdBackend{name: staticName, nameFn: nameFn}
}

// resolvedName returns the service name, applying lazy resolution and cache-once caching.
// Safe for concurrent use.
func (b *SystemdBackend) resolvedName() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.name != "" {
		return b.name
	}
	if b.nameFn != nil {
		if n := b.nameFn(); n != "" {
			b.name = n
			return b.name
		}
	}
	return "gnoland.service"
}

func (b *SystemdBackend) streamArgs() []string {
	return []string{"-u", b.resolvedName(), "-f", "-o", "cat", "--no-pager"}
}

func (b *SystemdBackend) StreamLogs(ctx context.Context) (io.ReadCloser, error) {
	cmd := exec.CommandContext(ctx, "journalctl", b.streamArgs()...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &cmdReadCloser{ReadCloser: stdout, cmd: cmd}, nil
}

func (b *SystemdBackend) ServiceUptime(ctx context.Context) (time.Duration, error) {
	out, err := exec.CommandContext(ctx, "systemctl", "show", b.resolvedName(),
		"--property=ActiveEnterTimestamp", "--value").Output()
	if err != nil {
		return 0, err
	}
	ts := strings.TrimSpace(string(out))
	t, err := time.Parse("Mon 2006-01-02 15:04:05 MST", ts)
	if err != nil {
		return 0, err
	}
	return time.Since(t), nil
}

func (b *SystemdBackend) ProcessMemory(ctx context.Context) (int, error) {
	out, err := exec.CommandContext(ctx, "systemctl", "show", b.resolvedName(),
		"--property=MainPID", "--value").Output()
	if err != nil {
		return 0, err
	}
	pid := strings.TrimSpace(string(out))
	if pid == "" || pid == "0" {
		return 0, nil
	}
	return readProcRSS(pid)
}

// ---- helpers shared with DockerBackend ----

// cmdReadCloser wraps a command's pipe and waits for the command on close.
type cmdReadCloser struct {
	io.ReadCloser
	cmd *exec.Cmd
}

func (c *cmdReadCloser) Close() error {
	err := c.ReadCloser.Close()
	// cmd.Wait() error is intentionally ignored: the pipe read error (if any) is
	// what callers care about; non-zero exit (e.g. journalctl killed on context
	// cancellation) is expected and handled by the caller's retry loop.
	c.cmd.Wait()
	return err
}

// readProcRSS reads VmRSS from /proc/<pid>/status and returns KB.
func readProcRSS(pid string) (int, error) {
	f, err := os.Open("/proc/" + pid + "/status")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "VmRSS:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				var kb int
				fmt.Sscanf(parts[1], "%d", &kb)
				return kb, nil
			}
		}
	}
	return 0, nil
}
