package push

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/ranking"
)

var discordAt = time.Date(2026, 11, 26, 9, 40, 0, 0, time.UTC)

const discordPublicURL = "https://gnockpit.example.com"

// discordOpen is the line that opens the dashboard at discordPublicURL.
const discordOpen = "-# [Open in Gnockpit ↗](https://gnockpit.example.com)"

// marshalPayloads returns each payload as the JSON a webhook is posted.
func marshalPayloads(t *testing.T, payloads []discordPayload) []string {
	t.Helper()
	out := make([]string, len(payloads))
	for i, p := range payloads {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = string(b)
	}
	return out
}

func TestDiscordAlertPayloads(t *testing.T) {
	// payload is the JSON of one payload of one embed; desc is JSON text, its
	// line breaks written \n, and "" leaves the description out.
	payload := func(title, desc string, color int) string {
		if desc != "" {
			desc = `,"description":"` + desc + `"`
		}
		return fmt.Sprintf(`{"embeds":[{"title":"%s"%s,"color":%d}],"allowed_mentions":{"parse":[]}}`, title, desc, color)
	}
	schedule := func(left time.Duration) Schedule {
		s := halt(900, 1000)
		s.Halt.MinVersion = "v1.8.0"
		s.TimeLeft = func(int64) (time.Duration, bool) { return left, true }
		return s
	}
	joined := valsetChangeAlert(blocks.ValsetChange{
		Height: 104, Time: discordAt, SizeBefore: 3, SizeAfter: 4,
		Added: []blocks.Member{{Address: "g1mithrandir", Power: 1}},
	}, func(string) string { return "Mithrandir" }, discordAt)
	const root = discordPublicURL
	const open = `\n` + discordOpen
	for _, tc := range []struct {
		name  string
		chain string
		pub   string
		a     Alert
		want  string
	}{
		{
			"missing blocks", "gnoland-1", root,
			Alert{Type: AlertValidatorMissingBlocks, Firing: true, Title: "Validator missing blocks", Body: missedBody("Mithrandir", 10)},
			payload("🚨 gnoland-1 — Validator missing blocks", `**Mithrandir** missed **10** blocks in a row`+open, 16273737),
		},
		{
			"back online", "gnoland-1", root,
			Alert{Type: AlertValidatorMissingBlocks, Title: "Validator back online", Body: signingBody("Mithrandir", 3)},
			payload("✅ gnoland-1 — Validator back online", `**Mithrandir** is signing again (**3** in a row)`+open, 4176208),
		},
		{
			"chain stuck", "gnoland-1", root,
			Alert{Type: AlertChainStuck, Firing: true, Title: "Chain stuck", Body: stuckBody(60)},
			payload("🚨 gnoland-1 — Chain stuck", `No new block for **60+ seconds**`+open, discordRed),
		},
		{
			"chain resumed", "gnoland-1", root,
			Alert{Type: AlertChainStuck, Title: "Chain resumed", Body: resumedBody(1234)},
			payload("✅ gnoland-1 — Chain resumed", `New block at height **1234**`+open, discordGreen),
		},
		{
			"halt scheduled", "gnoland-1", root,
			Alert{Type: AlertHaltScheduled, Firing: true, Notice: true, Title: "Coordinated halt scheduled at #1000", Body: haltBody(schedule(2*time.Hour), "")},
			payload("📣 gnoland-1 — Coordinated halt scheduled at #1000", `Expected in about **2 h**. Minimum version **v1.8.0**.`+open, 5809919),
		},
		{
			"halt approaching", "gnoland-1", root,
			Alert{Type: AlertHaltApproaching, Firing: true, Notice: true, Title: "Coordinated halt at #1000 in 98 blocks", Body: haltBody(schedule(10*time.Minute), "left")},
			payload("📣 gnoland-1 — Coordinated halt at #1000 in 98 blocks", `About **10 min** left. Minimum version **v1.8.0**.`+open, discordBlue),
		},
		{
			"halt reached", "gnoland-1", root,
			Alert{Type: AlertChainStuck, Firing: true, Notice: true, Title: "Coordinated halt reached at #1000", Body: Body{plain("The chain stopped at the scheduled height, as planned.")}},
			payload("📣 gnoland-1 — Coordinated halt reached at #1000", `The chain stopped at the scheduled height, as planned.`+open, discordBlue),
		},
		{
			"valset change, a blank line before the link", "gnoland-1", root, joined,
			payload("📣 gnoland-1 — Validator set changed at #104", `**Joined (1)**\nMithrandir (power 1)\n\n**Validator set size (+1)**\n3 → 4\n`+open, discordBlue),
		},
		{
			"valset change without a public URL, ending at its last line", "gnoland-1", "", joined,
			payload("📣 gnoland-1 — Validator set changed at #104", `**Joined (1)**\nMithrandir (power 1)\n\n**Validator set size (+1)**\n3 → 4`, discordBlue),
		},
		{
			"link joins the public URL", "gnoland-1", "https://x/",
			Alert{Type: AlertRanking, Firing: true, Notice: true, Title: "Ranking", Body: Body{plain("b")}, Link: "/?view=ranking"},
			payload("📣 gnoland-1 — Ranking", `b\n-# [Open in Gnockpit ↗](https://x/?view=ranking)`, discordBlue),
		},
		{
			"parentheses in the public URL, percent-encoded", "gnoland-1", "https://x/a(b)",
			Alert{Type: AlertRanking, Firing: true, Notice: true, Title: "Ranking", Body: Body{plain("b")}, Link: "/?view=ranking"},
			payload("📣 gnoland-1 — Ranking", `b\n-# [Open in Gnockpit ↗](https://x/a%28b%29/?view=ranking)`, discordBlue),
		},
		{
			"no public URL, the body alone", "gnoland-1", "",
			Alert{Type: AlertChainStuck, Firing: true, Title: "Chain stuck", Body: stuckBody(60)},
			payload("🚨 gnoland-1 — Chain stuck", `No new block for **60+ seconds**`, discordRed),
		},
		{
			"empty body, the link line alone", "gnoland-1", root,
			Alert{Type: AlertHaltScheduled, Firing: true, Notice: true, Title: "Coordinated halt scheduled at #1000"},
			payload("📣 gnoland-1 — Coordinated halt scheduled at #1000", discordOpen, discordBlue),
		},
		{
			"empty body and no public URL, no description", "gnoland-1", "",
			Alert{Type: AlertHaltScheduled, Firing: true, Notice: true, Title: "Coordinated halt scheduled at #1000"},
			payload("📣 gnoland-1 — Coordinated halt scheduled at #1000", "", discordBlue),
		},
		{
			"no chain name", "", root,
			Alert{Type: AlertValidatorMissingBlocks, Firing: true, Title: "Validator missing blocks", Body: missedBody("Mithrandir", 10)},
			payload("🚨 Validator missing blocks", `**Mithrandir** missed **10** blocks in a row`+open, discordRed),
		},
		{
			"title as written", "gnoland-1", root,
			Alert{Type: AlertChainStuck, Firing: true, Title: "<@1> & co", Body: Body{plain("b")}},
			payload(`🚨 gnoland-1 — \u003c@1\u003e \u0026 co`, `b`+open, discordRed),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := marshalPayloads(t, discordAlert(tc.chain, tc.pub, tc.a))
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("discordAlert(...) = %q, want [%q]", got, tc.want)
			}
		})
	}
}

