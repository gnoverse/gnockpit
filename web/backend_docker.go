package web

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DockerBackend implements RuntimeBackend using docker logs and docker inspect.
type DockerBackend struct {
	ContainerName string
}

func (b *DockerBackend) streamLogsArgs() []string {
	return []string{"logs", b.ContainerName, "-f", "--since", "1m"}
}

func (b *DockerBackend) StreamLogs(ctx context.Context) (io.ReadCloser, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "docker", b.streamLogsArgs()...)
	// Combine stdout and stderr: gnoland may write to either stream.
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Start(); err != nil {
		w.Close()
		r.Close()
		return nil, err
	}
	// Close the parent's write handle: the child holds its own dup'd fd.
	// When the child exits, the read end will get EOF.
	w.Close()
	return &cmdReadCloser{ReadCloser: r, cmd: cmd}, nil
}

func (b *DockerBackend) FetchLogs(ctx context.Context, n int) ([]string, error) {
	out, err := exec.CommandContext(ctx, "docker", "logs", b.ContainerName,
		"--tail", fmt.Sprintf("%d", n)).CombinedOutput()
	if err != nil {
		return nil, err
	}
	return splitLines(string(out)), nil
}

func (b *DockerBackend) ServiceUptime(ctx context.Context) (time.Duration, error) {
	out, err := exec.CommandContext(ctx, "docker", "inspect", b.ContainerName,
		"--format", "{{.State.StartedAt}}").Output()
	if err != nil {
		return 0, err
	}
	ts := strings.TrimSpace(string(out))
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		// Fallback: try without nanoseconds
		t, err = time.Parse(time.RFC3339, ts)
		if err != nil {
			return 0, err
		}
	}
	return time.Since(t), nil
}

func (b *DockerBackend) binaryHashArgs() []string {
	return []string{"exec", b.ContainerName, "sha256sum", "/usr/local/bin/gnoland"}
}

func (b *DockerBackend) BinaryHash(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", b.binaryHashArgs()...).Output()
	if err != nil {
		return "", err
	}
	parts := strings.Fields(string(out))
	if len(parts) == 0 {
		return "", fmt.Errorf("unexpected sha256sum output: %q", out)
	}
	return parts[0][:12], nil
}

func (b *DockerBackend) ProcessMemory(ctx context.Context) (int, error) {
	out, err := exec.CommandContext(ctx, "docker", "inspect", b.ContainerName,
		"--format", "{{.State.Pid}}").Output()
	if err != nil {
		return 0, err
	}
	pid := strings.TrimSpace(string(out))
	if pid == "" || pid == "0" {
		return 0, nil
	}
	return readProcRSS(pid) // same /proc/<pid>/status read as SystemdBackend
}
