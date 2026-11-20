package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	neturl "net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
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
	var res struct {
		Response struct {
			ResponseBase struct {
				Error json.RawMessage `json:"Error"`
				Data  []byte          `json:"Data"` // base64 in JSON → bytes
			} `json:"ResponseBase"`
		} `json:"response"`
	}
	if err := c.rpcGetJSON(ctx, "/abci_query?"+q.Encode(), &res); err != nil {
		return nil, fmt.Errorf("abci_query %s: %w", abciPath, err)
	}
	if e := res.Response.ResponseBase.Error; len(e) > 0 && string(e) != "null" {
		return nil, fmt.Errorf("abci_query %s: %s", abciPath, e)
	}
	return res.Response.ResponseBase.Data, nil
}

// qrender calls vm/qrender for "<pkgPath>:<renderPath>".
func (c *Client) qrender(ctx context.Context, pkgPath, renderPath string) (string, error) {
	data, err := c.abciQuery(ctx, "vm/qrender", pkgPath+":"+renderPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Halt is the coordinated halt governance set on chain: the height the chain
// stops at and the minimum release allowed to resume it. Height 0 means no halt
// was ever set; the params keep their value after the halt, so a Height at or
// below the chain's height is a past halt.
type Halt struct {
	Height     int64
	MinVersion string
}

// HaltParams reads the node:p:halt_height and node:p:halt_min_version chain
// params. A params query runs no code, so nodes with rpc.allow_code_execution
// set to false still answer it.
func (c *Client) HaltParams(ctx context.Context) (Halt, error) {
	var h Halt
	height, err := c.paramString(ctx, "node:p:halt_height")
	if err != nil {
		return h, err
	}
	if height != "" {
		if h.Height, err = strconv.ParseInt(height, 10, 64); err != nil {
			return h, fmt.Errorf("node:p:halt_height %q: %w", height, err)
		}
	}
	if h.MinVersion, err = c.paramString(ctx, "node:p:halt_min_version"); err != nil {
		return h, err
	}
	return h, nil
}

// paramString reads a chain param whose amino-JSON value is a string (amino
// writes an int64 as a quoted string too); an unset param reads as "".
func (c *Client) paramString(ctx context.Context, key string) (string, error) {
	data, err := c.abciQuery(ctx, "params/"+key, "")
	if err != nil || len(data) == 0 {
		return "", err
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return "", fmt.Errorf("param %s: %w", key, err)
	}
	return s, nil
}

var (
	// Home render: "* [Moniker](/r/gnops/valopers:g1<operator>) - [profile](...)".
	valoperListRe = regexp.MustCompile(`\[([^\]]+)\]\(/r/gnops/valopers:(g1[a-z0-9]+)\)`)
	qevalBoolRe   = regexp.MustCompile(`^\((true|false) bool\)$`)
	qevalIntRe    = regexp.MustCompile(`^\((-?[0-9]+) int64\)$`)
)

// Valoper is a validator operator's profile in the valopers realm.
type Valoper struct {
	Operator, Moniker, Description, SigningAddress, SigningPubKey, ServerType string
	State                                                                     *ValoperState // nil when vm/qeval did not answer
	StateError                                                                string        // why, when State is nil
}

// ValoperState is what the realm keeps about a profile beyond its render.
type ValoperState struct {
	KeepRunning        bool  // the operator wants its validator running in the set
	LastRotationHeight int64 // the height its signing key last changed, or its registration's
}

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

// parseValoperDetail reads a valoper's detail render: "## <moniker>", the
// description, then the realm's "- <field>: <value>" lines from the last
// "- Operator Address:" on, which the realm renders after the description: a
// description's own list or field-like lines stay in it. ok needs a moniker
// and an operator.
func parseValoperDetail(md string) (Valoper, bool) {
	var v Valoper
	lines := strings.Split(md, "\n")
	head := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "## ") })
	fields := -1
	for i, l := range lines {
		if i > head && strings.HasPrefix(l, "- Operator Address:") {
			fields = i
		}
	}
	if head < 0 || fields < 0 {
		return Valoper{}, false
	}
	v.Moniker = strings.TrimSpace(lines[head][3:])
	v.Description = strings.TrimSpace(strings.Join(lines[head+1:fields], "\n"))
	for _, line := range lines[fields:] {
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "- "), ":")
		if !ok || !strings.HasPrefix(line, "- ") {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "Operator Address":
			v.Operator = value
		case "Signing Address":
			v.SigningAddress = value
		case "Signing PubKey":
			v.SigningPubKey = value
		case "Server Type":
			v.ServerType = value
		}
	}
	return v, v.Moniker != "" && v.Operator != ""
}