func TestDiscordShowsMentionsAsWritten(t *testing.T) {
	a := Alert{Type: AlertValidatorMissingBlocks, Firing: true, Title: "Validator missing blocks", Body: missedBody("@everyone <@1>", 10)}
	payloads := discordAlert("gnoland-1", discordPublicURL, a)
	want := `**\@everyone \<\@1>** missed **10** blocks in a row` + "\n" + discordOpen
	if len(payloads) != 1 || len(payloads[0].Embeds) != 1 || payloads[0].Embeds[0].Description != want {
		t.Fatalf("discordAlert(mentions) = %+v, want one embed described %q", payloads, want)
	}
	for _, s := range marshalPayloads(t, payloads) {
		if strings.Contains(s, zwsp) {
			t.Errorf("payload %q holds a zero-width space, want none", s)
		}
	}
}

// payloadSize counts the characters Discord limits at 6,000 per message, in
// UTF-8 bytes: the titles, the descriptions, and the fields' names and values.
func payloadSize(p discordPayload) int {
	n := 0
	for _, e := range p.Embeds {
		n += len(e.Title) + len(e.Description)
		for _, f := range e.Fields {
			n += len(f.Name) + len(f.Value)
		}
	}
	return n
}

// discordDescriptions checks payloads against Discord's limits, their title
// on the first embed only and color on every embed, and returns their
// descriptions in order.
func discordDescriptions(t *testing.T, payloads []discordPayload, color int) []string {
	t.Helper()
	var descriptions []string
	first := true
	for pi, p := range payloads {
		if n := payloadSize(p); n > 6000 {
			t.Errorf("payload %d is %d bytes, want at most 6000", pi, n)
		}
		if len(p.Embeds) > 10 {
			t.Errorf("payload %d has %d embeds, want at most 10", pi, len(p.Embeds))
		}
		for ei, e := range p.Embeds {
			if len(e.Description) > 4096 {
				t.Errorf("payload %d embed %d description is %d bytes, want at most 4096", pi, ei, len(e.Description))
			}
			if (e.Title != "") != first {
				t.Errorf("payload %d embed %d title = %q, want it on the first embed only", pi, ei, e.Title)
			}
			if e.Color != color {
				t.Errorf("payload %d embed %d color = %d, want %d", pi, ei, e.Color, color)
			}
			descriptions = append(descriptions, e.Description)
			first = false
		}
	}
	return descriptions
}

