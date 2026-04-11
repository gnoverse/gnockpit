package hub

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gnoverse/gnockpit/node"
	"github.com/gorilla/websocket"
)

// ProbeClient connects to a hub server and pushes snapshots.
type ProbeClient struct {
	ServerURL string // e.g. "wss://gnockpit.example.com/ws/probe"
	Token     string
	RPC       *node.Client
	Interval  time.Duration

	// Optional callbacks
	OnConnect    func()
	OnDisconnect func(error)
}

// Run connects to the hub and pushes snapshots until ctx is cancelled.
// Auto-reconnects with exponential backoff.
func (p *ProbeClient) Run(ctx context.Context, fetchSnapshot func(context.Context) *node.Snapshot) error {
	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := p.connectAndPush(ctx, fetchSnapshot)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if p.OnDisconnect != nil {
			p.OnDisconnect(err)
		}
		log.Printf("probe: disconnected: %v, reconnecting in %s", err, backoff)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (p *ProbeClient) connectAndPush(ctx context.Context, fetchSnapshot func(context.Context) *node.Snapshot) error {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+p.Token)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, p.ServerURL, header)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Reset backoff on successful connect
	if p.OnConnect != nil {
		p.OnConnect()
	}
	log.Printf("probe: connected to %s", p.ServerURL)

	// Set generous read deadline — hub may not send anything for a while
	conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		return nil
	})

	// Read pump (drain acks, detect close)
	done := make(chan error, 1)
	go func() {
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				done <- err
				return
			}
			conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		}
	}()

	// Ping loop to keep connection alive during long snapshot fetches
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()
	go func() {
		for range pingTicker.C {
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(p.Interval)
	defer ticker.Stop()

	push := func() error {
		snap := fetchSnapshot(ctx)
		msg := struct {
			Type string         `json:"type"`
			Data *node.Snapshot `json:"data"`
		}{Type: "snapshot", Data: snap}
		b, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		return conn.WriteMessage(websocket.TextMessage, b)
	}

	// Push immediately
	if err := push(); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return ctx.Err()
		case err := <-done:
			return err
		case <-ticker.C:
			if err := push(); err != nil {
				return err
			}
		}
	}
}
