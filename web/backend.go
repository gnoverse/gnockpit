package web

import (
	"context"
	"io"
	"time"
)

// RuntimeBackend abstracts log access and process metrics for the managed gnoland process.
type RuntimeBackend interface {
	// StreamLogs returns a stream of log lines. The caller owns the reconnect/retry loop.
	StreamLogs(ctx context.Context) (io.ReadCloser, error)
	// FetchLogs returns the last n raw log lines (no sanitization applied).
	FetchLogs(ctx context.Context, n int) ([]string, error)
	// ServiceUptime returns how long the gnoland process has been running. Returns 0 if unknown.
	ServiceUptime(ctx context.Context) (time.Duration, error)
	// ProcessMemory returns the resident set size of the gnoland process in KB. Returns 0 if unknown.
	ProcessMemory(ctx context.Context) (int, error)
	// BinaryHash returns the first 12 hex chars of the SHA256 of the gnoland binary. Returns "" if unavailable.
	BinaryHash(ctx context.Context) (string, error)
}