// longBody is 300 lines of 30 bytes each.
func longBody() string {
	lines := make([]string, 300)
	for i := range lines {
		lines[i] = fmt.Sprintf("%029d", i) + "x"
	}
	return strings.Join(lines, "\n")
}

func TestDiscordAlertContinuesALongBody(t *testing.T) {
	body := longBody()
	payloads := discordAlert("gnoland-1", discordPublicURL, Alert{Firing: true, Title: "Chain stuck", Body: Body{plain(body)}})
	if len(payloads) < 2 {
		t.Fatalf("got %d payloads, want several", len(payloads))
	}
	descriptions := discordDescriptions(t, payloads, discordRed)
	if got, want := strings.Join(descriptions, "\n"), body+"\n"+discordOpen; got != want {
		t.Errorf("descriptions joined = %d bytes, want the body and the link line's %d", len(got), len(want))
	}
}

func TestDiscordAlertSplitsALongValsetChange(t *testing.T) {
	c := blocks.ValsetChange{Height: 104, Time: discordAt, SizeBefore: 3, SizeAfter: 303}
	for i := range 300 {
		c.Added = append(c.Added, blocks.Member{Address: fmt.Sprintf("validator-%03d", i), Power: 1})
	}
	a := valsetChangeAlert(c, func(addr string) string { return addr }, discordAt)
	payloads := discordAlert("gnoland-1", discordPublicURL, a)
	if len(payloads) < 2 {
		t.Fatalf("got %d payloads, want several", len(payloads))
	}
	descriptions := discordDescriptions(t, payloads, discordBlue)
	for i, d := range descriptions {
		for _, l := range strings.Split(d, "\n") {
			if n := strings.Count(l, "**"); n%2 != 0 {
				t.Errorf("description %d line %q holds %d \"**\", want an even count", i, l, n)
			}
		}
	}
	if got, want := strings.Join(descriptions, "\n"), markdownText(a.Body)+"\n\n"+discordOpen; got != want {
		t.Errorf("descriptions joined = %d bytes, want the body, a blank line and the link line's %d", len(got), len(want))
	}
}

// TestDiscordAlertPacksTheBodyAsEscaped checks that a body within an embed's
// limit as written, but over it once escaped, continues in a further embed:
// the packer counts the escaped bytes.
func TestDiscordAlertPacksTheBodyAsEscaped(t *testing.T) {
	lines := make([]string, 800)
	for i := range lines {
		lines[i] = "@://"
	}
	body := strings.Join(lines, "\n")
	escaped := strings.ReplaceAll(body, "@://", `\@\://`)
	if len(body) > discordDescriptionLimit || len(escaped) <= discordDescriptionLimit {
		t.Fatalf("body is %d bytes and %d escaped, want within %d and over it", len(body), len(escaped), discordDescriptionLimit)
	}
	payloads := discordAlert("gnoland-1", "", Alert{Firing: true, Title: "Chain stuck", Body: Body{plain(body)}})
	descriptions := discordDescriptions(t, payloads, discordRed)
	if len(descriptions) < 2 {
		t.Errorf("got %d embeds, want the escaped body over several", len(descriptions))
	}
	if got := strings.Join(descriptions, "\n"); got != escaped {
		t.Errorf("descriptions joined = %d bytes, want the escaped body's %d", len(got), len(escaped))
	}
}

