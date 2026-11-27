package push

import (
	"strings"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
)

// marked returns b's text with each bold span between "**", unescaped: a
// test reads a body's emphasis through it.
func marked(b Body) string {
	var sb strings.Builder
	for _, s := range b {
		if s.Bold {
			sb.WriteString("**" + s.Text + "**")
			continue
		}
		sb.WriteString(s.Text)
	}
	return sb.String()
}

func TestBodyString(t *testing.T) {
	b := Body{bold("Mithrandir"), plain(" missed "), bold("10"), plain(" blocks in a row")}
	if got, want := b.String(), "Mithrandir missed 10 blocks in a row"; got != want {
		t.Errorf("Body.String() = %q, want %q", got, want)
	}
	if got := Body(nil).String(); got != "" {
		t.Errorf("Body(nil).String() = %q, want \"\"", got)
	}
}

func TestAlertBodiesBoldTheirKeyFacts(t *testing.T) {
	twoHours := func(int64) (time.Duration, bool) { return 2 * time.Hour, true }
	s := halt(900, 1000)
	s.TimeLeft = twoHours
	noTimeLeft := halt(900, 1000)
	noVersion := halt(900, 1000)
	noVersion.TimeLeft = twoHours
	noVersion.Halt.MinVersion = ""
	neither := halt(900, 1000)
	neither.Halt.MinVersion = ""
	for _, tc := range []struct {
		name string
		body Body
		want string
	}{
		{"missed", missedBody("Mithrandir", 10), "**Mithrandir** missed **10** blocks in a row"},
		{"signing", signingBody("Mithrandir", 3), "**Mithrandir** is signing again (**3** in a row)"},
		{"stuck", stuckBody(30), "No new block for **30+ seconds**"},
		{"resumed", resumedBody(1001), "New block at height **1001**"},
		{"halt scheduled", haltBody(s, ""), "Expected in about **2 h**. Minimum version **v1.2.0**."},
		{"halt approaching", haltBody(s, "left"), "About **2 h** left. Minimum version **v1.2.0**."},
		{"halt without an estimate", haltBody(noTimeLeft, ""), "Minimum version **v1.2.0**."},
		{"halt without a version", haltBody(noVersion, ""), "Expected in about **2 h**."},
		{"halt with neither", haltBody(neither, ""), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := marked(tc.body); got != tc.want {
				t.Errorf("marked(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
	if got := haltBody(neither, ""); got != nil {
		t.Errorf("haltBody(neither) = %#v, want nil", got)
	}
}

func TestFormatAlertWritesTheBodyPlain(t *testing.T) {
	got := FormatAlert("test4", "", Alert{Firing: true, Title: "Validator missing blocks", Body: missedBody("@everyone", 10)})
	if want := "🚨 test4 — Validator missing blocks\n@" + zwsp + "everyone missed 10 blocks in a row"; got != want {
		t.Errorf("FormatAlert(bold body) = %q, want %q", got, want)
	}
}

func TestCutName(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"at the limit", strings.Repeat("é", 64), strings.Repeat("é", 64)},
		{"over the limit", strings.Repeat("é", 65), strings.Repeat("é", 63) + "…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cutName(tc.in, alertNameLimit); got != tc.want {
				t.Errorf("cutName(%q, %d) = %q, want %q", tc.in, alertNameLimit, got, tc.want)
			}
		})
	}
}

func TestAlertsCutALongName(t *testing.T) {
	long := strings.Repeat("é", 100)
	cut := strings.Repeat("é", 63) + "…"
	valset := valsetChangeAlert(blocks.ValsetChange{
		Height: 5, Time: discordAt, SizeBefore: 1, SizeAfter: 1,
		Added: []blocks.Member{{Address: "g1x", Power: 1}},
	}, func(string) string { return long }, discordAt)
	for _, tc := range []struct {
		name       string
		a          Alert
		discord    string
		textTarget string
	}{
		{
			"missing blocks",
			Alert{Firing: true, Title: "Validator missing blocks", Body: missedBody(long, 10)},
			"**" + cut + "** missed **10** blocks in a row",
			"🚨 gnoland-1 — Validator missing blocks\n" + cut + " missed 10 blocks in a row",
		},
		{
			"back online",
			Alert{Title: "Validator back online", Body: signingBody(long, 3)},
			"**" + cut + "** is signing again (**3** in a row)",
			"✅ gnoland-1 — Validator back online\n" + cut + " is signing again (3 in a row)",
		},
		{
			"set change", valset,
			"**Joined (1)**\n" + cut + " (power 1)",
			"📣 gnoland-1 — Validator set changed at #5\nJoined (1)\n" + cut + " (power 1)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payloads := discordAlert("gnoland-1", "", tc.a)
			if len(payloads) != 1 || len(payloads[0].Embeds) != 1 || payloads[0].Embeds[0].Description != tc.discord {
				t.Errorf("discordAlert(%s) = %+v, want one embed described %q", tc.name, payloads, tc.discord)
			}
			if got := FormatAlert("gnoland-1", "", tc.a); got != tc.textTarget {
				t.Errorf("FormatAlert(%s) = %q, want %q", tc.name, got, tc.textTarget)
			}
		})
	}
}

