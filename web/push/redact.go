package push

import (
	"cmp"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// embeddedURLPattern matches a URL up to the next whitespace or unescaped
// double quote, capturing its scheme. A backslash escape, as Go's %q writes
// one, is consumed whole, so an escaped quote does not end the match.
var embeddedURLPattern = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*)://(?:\\.|[^\s"\\])*`)

// minSecretPieceLen is the length from which a configured URL piece is
// redacted wherever it appears. Shorter pieces (ports, user names such as
// "user", option keys) are rarely secrets and would blank out ordinary words.
const minSecretPieceLen = 6

// urlPieceSeparators split a configured URL into the pieces a Shoutrrr
// service may echo on their own, such as a bot token or a webhook ID.
const urlPieceSeparators = ":/@?&=#"

// secretReplacement replaces secret, wherever it appears, with label.
type secretReplacement struct{ secret, label string }

// redactNotifySecrets strips notification secrets from msg, a Shoutrrr error
// text, by three rules:
//   - each configured URL in urls becomes "<scheme>:<fingerprint>", as in
//     NotifyTargets;
//   - each piece of a configured URL, as written or percent-decoded, becomes
//     "[redacted]" from minSecretPieceLen characters on: some services echo a
//     secret outside any URL ("invalid telegram token <token>");
//   - every other URL keeps only its scheme: services build their own request
//     URLs from the configured secrets (Discord posts to
//     https://discord.com/api/webhooks/<id>/<token>), and Go's HTTP client
//     quotes that URL in network errors.
//
// Values are matched as written and as Go's %q quotes them, in a single pass
// that prefers the longest match, so a configured URL is labelled whole and
// no label is rewritten by a piece (the host "telegram" of a Telegram URL).
func redactNotifySecrets(msg string, urls []string) string {
	var repls []secretReplacement
	add := func(secret, label string) {
		repls = append(repls, secretReplacement{secret, label})
		quoted := strconv.Quote(secret)
		if inner := quoted[1 : len(quoted)-1]; inner != secret {
			repls = append(repls, secretReplacement{inner, label})
		}
	}
	for _, u := range urls {
		if u == "" {
			continue
		}
		add(u, notifyScheme(u)+":"+notifyFingerprint(u))
		for _, p := range secretPieces(u) {
			add(p, "[redacted]")
		}
	}
	// strings.Replacer tries its pairs in argument order at each position.
	slices.SortStableFunc(repls, func(a, b secretReplacement) int {
		return cmp.Compare(len(b.secret), len(a.secret))
	})
	pairs := make([]string, 0, 2*len(repls))
	for _, r := range repls {
		pairs = append(pairs, r.secret, r.label)
	}
	msg = strings.NewReplacer(pairs...).Replace(msg)
	return embeddedURLPattern.ReplaceAllString(msg, "${1}://[redacted]")
}

// secretPieces splits rawURL, past its scheme if it has one, into the pieces
// of at least minSecretPieceLen characters, each as written and
// percent-decoded. A decoded piece is split again, since a service may split
// a decoded value further (Teams splits its decoded user name on "@").
func secretPieces(rawURL string) []string {
	_, rest, found := strings.Cut(rawURL, "://")
	if !found {
		rest = rawURL
	}
	isSeparator := func(r rune) bool { return strings.ContainsRune(urlPieceSeparators, r) }
	var pieces []string
	for _, p := range strings.FieldsFunc(rest, isSeparator) {
		forms := []string{p}
		if decoded, err := url.PathUnescape(p); err == nil && decoded != p {
			forms = append(forms, decoded)
			if sub := strings.FieldsFunc(decoded, isSeparator); len(sub) > 1 {
				forms = append(forms, sub...)
			}
		}
		for _, f := range forms {
			if len(f) >= minSecretPieceLen {
				pieces = append(pieces, f)
			}
		}
	}
	return pieces
}
