package web

import (
	"bytes"
	"fmt"
	"strings"
)

// simpleAnalyticsSnippet is the embed documented at
// https://docs.simpleanalytics.com/script. The script is async so it never
// blocks first paint, and the no-JS fallback is an <img>, which is why the
// whole snippet goes at the end of <body> rather than in <head>: a <noscript>
// inside <head> may only hold link, style and meta.
const simpleAnalyticsSnippet = `<!-- Simple Analytics: privacy-first, no cookies, no personal data. -->
<script async src="https://scripts.simpleanalyticscdn.com/latest.js"></script>
<noscript><img src="https://queue.simpleanalyticscdn.com/noscript.gif" alt="" referrerpolicy="no-referrer-when-downgrade" /></noscript>`

// Analytics is a third-party analytics snippet spliced into the dashboard HTML
// as it is served.
//
// The zero value is "no analytics", and that is deliberately the default: a
// gnockpit usually watches its operator's own validator, so the dashboard must
// never report to anyone the operator did not name themselves.
type Analytics struct {
	// Provider is the canonical name, as accepted by --analytics.
	Provider string
	// snippet is the HTML injected at the end of <body>, without indentation.
	snippet string
}

// ParseAnalytics parses an --analytics flag value. An empty spec disables
// analytics and yields the zero Analytics; an unrecognised one is an error, so
// a typo fails at startup instead of quietly collecting nothing.
func ParseAnalytics(spec string) (Analytics, error) {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "":
		return Analytics{}, nil
	case "simple-analytics", "simpleanalytics":
		return Analytics{Provider: "simple-analytics", snippet: simpleAnalyticsSnippet}, nil
	}
	return Analytics{}, fmt.Errorf("unknown analytics provider %q: want \"simple-analytics\"", spec)
}

// Enabled reports whether a provider was configured.
func (a Analytics) Enabled() bool { return a.snippet != "" }

// inject returns page with the analytics snippet inserted just above its
// closing </body>. When analytics is off the original slice is returned
// untouched, so the common self-hosted case copies nothing; a page with no
// </body> is likewise returned unchanged rather than growing a snippet in some
// arbitrary place.
func (a Analytics) inject(page []byte) []byte {
	if !a.Enabled() {
		return page
	}
	closing := bytes.LastIndex(page, []byte("</body>"))
	if closing < 0 {
		return page
	}
	at, indent := injectPoint(page, closing)

	var block bytes.Buffer
	block.Grow(len(a.snippet) + 4*len(indent))
	for _, line := range strings.Split(a.snippet, "\n") {
		block.Write(indent)
		block.WriteString(line)
		block.WriteByte('\n')
	}

	out := make([]byte, 0, len(page)+block.Len())
	out = append(out, page[:at]...)
	out = append(out, block.Bytes()...)
	return append(out, page[at:]...)
}

// injectPoint locates where the snippet block starts and how far to indent it,
// given the offset of the closing </body>. When </body> sits alone on its own
// line, which is how index.html is written, the block is placed on the lines
// above it at the same depth. Otherwise it is spliced in flush, since there is
// no indentation to copy.
func injectPoint(page []byte, closing int) (at int, indent []byte) {
	at = closing
	for at > 0 && (page[at-1] == ' ' || page[at-1] == '\t') {
		at--
	}
	if at == 0 || page[at-1] != '\n' {
		return closing, nil
	}
	return at, page[at:closing]
}