func TestDiscordPayloadsAllowNoMentions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		payloads []discordPayload
	}{
		{"alert", discordAlert("gnoland-1", discordPublicURL, Alert{Firing: true, Title: "Chain stuck", Body: Body{plain(longBody())}})},
		{"ranking", discordRanking("gnoland-1", discordPublicURL, rankingDigest(rankingRows(400)...))},
		{"test send", discordTest("hello")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i, s := range marshalPayloads(t, tc.payloads) {
				if !strings.HasSuffix(s, `,"allowed_mentions":{"parse":[]}}`) {
					t.Errorf("payload %d ends %q, want it to allow no mention", i, s[max(0, len(s)-60):])
				}
			}
		})
	}
}

func TestDiscordPackCutsALongLine(t *testing.T) {
	line := strings.Repeat("é", 10000)
	payloads := packEmbeds(discordEmbed{Color: discordBlue}, []discordLine{{text: line}})
	var joined strings.Builder
	for _, p := range payloads {
		for _, e := range p.Embeds {
			if len(e.Description) > 4096 || !utf8.ValidString(e.Description) {
				t.Errorf("description is %d bytes, valid UTF-8 %v, want at most 4096 and valid", len(e.Description), utf8.ValidString(e.Description))
			}
			joined.WriteString(e.Description)
		}
	}
	if joined.String() != line {
		t.Errorf("the pieces joined are %d bytes, want the line's %d", joined.Len(), len(line))
	}
}

