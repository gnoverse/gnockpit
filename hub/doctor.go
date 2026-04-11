package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gnoverse/gnockpit/node"
	"github.com/gorilla/websocket"
)

// DoctorCheck is a single diagnostic result.
type DoctorCheck struct {
	Severity string   `json:"severity"` // "ok", "warn", "error"
	Check    string   `json:"check"`
	Probes   []string `json:"probes,omitempty"` // for cluster checks
	Detail   string   `json:"detail"`
}

// DoctorReport is the full diagnostic output.
type DoctorReport struct {
	Mode      string        `json:"mode"` // "single", "cluster"
	Checks    []DoctorCheck `json:"checks"`
	Timestamp string        `json:"timestamp"`
	Summary   string        `json:"summary"` // e.g. "5 ok, 2 warn, 1 error"
}

// DoctorSingle runs one-shot diagnostics against a single RPC endpoint.
func DoctorSingle(ctx context.Context, rpcURL string, timeout time.Duration) *DoctorReport {
	report := &DoctorReport{
		Mode:      "single",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	c := node.NewClient(rpcURL, timeout)

	// Check 1: RPC reachable
	status, err := c.GetStatus(ctx)
	if err != nil {
		report.Checks = append(report.Checks, DoctorCheck{"error", "rpc_reachable", nil, "Not reachable: " + err.Error()})
		report.Summary = summarize(report.Checks)
		return report
	}
	report.Checks = append(report.Checks, DoctorCheck{"ok", "rpc_reachable", nil,
		fmt.Sprintf("Connected to %s (%s)", status.NodeInfo.Moniker, rpcURL)})

	// Check 2: Chain info
	report.Checks = append(report.Checks, DoctorCheck{"ok", "chain_id", nil, status.NodeInfo.Network})

	// Check 3: Catching up
	if status.SyncInfo.CatchingUp {
		report.Checks = append(report.Checks, DoctorCheck{"warn", "sync_status", nil, "Node is catching up"})
	} else {
		report.Checks = append(report.Checks, DoctorCheck{"ok", "sync_status", nil, "Synced"})
	}

	// Check 4: Block height
	h, _ := strconv.Atoi(status.SyncInfo.LatestBlockHeight)
	report.Checks = append(report.Checks, DoctorCheck{"ok", "block_height", nil,
		fmt.Sprintf("Height %d", h)})

	// Check 5: Block time freshness
	if bt, err := time.Parse(time.RFC3339Nano, status.SyncInfo.LatestBlockTime); err == nil {
		age := time.Since(bt)
		if strings.HasPrefix(status.SyncInfo.LatestBlockTime, "1970") {
			report.Checks = append(report.Checks, DoctorCheck{"warn", "block_time", nil, "Block time is 1970 — no blocks produced yet"})
		} else if age > 30*time.Second {
			report.Checks = append(report.Checks, DoctorCheck{"warn", "block_time", nil,
				fmt.Sprintf("Last block %s ago", age.Truncate(time.Second))})
		} else {
			report.Checks = append(report.Checks, DoctorCheck{"ok", "block_time", nil,
				fmt.Sprintf("Last block %s ago", age.Truncate(time.Second))})
		}
	}

	// Check 6: Peers
	peers, err := c.GetNetInfo(ctx)
	if err != nil {
		report.Checks = append(report.Checks, DoctorCheck{"warn", "peers", nil, "Cannot fetch peers: " + err.Error()})
	} else if len(peers) == 0 {
		report.Checks = append(report.Checks, DoctorCheck{"error", "peers", nil, "No peers connected"})
	} else if len(peers) < 3 {
		report.Checks = append(report.Checks, DoctorCheck{"warn", "peers", nil,
			fmt.Sprintf("%d peers (low)", len(peers))})
	} else {
		report.Checks = append(report.Checks, DoctorCheck{"ok", "peers", nil,
			fmt.Sprintf("%d peers", len(peers))})
	}

	// Check 7: Validator info
	if status.ValidatorInfo.Address != "" {
		report.Checks = append(report.Checks, DoctorCheck{"ok", "validator", nil,
			"Address: " + status.ValidatorInfo.Address})
	} else {
		report.Checks = append(report.Checks, DoctorCheck{"warn", "validator", nil, "No validator address — not a validator?"})
	}

	// Check 8: Consensus state
	cs, _, err := c.GetConsensusState(ctx)
	if err != nil {
		report.Checks = append(report.Checks, DoctorCheck{"warn", "consensus", nil, "Cannot fetch: " + err.Error()})
	} else {
		info := fmt.Sprintf("h=%s r=%s s=%s", cs.Height, cs.Round, cs.Step)
		if cs.RoundStartTime != "" {
			if t, err := time.Parse(time.RFC3339Nano, cs.RoundStartTime); err == nil {
				age := time.Since(t)
				if age > 30*time.Second {
					report.Checks = append(report.Checks, DoctorCheck{"warn", "consensus", nil,
						fmt.Sprintf("%s — stuck for %s", info, age.Truncate(time.Second))})
				} else {
					report.Checks = append(report.Checks, DoctorCheck{"ok", "consensus", nil, info})
				}
			} else {
				report.Checks = append(report.Checks, DoctorCheck{"ok", "consensus", nil, info})
			}
		} else {
			report.Checks = append(report.Checks, DoctorCheck{"ok", "consensus", nil, info})
		}
	}

	// Check 9: Validators
	vals, err := c.GetValidators(ctx)
	if err != nil {
		report.Checks = append(report.Checks, DoctorCheck{"warn", "validators", nil, "Cannot fetch: " + err.Error()})
	} else {
		needed := (len(vals)*2/3) + 1
		report.Checks = append(report.Checks, DoctorCheck{"ok", "validators", nil,
			fmt.Sprintf("%d validators (need %d for consensus)", len(vals), needed)})
	}

	report.Summary = summarize(report.Checks)
	return report
}

// DoctorCluster fetches probe data from a hub server and runs cluster health checks.
func DoctorCluster(ctx context.Context, serverURL, token string) (*DoctorReport, error) {
	report := &DoctorReport{
		Mode:      "cluster",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	// Fetch probes from server API
	url := strings.TrimRight(serverURL, "/") + "/api/probes"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch probes: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, body)
	}

	var probes map[string]*ProbeState
	if err := json.NewDecoder(resp.Body).Decode(&probes); err != nil {
		return nil, fmt.Errorf("decode probes: %w", err)
	}

	// Per-probe checks
	for name, ps := range probes {
		if !ps.Connected {
			report.Checks = append(report.Checks, DoctorCheck{"error", "probe_disconnected", []string{name}, "Probe disconnected"})
			continue
		}
		if ps.Snapshot == nil {
			report.Checks = append(report.Checks, DoctorCheck{"warn", "probe_no_data", []string{name}, "Connected but no snapshot yet"})
			continue
		}
		report.Checks = append(report.Checks, DoctorCheck{"ok", "probe_connected", []string{name},
			fmt.Sprintf("Height %s, chain %s", ps.Height, ps.ChainID)})
	}

	// Fetch health report
	url = strings.TrimRight(serverURL, "/") + "/api/health"
	req, err = http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch health: %w", err)
	}
	defer resp.Body.Close()

	var health HealthReport
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, fmt.Errorf("decode health: %w", err)
	}

	// Convert health issues to doctor checks
	for _, issue := range health.Issues {
		report.Checks = append(report.Checks, DoctorCheck{
			Severity: issue.Severity,
			Check:    issue.Check,
			Probes:   issue.Probes,
			Detail:   issue.Detail,
		})
	}

	report.Summary = summarize(report.Checks)
	return report, nil
}

