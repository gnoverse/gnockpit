//go:build page

package web

import (
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/gnoverse/gnockpit/node"
)

// ---- Scenarios

// valPageNobody is a well-formed address gnockpit knows nothing of.
var valPageNobody = preimageAddress("nobody")

// valPageReadBound is how long the page waits for a validator's report.
const valPageReadBound = 10 * time.Second

// valPageNetworkOf returns a builder of setChangesNetwork(t, unit), for a
// table whose every run builds its own scenario.
func valPageNetworkOf(unit int64) func(t *testing.T) *scenario {
	return func(t *testing.T) *scenario {
		t.Helper()
		return setChangesNetwork(t, unit)
	}
}

// valPageWithProfile is sc with bravo's valopers profile given description.
func valPageWithProfile(sc *scenario, description string) *scenario {
	p := sc.valopers[opBravo]
	p.Description = description
	sc.valopers[opBravo] = p
	return sc
}

// ---- Page

// valPageValue is the modal's i-th profile value.
func (e *pageEnv) valPageValue(i int) playwright.Locator {
	return e.Page.Locator("#val-body .kv .v").Nth(i)
}

// valPageTimelineButton is the Validators card's button to its Timeline
// view.
func (e *pageEnv) valPageTimelineButton() playwright.Locator {
	return e.Page.Locator("#validators-view").GetByRole(*playwright.AriaRoleButton, playwright.LocatorGetByRoleOptions{Name: "Timeline"})
}

// valPageDocumentLoads counts the documents the page loads from the call on: a
// page opened in place loads none.
func (e *pageEnv) valPageDocumentLoads() func() int {
	var mu sync.Mutex
	loads := 0
	e.Page.OnRequest(func(r playwright.Request) {
		if r.ResourceType() == "document" {
			mu.Lock()
			defer mu.Unlock()
			loads++
		}
	})
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		return loads
	}
}

// valPageExpectURL waits until the page's URL is the server's path.
func (e *pageEnv) valPageExpectURL(path string) {
	e.T.Helper()
	e.must(e.Expect.Page(e.Page).ToHaveURL(e.ts.URL + path))
}

// valPageHistoryLength reads the page's history.length.
func (e *pageEnv) valPageHistoryLength() int {
	e.T.Helper()
	v, err := e.Page.Evaluate(`() => history.length`)
	if err != nil {
		e.T.Fatalf("read history.length: %v", err)
	}
	n, ok := v.(int)
	if !ok {
		e.T.Fatalf("history.length = %T %v, want an int", v, v)
	}
	return n
}

// valPageMarkdownTree reads the rendered description of the modal's
// profile, one line per block: an element is its tag, classes, a link's href
// and a list's start, then its children in parentheses; text is quoted,
// adjacent text nodes as one; a link's ↗ arrow is left out.
func (e *pageEnv) valPageMarkdownTree() []string {
	e.T.Helper()
	v, err := e.Page.Locator("#val-body .kv .v.val-text").Evaluate(`root => {
		const children = (n) => {
			const out = []
			let text = null
			const flush = () => {
				if (text !== null) out.push(JSON.stringify(text))
				text = null
			}
			for (const c of n.childNodes) {
				if (c.nodeType === Node.TEXT_NODE) {
					text = (text || '') + c.data
					continue
				}
				flush()
				if (!c.matches('span.ext-arrow')) out.push(element(c))
			}
			flush()
			return out.join(' ')
		}
		const element = (el) => {
			let s = el.localName
			if (el.classList.length) s += '.' + [...el.classList].join('.')
			if (el.localName === 'a') s += '[' + el.getAttribute('href') + ']'
			if (el.hasAttribute('start')) s += '[start=' + el.getAttribute('start') + ']'
			return s + '(' + children(el) + ')'
		}
		return [...root.children].map(element)
	}`, nil)
	if err != nil {
		e.T.Fatalf("read the description: %v", err)
	}
	items, ok := v.([]any)
	if !ok {
		e.T.Fatalf("description tree = %T, want a list", v)
	}
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = fmt.Sprint(it)
	}
	return out
}

// valPageNextHead is setChangesNetwork(t, 1)'s snapshot one block later,
// bravo's power one higher: the dashboard re-renders its validators.
func valPageNextHead(t *testing.T) *node.Snapshot {
	t.Helper()
	snap := setChangesNetwork(t, 1).snap
	snap.Status.SyncInfo.LatestBlockHeight = strconv.Itoa(pageHeight + 1)
	for i := range snap.Validators {
		if snap.Validators[i].Address == valBravo.address() {
			snap.Validators[i].VotingPower = "11"
		}
	}
	return snap
}
