package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	neturl "net/url"
	"regexp"
	"strconv"
	"sync"
)

const valopersRealm = "gno.land/r/gnops/valopers"

// abciQuery runs an ABCI query (path + raw data argument) and returns the
// decoded response payload. gno's vm/* queries take the argument as data and
// return the result base64-encoded under result.response.ResponseBase.Data.
func (c *Client) abciQuery(ctx context.Context, abciPath, data string) ([]byte, error) {
	q := neturl.Values{}
	q.Set("path", `"`+abciPath+`"`)
	q.Set("data", `"`+base64.StdEncoding.EncodeToString([]byte(data))+`"`)
	body, err := c.rpcGet(ctx, "/abci_query?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var env struct {
		Result struct {
			Response struct {
				ResponseBase struct {
					Error json.RawMessage `json:"Error"`
					Data  []byte          `json:"Data"` // base64 in JSON → bytes
				} `json:"ResponseBase"`
			} `json:"response"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("abci_query %s: %w", abciPath, err)
	}
	if e := env.Result.Response.ResponseBase.Error; len(e) > 0 && string(e) != "null" {
		return nil, fmt.Errorf("abci_query %s: %s", abciPath, e)
	}
	return env.Result.Response.ResponseBase.Data, nil
}

// qrender calls vm/qrender for "<pkgPath>:<renderPath>".
func (c *Client) qrender(ctx context.Context, pkgPath, renderPath string) (string, error) {
	data, err := c.abciQuery(ctx, "vm/qrender", pkgPath+":"+renderPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

var (
	// Home render: "* [Moniker](/r/gnops/valopers:g1<operator>) - [profile](...)".
	valoperListRe = regexp.MustCompile(`\[([^\]]+)\]\(/r/gnops/valopers:(g1[a-z0-9]+)\)`)
	// Detail render: "- Signing Address: g1<signing>".
	valoperSignRe = regexp.MustCompile(`Signing Address:\s*(g1[a-z0-9]+)`)
)

// parseValoperList extracts operator address → moniker from a valopers home
// render page.
func parseValoperList(md string) map[string]string {
	out := map[string]string{}
	for _, m := range valoperListRe.FindAllStringSubmatch(md, -1) {
		moniker, operator := m[1], m[2]
		if _, ok := out[operator]; !ok {
			out[operator] = moniker
		}
	}
	return out
}

// parseValoperSigning extracts the signing (consensus) address from a valoper
// detail render, or "" if none is present.
func parseValoperSigning(md string) string {
	if m := valoperSignRe.FindStringSubmatch(md); m != nil {
		return m[1]
	}
	return ""
}

// FetchValoperNames enumerates the on-chain valoper registry and returns a map
// of signing (consensus) address → moniker. It pages the home render for
// operators + monikers, then fetches each operator's detail for its signing
// address. This is one query per registered valoper — call it on a slow timer,
// never in the snapshot hot path.
func (c *Client) FetchValoperNames(ctx context.Context) (map[string]string, error) {
	// 1. Page the home render → operator address → moniker.
	operators := map[string]string{}
	var pageErr error
	for page := 1; page <= 100; page++ {
		renderPath := ""
		if page > 1 {
			renderPath = "?page=" + strconv.Itoa(page)
		}
		md, err := c.qrender(ctx, valopersRealm, renderPath)
		if err != nil {
			pageErr = err
			break // keep whatever earlier pages yielded (best-effort)
		}
		got := parseValoperList(md)
		if len(got) == 0 {
			break
		}
		for op, mon := range got {
			operators[op] = mon
		}
	}
	// Surface an error only if nothing was collected; a partial result from one
	// flaky page is still useful for best-effort name enrichment.
	if len(operators) == 0 && pageErr != nil {
		return nil, pageErr
	}

	// 2. Resolve each operator's signing address from its detail render,
	//    with bounded concurrency.
	type entry struct{ sign, moniker string }
	out := make(chan entry)
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for op, mon := range operators {
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			md, err := c.qrender(ctx, valopersRealm, op)
			if err != nil {
				return
			}
			sign := parseValoperSigning(md)
			if sign == "" {
				return
			}
			select {
			case out <- entry{sign, mon}:
			case <-ctx.Done():
			}
		})
	}
	go func() { wg.Wait(); close(out) }()

	byAddr := map[string]string{}
	for e := range out {
		byAddr[e.sign] = e.moniker
	}
	return byAddr, nil
}