// DoctorLive connects to a hub's doctor WS and streams health updates.
// It calls onReport for each health update received.
func DoctorLive(ctx context.Context, serverURL, token string, onReport func(*HealthReport)) error {
	wsURL := strings.TrimRight(serverURL, "/") + "/ws/doctor"
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)

	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)

	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, header)
		if err != nil {
			log.Printf("doctor: connect failed: %v, retrying in %s", err, backoff)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		backoff = time.Second
		log.Printf("doctor: connected to %s", wsURL)

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				log.Printf("doctor: disconnected: %v", err)
				conn.Close()
				break
			}
			var envelope struct {
				Type string          `json:"type"`
				Data json.RawMessage `json:"data"`
			}
			if json.Unmarshal(msg, &envelope) != nil {
				continue
			}
			if envelope.Type == "cluster_health" {
				var health HealthReport
				if json.Unmarshal(envelope.Data, &health) == nil {
					onReport(&health)
				}
			}
		}
	}
}

func summarize(checks []DoctorCheck) string {
	ok, warn, errC := 0, 0, 0
	for _, c := range checks {
		switch c.Severity {
		case "ok":
			ok++
		case "warn":
			warn++
		case "error":
			errC++
		}
	}
	return fmt.Sprintf("%d ok, %d warn, %d error", ok, warn, errC)
}