func TestDiscordPackFencesRunsOfCodeLines(t *testing.T) {
	payloads := packEmbeds(discordEmbed{Title: "T"}, []discordLine{
		{text: "intro"}, {text: "a", code: true}, {text: "b", code: true}, {text: "outro"},
	})
	if len(payloads) != 1 || len(payloads[0].Embeds) != 1 {
		t.Fatalf("packEmbeds(...) = %+v, want one embed", payloads)
	}
	want := "intro\n```ansi\na\nb\n```\noutro"
	if got := payloads[0].Embeds[0].Description; got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

func TestDiscordPackFencedLinesStayWithinTheLimit(t *testing.T) {
	var lines []discordLine
	for range 50 {
		lines = append(lines, discordLine{text: strings.Repeat("c", 300), code: true})
	}
	for _, p := range packEmbeds(discordEmbed{}, lines) {
		for _, e := range p.Embeds {
			if len(e.Description) > 4096 {
				t.Errorf("description is %d bytes, want at most 4096", len(e.Description))
			}
		}
	}
}

func TestDiscordPackWithoutLinesIsTheHeadAlone(t *testing.T) {
	payloads := packEmbeds(discordEmbed{Title: "T", Color: discordBlue}, nil)
	if len(payloads) != 1 || len(payloads[0].Embeds) != 1 || payloads[0].Embeds[0].Title != "T" {
		t.Errorf("packEmbeds(head, nil) = %+v, want one embed titled T", payloads)
	}
}

// recordedPost is one request a recording transport saw.
type recordedPost struct {
	method, url, contentType, body string
}

// recordDiscord swaps http.DefaultTransport for one that records each request
// and answers 204, as Discord's webhook does. It swaps a process-wide value,
// so its callers must not run in parallel.
func recordDiscord(t *testing.T) *[]recordedPost {
	t.Helper()
	var posts []recordedPost
	prev := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		posts = append(posts, recordedPost{r.Method, r.URL.String(), r.Header.Get("Content-Type"), string(b)})
		return &http.Response{StatusCode: http.StatusNoContent, Status: "204 No Content", Body: io.NopCloser(bytes.NewReader(nil)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = prev })
	return &posts
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const discordTargetURL = "discord://SECRETTOKEN@123456789?username=gnockpit-bot&avatar=https%3A%2F%2Fx%2Fa.png"

func TestDiscordTargetPostsThroughJSONMode(t *testing.T) {
	posts := recordDiscord(t)
	m := newTestManager(t, false)
	logs := captureLog(t)
	if err := m.SetNotifyConfig(everyAlert(discordTargetURL)); err != nil {
		t.Fatal(err)
	}
	m.SetPublicURL(discordPublicURL)
	m.NotifyAlert(Alert{Type: AlertValidatorMissingBlocks, Firing: true, Title: "Validator missing blocks", Body: missedBody("Mithrandir", 10)})
	want := recordedPost{
		method: "POST", url: "https://discord.com/api/webhooks/123456789/SECRETTOKEN", contentType: "application/json",
		body: `{"username":"gnockpit-bot","avatar_url":"https://x/a.png","embeds":[{"title":"🚨 Validator missing blocks","description":"**Mithrandir** missed **10** blocks in a row\n-# [Open in Gnockpit ↗](https://gnockpit.example.com)","color":16273737}],"allowed_mentions":{"parse":[]}}`,
	}
	if len(*posts) != 1 || (*posts)[0] != want {
		t.Errorf("posts = %+v, want [%+v]", *posts, want)
	}
	if n := strings.Count(logs.String(), "notify: "); n != 1 {
		t.Errorf("log = %q, want only the config's line", logs.String())
	}
}

func TestDiscordTestSendIsAnEmbed(t *testing.T) {
	posts := recordDiscord(t)
	m := managerWith(t, "discord://SECRETTOKEN@123456789")
	if err := m.SendTestNotify(0, "hello"); err != nil {
		t.Fatal(err)
	}
	want := `{"embeds":[{"description":"hello","color":5809919}],"allowed_mentions":{"parse":[]}}`
	if len(*posts) != 1 || (*posts)[0].body != want {
		t.Errorf("posts = %+v, want one with body %s", *posts, want)
	}
}

func TestDiscordTestSendKeepsTheMessageVerbatim(t *testing.T) {
	posts := recordDiscord(t)
	m := managerWith(t, "discord://SECRETTOKEN@123456789")
	if err := m.SendTestNotify(0, "@here hello_world"); err != nil {
		t.Fatal(err)
	}
	want := `{"embeds":[{"description":"@here hello_world","color":5809919}],"allowed_mentions":{"parse":[]}}`
	if len(*posts) != 1 || (*posts)[0].body != want {
		t.Errorf("posts = %+v, want one with body %s", *posts, want)
	}
}

func TestSenderURL(t *testing.T) {
	got, hook, err := senderURL(discordTargetURL)
	if err != nil {
		t.Fatal(err)
	}
	if hook == nil || hook.username != "gnockpit-bot" || hook.avatarURL != "https://x/a.png" {
		t.Errorf("hook = %+v, want gnockpit-bot and https://x/a.png", hook)
	}
	if !strings.Contains(got, "json=yes") || !strings.HasPrefix(got, "discord://SECRETTOKEN@123456789?") {
		t.Errorf("senderURL(...) = %q, want the discord URL with json=yes", got)
	}
	upper, hook, err := senderURL("DISCORD://SECRETTOKEN@123456789?username=u")
	if err != nil || hook == nil || hook.username != "u" || !strings.Contains(upper, "json=yes") {
		t.Errorf("senderURL(DISCORD://…) = %q, %+v, %v, want a Discord URL with json=yes and its hook", upper, hook, err)
	}
	for _, raw := range []string{"generic+http://example.com/hook", "slack://a/b/c"} {
		got, hook, err := senderURL(raw)
		if got != raw || hook != nil || err != nil {
			t.Errorf("senderURL(%q) = %q, %v, %v, want it unchanged, no hook", raw, got, hook, err)
		}
	}
	if _, _, err := senderURL("discord://a b@c%zz"); err == nil {
		t.Error("senderURL(unparsable) = nil error, want one")
	}
}

func TestSenderURLOverridesAnyJSONKey(t *testing.T) {
	got, _, err := senderURL("discord://SECRETTOKEN@123456789?JSON=No&Json=false&username=u")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if q := u.Query(); len(q["json"]) != 1 || q.Get("json") != "yes" || len(q) != 2 {
		t.Errorf("senderURL(...) query = %v, want json=yes once, any other json key gone, username kept", q)
	}
}

func TestDiscordPackLimitsAreExact(t *testing.T) {
	head := discordEmbed{}
	a := func(n int) discordLine { return discordLine{text: strings.Repeat("a", n)} }
	embeds := func(ps []discordPayload) (payloads, total int) {
		for _, p := range ps {
			total += len(p.Embeds)
		}
		return len(ps), total
	}
	t.Run("description of 4096 is one embed", func(t *testing.T) {
		ps := packEmbeds(head, []discordLine{a(2047), a(2048)})
		if p, e := embeds(ps); p != 1 || e != 1 {
			t.Errorf("packEmbeds(4096 bytes) = %d payloads, %d embeds, want 1, 1", p, e)
		}
	})
	t.Run("description of 4097 is two embeds", func(t *testing.T) {
		ps := packEmbeds(head, []discordLine{a(2048), a(2048)})
		if p, e := embeds(ps); p != 1 || e != 2 {
			t.Errorf("packEmbeds(4097 bytes) = %d payloads, %d embeds, want 1, 2", p, e)
		}
	})
	t.Run("code line of 4084 bytes is one embed of 4096", func(t *testing.T) {
		ps := packEmbeds(head, []discordLine{{text: strings.Repeat("c", 4084), code: true}})
		if p, e := embeds(ps); p != 1 || e != 1 {
			t.Fatalf("packEmbeds(code line of 4084 bytes) = %d payloads, %d embeds, want 1, 1", p, e)
		}
		if n := len(ps[0].Embeds[0].Description); n != 4096 {
			t.Errorf("packEmbeds(code line of 4084 bytes) description = %d bytes, want 4096", n)
		}
	})
	for n := 4085; n <= 4096; n++ {
		t.Run(fmt.Sprintf("code line of %d bytes", n), func(t *testing.T) {
			ps := packEmbeds(head, []discordLine{{text: strings.Repeat("c", n), code: true}})
			for _, p := range ps {
				for _, e := range p.Embeds {
					if len(e.Description) > 4096 {
						t.Errorf("packEmbeds(code line of %d bytes) has a description of %d bytes, want at most 4096", n, len(e.Description))
					}
				}
			}
		})
	}
	// The title (100 bytes) and 5,900 bytes of descriptions make 6,000.
	titled := discordEmbed{Title: strings.Repeat("t", 100)}
	t.Run("payload of 6000 is one payload", func(t *testing.T) {
		ps := packEmbeds(titled, []discordLine{a(2047), a(2048), a(900), a(903)})
		if p, e := embeds(ps); p != 1 || e != 2 {
			t.Errorf("packEmbeds(6000 bytes) = %d payloads, %d embeds, want 1, 2", p, e)
		}
	})
	t.Run("a line joining an embed spills past 6000", func(t *testing.T) {
		ps := packEmbeds(titled, []discordLine{a(2047), a(2048), a(900), a(903), a(1)})
		if p, _ := embeds(ps); p != 2 {
			t.Errorf("packEmbeds(6002 bytes) = %d payloads, want 2", p)
		}
	})
	t.Run("a line starting an embed fits at 6000", func(t *testing.T) {
		ps := packEmbeds(titled, []discordLine{a(2047), a(2048), a(1804)})
		if p, e := embeds(ps); p != 1 || e != 2 {
			t.Errorf("packEmbeds(6000 bytes) = %d payloads, %d embeds, want 1, 2", p, e)
		}
	})
	t.Run("a line starting an embed spills at 6001", func(t *testing.T) {
		ps := packEmbeds(titled, []discordLine{a(2047), a(2048), a(1805)})
		if p, _ := embeds(ps); p != 2 {
			t.Errorf("packEmbeds(6001 bytes) = %d payloads, want 2", p)
		}
	})
	// The title (100 bytes) and the fields (100 bytes) leave 5,800 bytes of
	// descriptions; the fields count in every payload, as each could be the
	// last.
	fields := []discordField{{Name: strings.Repeat("n", 10), Value: strings.Repeat("v", 90)}}
	fielded := discordEmbed{Title: strings.Repeat("t", 100), Fields: fields}
	// fieldsOnLast checks that the fields are on the last embed of ps only.
	fieldsOnLast := func(t *testing.T, ps []discordPayload) {
		t.Helper()
		for pi, p := range ps {
			for ei, e := range p.Embeds {
				var want []discordField
				if pi == len(ps)-1 && ei == len(p.Embeds)-1 {
					want = fields
				}
				if !slices.Equal(e.Fields, want) {
					t.Errorf("payload %d embed %d fields = %+v, want %+v", pi, ei, e.Fields, want)
				}
			}
		}
	}
	t.Run("fields fit at 6000", func(t *testing.T) {
		ps := packEmbeds(fielded, []discordLine{a(2047), a(2048), a(1704)})
		if p, e := embeds(ps); p != 1 || e != 2 {
			t.Errorf("packEmbeds(6000 bytes with fields) = %d payloads, %d embeds, want 1, 2", p, e)
		}
		fieldsOnLast(t, ps)
	})
	t.Run("fields spill at 6001 and go on the last payload", func(t *testing.T) {
		ps := packEmbeds(fielded, []discordLine{a(2047), a(2048), a(1705)})
		if p, _ := embeds(ps); p != 2 {
			t.Fatalf("packEmbeds(6001 bytes with fields) = %d payloads, want 2", p)
		}
		fieldsOnLast(t, ps)
	})
	// The second payload holds the fields (100 bytes), an embed of 1,705 and
	// 2,390 bytes (4,096) and one more line: 1,804 bytes make 6,000, 1,805 go
	// on a third payload.
	t.Run("fields fit at 6000 in a later payload", func(t *testing.T) {
		ps := packEmbeds(fielded, []discordLine{a(2047), a(2048), a(1705), a(2390), a(1804)})
		if p, _ := embeds(ps); p != 2 {
			t.Fatalf("packEmbeds(a second payload of 6000 bytes with fields) = %d payloads, want 2", p)
		}
		if n := payloadSize(ps[1]); n != 6000 {
			t.Errorf("second payload = %d bytes with its fields, want 6000", n)
		}
		fieldsOnLast(t, ps)
	})
	t.Run("fields spill at 6001 in a later payload", func(t *testing.T) {
		ps := packEmbeds(fielded, []discordLine{a(2047), a(2048), a(1705), a(2390), a(1805)})
		if p, _ := embeds(ps); p != 3 {
			t.Fatalf("packEmbeds(a second payload of 6001 bytes with fields) = %d payloads, want 3", p)
		}
		fieldsOnLast(t, ps)
	})
}

// packedDescriptions returns the descriptions of payloads, embed by embed.
func packedDescriptions(payloads []discordPayload) [][]string {
	out := make([][]string, len(payloads))
	for i, p := range payloads {
		for _, e := range p.Embeds {
			out[i] = append(out[i], e.Description)
		}
	}
	return out
}

func TestDiscordPackOpensNoEmbedOnABlankLine(t *testing.T) {
	a := func(n int) discordLine { return discordLine{text: strings.Repeat("a", n)} }
	t.Run("at a payload's edge", func(t *testing.T) {
		// The title (100 bytes) and the first three lines make 6,000 bytes:
		// joining the blank line makes 6,001, so it is left out, as an embed
		// of it alone would show nothing; the next line goes on.
		ps := packEmbeds(discordEmbed{Title: strings.Repeat("t", 100)}, []discordLine{a(2047), a(2048), a(1804), {}, a(1)})
		want := [][]string{{strings.Repeat("a", 2047) + "\n" + strings.Repeat("a", 2048), strings.Repeat("a", 1804)}, {"a"}}
		if got := packedDescriptions(ps); !slices.EqualFunc(got, want, slices.Equal) {
			t.Errorf("packEmbeds(...) descriptions = %d bytes each, want %d bytes each", descriptionSizes(got), descriptionSizes(want))
		}
	})
	t.Run("first", func(t *testing.T) {
		ps := packEmbeds(discordEmbed{Title: "T"}, []discordLine{{}, a(1)})
		if got, want := packedDescriptions(ps), [][]string{{"a"}}; !slices.EqualFunc(got, want, slices.Equal) {
			t.Errorf("packEmbeds(a blank line, a) descriptions = %q, want %q", got, want)
		}
	})
}

// descriptionSizes returns the size of each description of descriptions.
func descriptionSizes(descriptions [][]string) [][]int {
	out := make([][]int, len(descriptions))
	for i, p := range descriptions {
		for _, d := range p {
			out[i] = append(out[i], len(d))
		}
	}
	return out
}

func TestDiscordPackCutsInvalidUTF8(t *testing.T) {
	for _, line := range []string{strings.Repeat("\x80", 4089), "a" + strings.Repeat("\x80", 4088), strings.Repeat("\x80", 10000)} {
		var joined strings.Builder
		for _, p := range packEmbeds(discordEmbed{}, []discordLine{{text: line}}) {
			for _, e := range p.Embeds {
				if len(e.Description) > 4096 {
					t.Errorf("description is %d bytes, want at most 4096", len(e.Description))
				}
				joined.WriteString(e.Description)
			}
		}
		// Pieces sharing an embed are joined by a line break.
		if got := strings.ReplaceAll(joined.String(), "\n", ""); got != line {
			t.Errorf("the pieces joined are %d bytes, want the line's %d", len(got), len(line))
		}
	}
}

func TestDiscordPackCutsOnRuneStarts(t *testing.T) {
	// The first cut, at 4,084 bytes, lands 3 bytes into a 4-byte rune.
	line := "a" + strings.Repeat("🚨", 1100)
	var joined strings.Builder
	for _, p := range packEmbeds(discordEmbed{}, []discordLine{{text: line}}) {
		for _, e := range p.Embeds {
			if !utf8.ValidString(e.Description) || len(e.Description) > 4096 {
				t.Errorf("description of %d bytes, valid UTF-8 %v, want at most 4096 and valid", len(e.Description), utf8.ValidString(e.Description))
			}
			joined.WriteString(e.Description)
		}
	}
	if got := strings.ReplaceAll(joined.String(), "\n", ""); got != line {
		t.Errorf("the pieces joined are %d bytes, want the line's %d", len(got), len(line))
	}
}

func TestShowChainOnADiscordTarget(t *testing.T) {
	posts := recordDiscord(t)
	m := newTestManager(t, false)
	captureLog(t)
	alerts := map[AlertType]AlertOptions{AlertChainStuck: {}, AlertRanking: {}}
	err := m.SetNotifyConfig(NotifyConfig{Targets: []NotifyRoute{
		{URL: "discord://TOKENA@111111111", Alerts: alerts},
		{URL: "discord://TOKENB@222222222", Alerts: alerts, HideChain: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	chainNamed(m)
	m.NotifyAlert(Alert{Type: AlertChainStuck, Firing: true, Title: "Chain stuck", Body: stuckBody(30)})
	const stuck = `{"embeds":[{"title":"🚨 %s","description":"No new block for **30+ seconds**","color":16273737}],"allowed_mentions":{"parse":[]}}`
	want := map[string]string{
		"https://discord.com/api/webhooks/111111111/TOKENA": fmt.Sprintf(stuck, "test-1 — Chain stuck"),
		"https://discord.com/api/webhooks/222222222/TOKENB": fmt.Sprintf(stuck, "Chain stuck"),
	}
	checkDiscordBodies(t, "alert", *posts, want)

	*posts = nil
	d := rankingDigest(ranking.DigestRow{Rank: 1, Name: "A", Score: "99.0", Change: "="})
	m.NotifyRanking("test-1:2026-11-26", d)
	titles := map[string]string{
		"https://discord.com/api/webhooks/111111111/TOKENA": "🏆 test-1 — Validator ranking · Nov 26",
		"https://discord.com/api/webhooks/222222222/TOKENB": "🏆 Validator ranking · Nov 26",
	}
	if len(*posts) != len(titles) {
		t.Fatalf("NotifyRanking made %d posts, want %d", len(*posts), len(titles))
	}
	for _, p := range *posts {
		var body struct {
			Embeds []struct{ Title string }
		}
		if err := json.Unmarshal([]byte(p.body), &body); err != nil || len(body.Embeds) == 0 {
			t.Fatalf("ranking post to %s = %s, want a payload with an embed (%v)", p.url, p.body, err)
		}
		if got := body.Embeds[0].Title; got != titles[p.url] {
			t.Errorf("ranking post to %s has the title %q, want %q", p.url, got, titles[p.url])
		}
	}
}

// checkDiscordBodies fails unless posts holds exactly one post per URL of
// want, with its body.
func checkDiscordBodies(t *testing.T, what string, posts []recordedPost, want map[string]string) {
	t.Helper()
	if len(posts) != len(want) {
		t.Fatalf("%s: %d posts, want %d", what, len(posts), len(want))
	}
	for _, p := range posts {
		if p.body != want[p.url] {
			t.Errorf("%s post to %s = %s, want %s", what, p.url, p.body, want[p.url])
		}
	}
}