func TestPushPayloadIsPlain(t *testing.T) {
	got := payloadOf("test-1", Alert{Title: "Validator missing blocks", Body: missedBody("Mithrandir", 10)})
	if want := (pushPayload{Title: "test-1 - Validator missing blocks", Body: "Mithrandir missed 10 blocks in a row"}); got != want {
		t.Errorf("payloadOf(bold body) = %+v, want %+v", got, want)
	}
}

func TestFormatAlert(t *testing.T) {
	tests := []struct {
		name      string
		chainName string
		publicURL string
		alert     Alert
		want      string
	}{
		{
			name:      "firing alert with chain name",
			chainName: "test4",
			alert:     Alert{Firing: true, Title: "Chain stuck", Body: Body{plain("No new block for 30+ seconds")}},
			want:      "🚨 test4 — Chain stuck\nNo new block for 30+ seconds",
		},
		{
			name:      "recovery alert with chain name",
			chainName: "test4",
			alert:     Alert{Firing: false, Title: "Chain resumed", Body: Body{plain("New block at height 100")}},
			want:      "✅ test4 — Chain resumed\nNew block at height 100",
		},
		{
			name:      "firing alert without chain name",
			chainName: "",
			alert:     Alert{Firing: true, Title: "Chain stuck", Body: Body{plain("No new block for 30+ seconds")}},
			want:      "🚨 Chain stuck\nNo new block for 30+ seconds",
		},
		{
			name:      "with public URL",
			chainName: "test4",
			publicURL: "https://gnockpit.example.com",
			alert:     Alert{Firing: true, Title: "Chain stuck", Body: Body{plain("No new block for 30+ seconds")}},
			want:      "🚨 test4 — Chain stuck\nNo new block for 30+ seconds\nhttps://gnockpit.example.com",
		},
		{
			name:      "empty public URL omitted",
			chainName: "test4",
			alert:     Alert{Firing: true, Title: "Chain stuck", Body: Body{plain("No new block for 30+ seconds")}},
			want:      "🚨 test4 — Chain stuck\nNo new block for 30+ seconds",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatAlert(tt.chainName, tt.publicURL, tt.alert)
			if got != tt.want {
				t.Errorf("FormatAlert() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPageLink(t *testing.T) {
	for _, tc := range []struct{ name, publicURL, path, want string }{
		{"a URL and a path", "https://x", "/?view=ranking", "https://x/?view=ranking"},
		{"a URL ending with a slash", "https://x/", "/?view=ranking&explain", "https://x/?view=ranking&explain"},
		{"no path", "https://x/", "", "https://x/"},
		{"no public URL", "", "/?view=ranking", ""},
		{"neither", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pageLink(tc.publicURL, tc.path); got != tc.want {
				t.Errorf("pageLink(%q, %q) = %q, want %q", tc.publicURL, tc.path, got, tc.want)
			}
		})
	}
}

func TestFormatAlertAppendsTheLink(t *testing.T) {
	a := Alert{Notice: true, Title: "Validator ranking", Body: Body{plain("#1 A 99.0")}, Link: "/?view=ranking"}
	got := FormatAlert("test4", "https://x/", a)
	if want := "📣 test4 — Validator ranking\n#1 A 99.0\nhttps://x/?view=ranking"; got != want {
		t.Errorf("FormatAlert(link) = %q, want %q", got, want)
	}
	if got := FormatAlert("test4", "", a); strings.Contains(got, "view=ranking") {
		t.Errorf("FormatAlert(no public URL) = %q, want no link", got)
	}
	a.Link = ""
	want := "📣 test4 — Validator ranking\n#1 A 99.0\nhttps://x/"
	if got := FormatAlert("test4", "https://x/", a); got != want {
		t.Errorf("FormatAlert(no link) = %q, want %q", got, want)
	}
}
