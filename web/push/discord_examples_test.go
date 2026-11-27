//go:build discord

package push

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/ranking"
)

const discordExamplesFile = ".ignore/dev/discord-test-url"

// examplesPause spaces the sends: Discord rate-limits webhooks, and a target
// in json mode fails on any answer but 204.
const examplesPause = 3 * time.Second

var discordWebhook = regexp.MustCompile(`^https://(?:discord|discordapp)\.com/api/webhooks/(\d+)/([A-Za-z0-9_-]+)$`)

// shoutrrrDiscordURL returns line as a discord://<token>@<id> URL: a
// discord:// line as is, a Discord webhook URL converted. ok is false for
// any other line.
func shoutrrrDiscordURL(line string) (url string, ok bool) {
	if strings.HasPrefix(line, "discord://") {
		return line, true
	}
	m := discordWebhook.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return "discord://" + m[2] + "@" + m[1], true
}

// discordTestURL reads the first discordExamplesFile found from the package
// directory up to the filesystem root, so the test also runs from a worktree
// under .ignore/. The text of the file never reaches a message.
func discordTestURL(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		b, err := os.ReadFile(filepath.Join(dir, discordExamplesFile))
		if err == nil {
			url, ok := shoutrrrDiscordURL(strings.TrimSpace(string(b)))
			if !ok {
				t.Fatal("the file holds no Discord webhook URL")
			}
			return url
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("cannot read a %s under %s", discordExamplesFile, dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("no " + discordExamplesFile)
		}
		dir = parent
	}
}

// rankedOperators is how many operators the ranking example ranks: enough
// for its table to span several messages.
const rankedOperators = 300

// operatorNames returns rankedOperators made-up names, as operators name
// themselves: the main sequence's validators, a name too long for the
// ranking's table, then a prefix and a suffix, each pair at most once.
func operatorNames() []string {
	names := []string{"Mithrandir", "Onbloc", "Berty", "Stakecraft", "Polkachu", "Northern Lights Staking Co."}
	prefixes := []string{
		"Aurora", "Basalt", "Cedar", "Driftwood", "Ember", "Fjord", "Granite", "Harbor", "Iris", "Juniper",
		"Kestrel", "Lumen", "Meridian", "Nimbus", "Obsidian", "Pinnacle", "Quartz", "Rook", "Sierra", "Tundra",
	}
	suffixes := []string{
		"Nodes", "Staking", "Labs", "Validator", "Capital", "Infra", "Stake", "Ventures",
		"Systems", "Collective", "Network", "Works", "Guild", "Cloud", "DAO",
	}
	// Each prefix in turn; a prefix's j-th turn takes suffix 2j plus the
	// prefix's index, modulo the suffixes, which differs for each of its
	// len(suffixes) turns as 2 and 15 are coprime.
	for k := 0; len(names) < rankedOperators; k++ {
		p := k % len(prefixes)
		s := (2*(k/len(prefixes)) + p) % len(suffixes)
		names = append(names, prefixes[p]+" "+suffixes[s])
	}
	return names
}