// parseQevalBool reads a vm/qeval answer for a bool: "(true bool)".
func parseQevalBool(s string) (bool, error) {
	m := qevalBoolRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return false, fmt.Errorf("not a bool: %q", s)
	}
	return m[1] == "true", nil
}

// parseQevalInt reads a vm/qeval answer for an int64: "(37395 int64)".
func parseQevalInt(s string) (int64, error) {
	m := qevalIntRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("not an int64: %q", s)
	}
	return strconv.ParseInt(m[1], 10, 64)
}

// valoperState reads, with vm/qeval, the realm's keep-running flag and last
// rotation height of an operator's profile.
func (c *Client) valoperState(ctx context.Context, operator string) (*ValoperState, error) {
	expr := valopersRealm + `.GetByAddr(address("` + operator + `"))`
	keep, err := c.abciQuery(ctx, "vm/qeval", expr+".KeepRunning")
	if err != nil {
		return nil, err
	}
	rot, err := c.abciQuery(ctx, "vm/qeval", expr+".LastRotationHeight")
	if err != nil {
		return nil, err
	}
	var st ValoperState
	if st.KeepRunning, err = parseQevalBool(string(keep)); err != nil {
		return nil, fmt.Errorf("keep running of %s: %w", operator, err)
	}
	if st.LastRotationHeight, err = parseQevalInt(string(rot)); err != nil {
		return nil, fmt.Errorf("last rotation of %s: %w", operator, err)
	}
	return &st, nil
}

// FetchValopers enumerates the on-chain valoper registry and returns each
// operator's profile, with its state when vm/qeval answers. It pages the
// home render for operators, then reads each operator's detail render and
// state: three queries per valoper, so call it on a slow timer, never in the
// snapshot hot path. A profile whose render fails or does not parse is left
// out, its operator in Unread; a profile is the operator's the registry lists.
func (c *Client) FetchValopers(ctx context.Context) (ValoperRead, error) {
	// 1. Page the home render → operator addresses.
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
	// flaky page is still useful.
	if len(operators) == 0 && pageErr != nil {
		return ValoperRead{}, pageErr
	}

	// 2. Read each operator's profile and state, with bounded concurrency.
	type result struct {
		op  string
		v   Valoper
		why string // set when the profile was not read
	}
	out := make(chan result)
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for op := range operators {
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			res := result{op: op}
			md, err := c.qrender(ctx, valopersRealm, op)
			switch {
			case err != nil:
				res.why = err.Error()
			default:
				var ok bool
				if res.v, ok = parseValoperDetail(md); !ok {
					res.why = "its render does not parse"
					break
				}
				res.v.Operator = op
				if res.v.State, err = c.valoperState(ctx, op); err != nil {
					res.v.StateError = err.Error()
				}
			}
			select {
			case out <- res:
			case <-ctx.Done():
			}
		})
	}
	go func() { wg.Wait(); close(out) }()

	read := ValoperRead{Unread: map[string]string{}}
	for res := range out {
		if res.why != "" {
			read.Unread[res.op] = res.why
			continue
		}
		read.Profiles = append(read.Profiles, res.v)
	}
	return read, nil
}

// ValoperRead is a read of the valopers registry: the profiles read, and the
// operators it lists whose profile was not, with why.
type ValoperRead struct {
	Profiles []Valoper
	Unread   map[string]string
}