// TestDiscordExamples sends examples to the Discord webhook named by
// discordExamplesFile: every alert, with realistic names; then a set change
// caught up after downtime and, last, an escaping check of hostile names,
// each announced by a message of its own. It is built only with the discord
// tag: a plain go test never posts to a server.
func TestDiscordExamples(t *testing.T) {
	url := discordTestURL(t)
	m := newTestManager(t, true)
	logs := captureLog(t)
	m.SetPublicURL("https://gnockpit.example.com")
	err := m.SetNotifyConfig(NotifyConfig{Targets: []NotifyRoute{{URL: url, HideChain: true, Alerts: map[AlertType]AlertOptions{
		AlertValidatorMissingBlocks: {},
		AlertChainStuck:             {},
		AlertValsetChanged:          {},
		AlertHaltScheduled:          {},
		AlertHaltApproaching:        {},
		AlertRanking:                {},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	// Saving the config logs the target count; only a failed send is an error.
	logs.Reset()
	const haltHeight = 1000
	pause := func() { time.Sleep(examplesPause) }
	stall := func() { m.detector.OverrideLastHeightChange(time.Now().Add(-time.Minute)) }
	// say posts text as a message of its own, telling what the messages
	// after it show.
	say := func(text string) {
		if err := m.SendTestNotify(0, text); err != nil {
			t.Errorf("SendTestNotify: %v", err)
		}
		pause()
	}
	// snap is the chain at height, set its validators: each signs 10 blocks
	// in a row but those lapses names, which sign as it gives.
	snap := func(height string, set []node.Validator, lapses map[string]node.ValSigning) *node.Snapshot {
		signing := make(map[string]node.ValSigning, len(set))
		for _, v := range set {
			vs, ok := lapses[v.Address]
			if !ok {
				vs = node.ValSigning{SignedInARow: 10}
			}
			signing[v.Address] = vs
		}
		return &node.Snapshot{
			Status:     &node.Status{NodeInfo: node.NodeInfo{Network: "gnoland-1"}, SyncInfo: node.SyncInfo{LatestBlockHeight: height}},
			Validators: set,
			Signing:    &node.SigningStats{WindowSize: 100, ValidatorSigning: signing},
		}
	}

	// The validators the examples name, at made-up addresses.
	var (
		mithrandir = node.Validator{Address: "g15feryxhrdz9m6y09mr8wrerwzgj6f8ntxvhxyr", Name: "Mithrandir"}
		onbloc     = node.Validator{Address: "g1dlm5aahn0t09nl4ujy8624fl6zy54klay937yr", Name: "Onbloc"}
		berty      = node.Validator{Address: "g1nujckpak28lrdjg0eel92ue3gm36kcwf9tfwwq", Name: "Berty"}
		stakecraft = node.Validator{Address: "g1ltsjqf6h5graeeeex7ervydu284rxqfxhpydcf", Name: "Stakecraft"}
		polkachu   = node.Validator{Address: "g1skh788la77n9fx4s72pdhfpn9sh2kw4wv0ewvl", Name: "Polkachu"}
		allnodes   = node.Validator{Address: "g1kpp37svkukh9wxw7v4d7q7k98cv7tm49eae922", Name: "Allnodes"}
		nodeist    = node.Validator{Address: "g178r57xr0v3zxupyu5v3u70svug68eu5y0mydn8", Name: "Nodeist"}
		kiln       = node.Validator{Address: "g1t3gmsefl593rtmy3p9s9wys8aq463gz082srtv", Name: "Kiln"}
		// Polkachu's signing key after its rotation.
		polkachuKey = node.Validator{Address: "g1fhfsgawxel2w2me46vk59hp4aupc4jy8wx9s3z", Name: "Polkachu"}
		// The hostile names of the escaping check: mentions, Markdown with a
		// URL, a bare domain, which nothing escapes, and a number that would
		// open a numbered list.
		everyone   = node.Validator{Address: "g1nndjut3kpszqpv70uxmlendw4vgekrgqysm2r9", Name: "@everyone <@123>"}
		markdown   = node.Validator{Address: "g1cj0jzat23uqsh450zndktq4c973v0q9s9fezep", Name: "gno_validator_[x](https://e.co/)"}
		bareDomain = node.Validator{Address: "g1sjtwfwtgydjpmgq3f9sumgskwlmhm58ksuguut", Name: "node.example.com/x"}
		numbered   = node.Validator{Address: "g1nnw9fc5lfjfzmgpw9pzghxcurp0lsqay9y7sys", Name: "1. Validator"}
	)
	// An address with no name, which a set change shows short.
	const nameless = "g1gpfaf7kfgpqxgmvdpsdj05s6grka6gfputqftf"
	names := map[string]string{}
	for _, v := range []node.Validator{mithrandir, onbloc, berty, stakecraft, polkachu, allnodes, nodeist, kiln, polkachuKey, everyone, markdown, bareDomain, numbered} {
		names[v.Address] = v.Name
	}
	name := func(address string) string { return cmp.Or(names[address], address) }
	set := []node.Validator{mithrandir, onbloc, berty, stakecraft, polkachu}

	// 1. A validator missing blocks, then back online.
	m.EvaluateAndNotify(snap("100", set, nil), 0)
	m.EvaluateAndNotify(snap("101", set, map[string]node.ValSigning{stakecraft.Address: {MissedInARow: 5}}), 0)
	pause()
	m.EvaluateAndNotify(snap("102", set, map[string]node.ValSigning{stakecraft.Address: {SignedInARow: 5}}), 0)
	pause()

	// 2. The chain stuck, then resumed.
	stall()
	m.EvaluateAndNotify(snap("102", set, nil), 0)
	pause()
	m.EvaluateAndNotify(snap("103", set, nil), 0)
	pause()

	// 3. A set change as it happens, with every section: names for some
	// addresses, a short address for the nameless.
	m.NotifyValsetChange(blocks.ValsetChange{
		Height: 104, Time: m.now(), SizeBefore: 5, SizeAfter: 6,
		Added:     []blocks.Member{{Address: allnodes.Address, Power: 1}, {Address: nameless, Power: 1}},
		Removed:   []blocks.Member{{Address: nodeist.Address, Power: 1}},
		Repowered: []blocks.PowerChange{{Address: onbloc.Address, Before: 1, After: 2}},
		Rotated:   []blocks.Rotation{{Operator: "g1uauvt7txunf9zeu2qyrzv0pa5uvwjlq9a369s5", OldAddress: polkachu.Address, NewAddress: polkachuKey.Address, Power: 1}},
	}, name)
	pause()

	// 4. A halt scheduled, far enough ahead for no lead to be met.
	left := 48 * time.Hour
	schedule := func(head int64) Schedule {
		s := halt(head, haltHeight)
		s.Chain = "gnoland-1"
		s.TimeLeft = func(int64) (time.Duration, bool) { return left, true }
		return s
	}
	m.EvaluateSchedule(schedule(900))
	pause()

	// 5. The series, one message per lead.
	for _, l := range []time.Duration{24*time.Hour - time.Minute, 2*time.Hour - time.Minute, time.Hour - time.Minute, 10*time.Minute - time.Minute, time.Minute - time.Second} {
		left = l
		m.EvaluateSchedule(schedule(900))
		pause()
	}

	// 6. The halt reached, then resumed.
	m.EvaluateAndNotify(snap("1000", set, nil), haltHeight)
	stall()
	m.EvaluateAndNotify(snap("1000", set, nil), haltHeight)
	pause()
	m.EvaluateAndNotify(snap("1001", set, nil), 0)
	pause()

	// 7. A ranking of rankedOperators operators against the day before: most
	// keep their rank, some neighbours swap, one climbs and one falls four
	// places, two are new; excluded and not-ranked-yet operators are counted.
	ranked := make([]ranking.Entry, rankedOperators)
	for i, n := range operatorNames() {
		ranked[i] = ranking.Entry{Operator: fmt.Sprintf("g1op%03d", i), Name: n, Status: ranking.Ranked, Rank: i + 1, Score: 99.8 - float64(i)*0.12}
	}
	before := slices.Clone(ranked)
	before = slices.Delete(before, 299, 300) // new at #300
	before = slices.Delete(before, 296, 297) // new at #297
	move := func(from, to int) {
		e := before[from]
		before = slices.Insert(slices.Delete(before, from, from+1), to, e)
	}
	move(30, 34)   // #31 was #35
	move(150, 146) // #151 was #147
	for _, i := range []int{4, 17, 63, 120, 205} {
		before[i], before[i+1] = before[i+1], before[i]
	}
	for i := range before {
		before[i].Rank = i + 1
	}
	unranked := []ranking.Entry{
		{Operator: "g1core01", Name: "gnocore-val-01", Status: ranking.Excluded},
		{Operator: "g1core02", Name: "gnocore-val-02", Status: ranking.Excluded},
		{Operator: "g1core03", Name: "gnocore-val-03", Status: ranking.Excluded},
		{Operator: "g1late01", Name: "Vesper Nodes", Status: ranking.NotRankedYet},
		{Operator: "g1late02", Name: "Wildflower Stake", Status: ranking.NotRankedYet},
	}
	excluded := []string{"g1core01", "g1core02", "g1core03"}
	today := ranking.Published{Chain: "gnoland-1", Day: "2026-11-26", Exclusions: excluded, Result: ranking.Result{
		Formula: 1, FirstDay: "2026-10-27", LastDay: "2026-11-25", Entries: slices.Concat(ranked, unranked),
	}}
	dayBefore := ranking.Published{Chain: "gnoland-1", Day: "2026-11-25", Exclusions: excluded, Result: ranking.Result{
		Formula: 1, FirstDay: "2026-10-26", LastDay: "2026-11-24", Entries: slices.Concat(before, unranked),
	}}
	digest := ranking.DigestOf(today, &dayBefore)
	if n := len(discordRanking("gnoland-1", "https://gnockpit.example.com", digest)); n < 2 {
		t.Fatalf("the ranking example is %d message(s), want it to span several", n)
	}
	m.NotifyRanking("gnoland-1:2026-11-26", digest)
	pause()

	// 8. The test notification.
	if err := m.SendTestNotify(0, "gnockpit test notification"); err != nil {
		t.Errorf("SendTestNotify: %v", err)
	}
	pause()

	// 9. A set change caught up after downtime, two hours old: its message
	// says when it happened.
	say("Caught up after downtime: gnockpit saw the set change below two hours late, so its message says when it happened.")
	m.NotifyValsetChange(blocks.ValsetChange{
		Height: 61, Time: m.now().Add(-2 * time.Hour), SizeBefore: 4, SizeAfter: 5,
		Added:     []blocks.Member{{Address: kiln.Address, Power: 1}},
		Repowered: []blocks.PowerChange{{Address: berty.Address, Before: 2, After: 1}},
	}, name)
	pause()

	// 10. The escaping check, last: hostile names missing blocks (bold),
	// joining the set (plain lines) and ranked (the table), with a control
	// character in a ranked name; the bare domain shows whether Discord
	// links one.
	say("Escaping check: the messages below quote hostile validator names. Each must show as written: no mention pill, no list, no colour, and no link from a URL with a scheme. The bare domain, which gnockpit leaves as it is, shows whether Discord links one.")
	hostile := []node.Validator{everyone, markdown, bareDomain, numbered}
	missed := make(map[string]node.ValSigning, len(hostile))
	joined := make([]blocks.Member, len(hostile))
	for i, v := range hostile {
		missed[v.Address] = node.ValSigning{MissedInARow: 5}
		joined[i] = blocks.Member{Address: v.Address, Power: 1}
	}
	m.EvaluateAndNotify(snap("1002", hostile, nil), 0)
	m.EvaluateAndNotify(snap("1003", hostile, missed), 0)
	pause()
	m.NotifyValsetChange(blocks.ValsetChange{Height: 1004, Time: m.now(), SizeBefore: 6, SizeAfter: 6 + len(joined), Added: joined}, name)
	pause()
	hostileDay := ranking.Published{Chain: "gnoland-1", Day: "2026-11-26", Result: ranking.Result{
		Formula: 1, FirstDay: "2026-10-27", LastDay: "2026-11-25", Entries: []ranking.Entry{
			{Operator: everyone.Address, Name: everyone.Name, Status: ranking.Ranked, Rank: 1, Score: 99.1},
			{Operator: markdown.Address, Name: markdown.Name, Status: ranking.Ranked, Rank: 2, Score: 98.7},
			{Operator: bareDomain.Address, Name: bareDomain.Name, Status: ranking.Ranked, Rank: 3, Score: 98.2},
			{Operator: numbered.Address, Name: numbered.Name, Status: ranking.Ranked, Rank: 4, Score: 97.4},
			{Operator: "g1evil", Name: "evil\x1b[31mred", Status: ranking.Ranked, Rank: 5, Score: 95.2},
		},
	}}
	m.NotifyRanking("gnoland-1:2026-11-26:escaping-check", ranking.DigestOf(hostileDay, nil))
	pause()

	if log := logs.String(); strings.Contains(log, "notify:") || strings.Contains(log, "push:") {
		t.Errorf("a notify or push error was logged, log:\n%s", log)
	}
}
