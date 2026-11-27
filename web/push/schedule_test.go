package push

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/node"
)

// Keys of a real browser push subscription: sends encrypt to them.
const (
	testP256dh = "BNNL5ZaTfK81qhXOx23-wewhigUeFb632jN6LvRWCFH1ubQr77FE_9qV1FuojuRmHP42zmf34rXgW80OvUVDgTk"
	testAuth   = "zqbxT6JKstKSY9JKibZLSQ"
)

// device saves a device pushed to http://host/path with alerts.
func device(t *testing.T, m *Manager, host, path string, alerts ...SubscriptionAlert) string {
	t.Helper()
	id, err := m.db.SaveDevice(Subscription{ID: "dev" + path, Endpoint: "http://" + host + path, P256dh: testP256dh, Auth: testAuth}, alerts, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// sendTo makes the manager's only notification target http://host/path,
// receiving alerts.
func sendTo(t *testing.T, m *Manager, host, path string, alerts map[AlertType]AlertOptions) {
	t.Helper()
	if err := m.SetNotifyConfig(NotifyConfig{Targets: []NotifyRoute{{URL: "generic+http://" + host + path, Alerts: alerts}}}); err != nil {
		t.Fatal(err)
	}
}

func noEstimate(int64) (time.Duration, bool) { return 0, false }

func halt(head, height int64) Schedule {
	return Schedule{Chain: "test-1", Head: head, Halt: &Halt{Height: height, MinVersion: "v1.2.0"}, TimeLeft: noEstimate}
}

func TestEvaluateScheduleAlertsANewHaltOncePerOwner(t *testing.T) {
	host, h := hookServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	device(t, m, host, "/dev", SubscriptionAlert{AlertType: AlertHaltScheduled})
	sendTo(t, m, host, "/target", map[AlertType]AlertOptions{AlertHaltScheduled: {}})
	for range 3 {
		m.EvaluateSchedule(halt(900, 1000))
	}
	if got := h.got(); !slices.Equal(got, []string{"/dev", "/target"}) {
		t.Fatalf("posts = %v, want the device and the target once", got)
	}
	restarted, err := NewManager(m.db, 30, 5)
	if err != nil {
		t.Fatal(err)
	}
	sendTo(t, restarted, host, "/target", map[AlertType]AlertOptions{AlertHaltScheduled: {}})
	restarted.EvaluateSchedule(halt(901, 1000))
	if got := h.got(); len(got) != 2 {
		t.Errorf("posts after a restart = %v, want none more", got)
	}
}

func TestEvaluateScheduleAlertsAMovedHaltAgain(t *testing.T) {
	host, h := hookServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	device(t, m, host, "/dev", SubscriptionAlert{AlertType: AlertHaltScheduled})
	m.EvaluateSchedule(halt(900, 1000))
	m.EvaluateSchedule(halt(901, 1200))
	m.EvaluateSchedule(Schedule{Chain: "test-1", Head: 902, TimeLeft: noEstimate})
	if got := h.got(); !slices.Equal(got, []string{"/dev", "/dev"}) {
		t.Errorf("posts = %v, want one per halt height, none once no halt is scheduled", got)
	}
}

func TestEvaluateScheduleAlertsTheApproachingHaltAtEachLead(t *testing.T) {
	host, h := hookServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	device(t, m, host, "/dev", SubscriptionAlert{AlertType: AlertHaltApproaching, Lead: "100 blocks"})
	sendTo(t, m, host, "/target", map[AlertType]AlertOptions{AlertHaltApproaching: {Leads: []Lead{{Time: time.Hour}}}})
	left := 2 * time.Hour
	at := func(head int64) Schedule {
		s := halt(head, 1000)
		s.TimeLeft = func(int64) (time.Duration, bool) { return left, true }
		return s
	}
	m.EvaluateSchedule(at(850))
	if got := h.got(); len(got) != 0 {
		t.Fatalf("posts at 150 blocks and 2 h = %v, want none", got)
	}
	m.EvaluateSchedule(at(900))
	if got := h.got(); !slices.Equal(got, []string{"/dev"}) {
		t.Fatalf("posts at 100 blocks = %v, want the device", got)
	}
	left = 59 * time.Minute
	for head := int64(950); head < 955; head++ {
		m.EvaluateSchedule(at(head))
	}
	if got := h.got(); !slices.Equal(got, []string{"/dev", "/target"}) {
		t.Errorf("posts = %v, want the target once at 59 min, nothing twice", got)
	}
}

func TestEvaluateScheduleSendsEachLeadOfTheSeries(t *testing.T) {
	host, h := hookServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	series := map[AlertType]AlertOptions{AlertHaltApproaching: {Leads: defaultHaltLeads()}}
	sendTo(t, m, host, "/target", series)
	left := 25 * time.Hour
	at := func(m *Manager, head int64) {
		s := halt(head, 1000)
		s.TimeLeft = func(int64) (time.Duration, bool) { return left, true }
		m.EvaluateSchedule(s)
	}
	at(m, 100)
	if got := h.got(); len(got) != 0 {
		t.Fatalf("posts at 25 h = %v, want none", got)
	}
	for i, l := range []time.Duration{24*time.Hour - time.Minute, 2*time.Hour - time.Minute, time.Hour - time.Minute, 10*time.Minute - time.Minute, time.Minute - time.Second} {
		left = l
		at(m, 100+int64(i))
		if got := h.got(); len(got) != i+1 {
			t.Fatalf("posts at %v left = %v, want %d", l, got, i+1)
		}
	}
	at(m, 110)
	restarted, err := NewManager(m.db, 30, 5)
	if err != nil {
		t.Fatal(err)
	}
	sendTo(t, restarted, host, "/target", series)
	left = 30 * time.Second
	at(restarted, 111)
	if got := h.got(); len(got) != 5 {
		t.Errorf("posts = %v, want 5: each lead once, a restart sending none more", got)
	}
}

func TestEvaluateScheduleSendsLeadsMetTogetherOnce(t *testing.T) {
	host, h := hookServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	sendTo(t, m, host, "/target", map[AlertType]AlertOptions{AlertHaltApproaching: {Leads: defaultHaltLeads()}})
	left := 50 * time.Minute
	at := func(m *Manager, s Schedule) {
		s.TimeLeft = func(int64) (time.Duration, bool) { return left, true }
		m.EvaluateSchedule(s)
	}
	at(m, halt(500, 1000))
	if got := h.got(); len(got) != 1 {
		t.Fatalf("posts at 50 min on the first refresh = %v, want one for the 24h, 2h and 1h leads met together", got)
	}
	left = 49 * time.Minute
	at(m, halt(501, 1000))
	if got := h.got(); len(got) != 1 {
		t.Fatalf("posts at 49 min = %v, want still one", got)
	}
	left = 9 * time.Minute
	at(m, halt(502, 1000))
	if got := h.got(); len(got) != 2 {
		t.Fatalf("posts at 9 min = %v, want two", got)
	}
	other := newTestManager(t, false)
	sendTo(t, other, host, "/target", map[AlertType]AlertOptions{AlertHaltApproaching: {Leads: defaultHaltLeads()}})
	other.EvaluateSchedule(halt(999, 1000))
	if got := h.got(); len(got) != 3 {
		t.Errorf("posts with no estimate at the block before the halt = %v, want one more for all five leads", got)
	}
}

func TestEvaluateScheduleSendsALeadAddedAfterItWasMet(t *testing.T) {
	host, h := hookServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	left := 59 * time.Minute
	at := func(m *Manager, head int64) {
		s := halt(head, 1000)
		s.TimeLeft = func(int64) (time.Duration, bool) { return left, true }
		m.EvaluateSchedule(s)
	}
	sendTo(t, m, host, "/target", map[AlertType]AlertOptions{AlertHaltApproaching: {Leads: []Lead{{Time: time.Hour}}}})
	at(m, 500)
	if got := h.got(); len(got) != 1 {
		t.Fatalf("posts at 59 min = %v, want one", got)
	}
	edited, err := NewManager(m.db, 30, 5)
	if err != nil {
		t.Fatal(err)
	}
	sendTo(t, edited, host, "/target", map[AlertType]AlertOptions{AlertHaltApproaching: {Leads: []Lead{{Time: time.Hour}, {Time: 10 * time.Minute}}}})
	left = 9 * time.Minute
	at(edited, 501)
	if got := h.got(); len(got) != 2 {
		t.Errorf("posts at 9 min with the 10m lead added = %v, want one more", got)
	}
}

func TestEvaluateScheduleFiresATimeLeadByTheBlockBeforeTheHalt(t *testing.T) {
	host, h := hookServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	device(t, m, host, "/dev", SubscriptionAlert{AlertType: AlertHaltApproaching, Lead: "30m"})
	m.EvaluateSchedule(halt(998, 1000))
	if got := h.got(); len(got) != 0 {
		t.Fatalf("posts with no estimate, 2 blocks out = %v, want none", got)
	}
	// The ETA tracker drops a halt once reached: the block before it is the
	// last refresh that sees it.
	m.EvaluateSchedule(halt(999, 1000))
	if got := h.got(); !slices.Equal(got, []string{"/dev"}) {
		t.Errorf("posts at the block before the halt = %v, want the device", got)
	}
}

func TestEvaluateScheduleSendsNothingTwiceAcrossAReSave(t *testing.T) {
	host, h := hookServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	lead := SubscriptionAlert{AlertType: AlertHaltApproaching, Lead: "100 blocks"}
	device(t, m, host, "/dev", lead)
	m.EvaluateSchedule(halt(950, 1000))
	device(t, m, host, "/dev", lead, SubscriptionAlert{AlertType: AlertValsetChanged})
	m.EvaluateSchedule(halt(951, 1000))
	if got := h.got(); !slices.Equal(got, []string{"/dev"}) {
		t.Errorf("posts = %v, want one: the re-saved device keeps what was sent to it", got)
	}
}

func TestValsetChangeAlertSaysWhatChanged(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	c := blocks.ValsetChange{
		Height: 1223188, Time: now.Add(-2 * time.Hour),
		Added: []blocks.Member{
			{Address: "g1alpha", Power: 1},
			{Address: "g1addedaddress00000000000000000000000xyz", Power: 1},
		},
		Removed:    []blocks.Member{{Address: "g1gone", Power: 2}},
		Repowered:  []blocks.PowerChange{{Address: "g1berty", Before: 1, After: 2}},
		Rotated:    []blocks.Rotation{{Operator: "g1op", OldAddress: "g1oldkey0000000000000000000000000000abcd", NewAddress: "g1newkey0000000000000000000000000000efgh", Power: 1}},
		SizeBefore: 54, SizeAfter: 55,
	}
	a := valsetChangeAlert(c, valsetNames, now)
	if a.Type != AlertValsetChanged || !a.Notice || !a.Firing || a.Title != "Validator set changed at #1223188" {
		t.Errorf("alert = %+v, want a valset_changed notice titled with the height", a)
	}
	want := "**Joined (2)**\nalpha (power 1)\ng1addeda…0xyz (power 1)\n\n" +
		"**Left (1)**\nteritori (power 2)\n\n" +
		"**Validator set size (+1)**\n54 → 55\n\n" +
		"**Voting power changed (1)**\nberty 1 → 2\n\n" +
		"**Rotated signing key (1)**\naeddi g1oldkey…abcd → g1newkey…efgh\n\n" +
		"At 2026-11-19 10:00 UTC (2 h ago)"
	if got := marked(a.Body); got != want {
		t.Errorf("body =\n%s\nwant\n%s", got, want)
	}
	if got, want := a.Body.String(), strings.ReplaceAll(want, "**", ""); got != want {
		t.Errorf("body text =\n%s\nwant\n%s", got, want)
	}
}

// valsetNames names the addresses of the set-change tests, and leaves the
// others as they are.
func valsetNames(addr string) string {
	names := map[string]string{
		"g1gone": "teritori", "g1berty": "berty", "g1alpha": "alpha", "g1bravo": "bravo",
		"g1teritori": "teritori", "g1newkey0000000000000000000000000000efgh": "aeddi",
	}
	if n, ok := names[addr]; ok {
		return n
	}
	return addr
}

func TestValsetChangeSections(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	alpha := blocks.Member{Address: "g1alpha", Power: 1}
	bravo := blocks.Member{Address: "g1bravo", Power: 1}
	teritori := blocks.Member{Address: "g1teritori", Power: 2}
	tests := []struct {
		name string
		c    blocks.ValsetChange
		want string
	}{
		{"the set grows", blocks.ValsetChange{Added: []blocks.Member{alpha, bravo}, SizeBefore: 3, SizeAfter: 5},
			"**Joined (2)**\nalpha (power 1)\nbravo (power 1)\n\n**Validator set size (+2)**\n3 → 5"},
		{"the set shrinks", blocks.ValsetChange{Removed: []blocks.Member{teritori}, SizeBefore: 4, SizeAfter: 3},
			"**Left (1)**\nteritori (power 2)\n\n**Validator set size (\u22121)**\n4 → 3"},
		{"a swap keeps the size", blocks.ValsetChange{Added: []blocks.Member{alpha}, Removed: []blocks.Member{teritori}, SizeBefore: 4, SizeAfter: 4},
			"**Joined (1)**\nalpha (power 1)\n\n**Left (1)**\nteritori (power 2)"},
		{"power only", blocks.ValsetChange{Repowered: []blocks.PowerChange{{Address: "g1berty", Before: 1, After: 2}}, SizeBefore: 4, SizeAfter: 4},
			"**Voting power changed (1)**\nberty 1 → 2"},
		{"rotation only", blocks.ValsetChange{Rotated: []blocks.Rotation{{Operator: "g1op", OldAddress: "g1oldkey0000000000000000000000000000abcd", NewAddress: "g1newkey0000000000000000000000000000efgh", Power: 1}}, SizeBefore: 4, SizeAfter: 4},
			"**Rotated signing key (1)**\naeddi g1oldkey…abcd → g1newkey…efgh"},
		{"the size alone", blocks.ValsetChange{SizeBefore: 4, SizeAfter: 5},
			"**Validator set size (+1)**\n4 → 5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.Height, tt.c.Time = 5, now.Add(-time.Minute)
			if got := marked(valsetChangeAlert(tt.c, valsetNames, now).Body); got != tt.want {
				t.Errorf("valsetChangeAlert(%s) body =\n%q\nwant\n%q", tt.name, got, tt.want)
			}
		})
	}
}

func TestValsetChangeOfNothingStartsWithItsAge(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	a := valsetChangeAlert(blocks.ValsetChange{Height: 5, Time: now.Add(-2 * time.Hour), SizeBefore: 4, SizeAfter: 4}, valsetNames, now)
	if got, want := a.Body.String(), "At 2026-11-19 10:00 UTC (2 h ago)"; got != want {
		t.Errorf("valsetChangeAlert(empty change, 2 h old) body = %q, want %q", got, want)
	}
}

func TestValsetChangeBreaksAMentionInATextTarget(t *testing.T) {
	host, got := bodyServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	sendTo(t, m, host, "/target", map[AlertType]AlertOptions{AlertValsetChanged: {}})
	m.NotifyValsetChange(blocks.ValsetChange{Height: 5, Time: time.Now(), SizeBefore: 1, SizeAfter: 1, Removed: []blocks.Member{{Address: "g1x", Power: 1}}},
		func(a string) string {
			if a == "g1x" {
				return "@everyone"
			}
			return a
		})
	want := "📣 Validator set changed at #5\nLeft (1)\n@\u200beveryone (power 1)"
	if bodies := got(); !slices.Equal(bodies, []string{want}) {
		t.Errorf("NotifyValsetChange text = %q, want %q", bodies, []string{want})
	}
}

func TestValsetChangeReachesATextTargetWithPlainTitles(t *testing.T) {
	host, got := bodyServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	sendTo(t, m, host, "/target", map[AlertType]AlertOptions{AlertValsetChanged: {}})
	m.NotifyValsetChange(blocks.ValsetChange{Height: 5, Time: time.Now(), SizeBefore: 1, SizeAfter: 2, Added: []blocks.Member{{Address: "g1x", Power: 1}}},
		func(a string) string {
			if a == "g1x" {
				return "alpha"
			}
			return a
		})
	want := "📣 Validator set changed at #5\nJoined (1)\nalpha (power 1)\n\nValidator set size (+1)\n1 → 2"
	if bodies := got(); !slices.Equal(bodies, []string{want}) {
		t.Errorf("NotifyValsetChange text = %q, want %q", bodies, []string{want})
	}
}

func TestNotifyValsetChangeReachesItsSubscribers(t *testing.T) {
	host, h := hookServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	device(t, m, host, "/dev", SubscriptionAlert{AlertType: AlertValsetChanged})
	sendTo(t, m, host, "/target", map[AlertType]AlertOptions{AlertValsetChanged: {}})
	m.NotifyValsetChange(blocks.ValsetChange{Height: 5, Time: time.Now(), SizeBefore: 1, SizeAfter: 2, Added: []blocks.Member{{Address: "g1x", Power: 1}}},
		func(a string) string { return a })
	if got := h.got(); !slices.Equal(got, []string{"/dev", "/target"}) {
		t.Errorf("posts = %v, want the device and the target", got)
	}
}

func TestFormatAlertMarksANotice(t *testing.T) {
	got := FormatAlert("onyx", "", Alert{Notice: true, Firing: true, Title: "Validator set changed at #5", Body: Body{plain("x")}})
	if !strings.HasPrefix(got, "📣 onyx — Validator set changed at #5") {
		t.Errorf("FormatAlert = %q, want a megaphone for a one-off event", got)
	}
}

// remind sets a reminder for the device pushed to http://host/path.
func remind(t *testing.T, m *Manager, host, path string, r Reminder) string {
	t.Helper()
	id, err := m.db.SetReminder(Subscription{ID: "dev" + path, Endpoint: "http://" + host + path, P256dh: testP256dh, Auth: testAuth}, r)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func at(chain string, head int64) Schedule {
	return Schedule{Chain: chain, Head: head, TimeLeft: noEstimate}
}

func TestEvaluateScheduleFiresAReminderOnceAndDropsIt(t *testing.T) {
	host, h := hookServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	id := remind(t, m, host, "/dev", Reminder{Chain: "test-1", Height: 1000, Label: "upgrade", Lead: Lead{Blocks: 100}})
	m.EvaluateSchedule(at("test-1", 850))
	m.EvaluateSchedule(at("test-1", 900))
	m.EvaluateSchedule(at("test-1", 901))
	if got := h.got(); !slices.Equal(got, []string{"/dev"}) {
		t.Errorf("posts = %v, want one at 100 blocks", got)
	}
	if _, ok, _ := m.db.Reminder(id); ok {
		t.Error("the reminder stays after it fired")
	}
	if subs, _ := m.db.AllSubscriptions(); len(subs) != 1 {
		t.Errorf("devices = %v, want the device kept: the page still holds its id", subs)
	}
}

// TestEvaluateScheduleDropsAReminderAsStored checks a reminder whose row
// holds its lead as "1 blocks", another text for a lead of 1 block: it fires
// once and is dropped.
func TestEvaluateScheduleDropsAReminderAsStored(t *testing.T) {
	host, h := hookServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	id := remind(t, m, host, "/dev", Reminder{Chain: "test-1", Height: 1000, Lead: Lead{Blocks: 1}})
	if _, err := m.db.db.Exec(`UPDATE push_reminders SET lead = '1 blocks' WHERE subscription_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	m.EvaluateSchedule(at("test-1", 999))
	m.EvaluateSchedule(at("test-1", 1000))
	if got := h.got(); !slices.Equal(got, []string{"/dev"}) {
		t.Errorf("posts = %v, want one at 1 block", got)
	}
	if _, ok, _ := m.db.Reminder(id); ok {
		t.Error("the reminder stays after it fired")
	}
}

func TestEvaluateScheduleDropsARemindersOfAnotherChain(t *testing.T) {
	host, h := hookServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	id := remind(t, m, host, "/dev", Reminder{Chain: "old-1", Height: 1000, Lead: Lead{Blocks: 100}})
	m.EvaluateSchedule(at("test-1", 950))
	if got := h.got(); len(got) != 0 {
		t.Errorf("posts = %v, want none for another chain's height", got)
	}
	if _, ok, _ := m.db.Reminder(id); ok {
		t.Error("another chain's reminder stays")
	}
}

func TestEvaluateScheduleFiresATimeReminderOnceReached(t *testing.T) {
	host, h := hookServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	remind(t, m, host, "/dev", Reminder{Chain: "test-1", Height: 1000, Lead: Lead{Time: 30 * time.Minute}})
	m.EvaluateSchedule(at("test-1", 999))
	if got := h.got(); len(got) != 0 {
		t.Fatalf("posts with no estimate before the height = %v, want none", got)
	}
	m.EvaluateSchedule(at("test-1", 1000))
	if got := h.got(); !slices.Equal(got, []string{"/dev"}) {
		t.Errorf("posts once reached = %v, want the device", got)
	}
}

// Run under -race: the publish loop records the chain's name while the block
// history's goroutine sends a set change.
func TestManagerSendsWhileTheChainNameChanges(t *testing.T) {
	host, _ := hookServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	sendTo(t, m, host, "/target", map[AlertType]AlertOptions{AlertValsetChanged: {}})
	snap := &node.Snapshot{Status: &node.Status{NodeInfo: node.NodeInfo{Network: "test-1"}}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 20 {
			m.EvaluateAndNotify(snap, 0)
		}
	}()
	for range 20 {
		m.NotifyValsetChange(blocks.ValsetChange{Height: 5, Time: time.Now(), SizeBefore: 1, SizeAfter: 1}, func(a string) string { return a })
	}
	<-done
}

func TestSendsGiveUpAfterTheSendTimeout(t *testing.T) {
	release := make(chan struct{})
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	t.Cleanup(hung.Close)
	t.Cleanup(func() { close(release) })
	m := newTestManager(t, false)
	captureLog(t)
	m.SetSendTimeout(100 * time.Millisecond)
	if _, err := m.db.SaveDevice(Subscription{ID: "d", Endpoint: hung.URL, P256dh: testP256dh, Auth: testAuth},
		[]SubscriptionAlert{{AlertType: AlertChainStuck}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	sent := make(chan struct{})
	go func() {
		m.NotifyAlert(Alert{Type: AlertChainStuck, Firing: true, Title: "Chain stuck"})
		close(sent)
	}()
	select {
	case <-sent:
	case <-time.After(3 * time.Second):
		t.Fatal("NotifyAlert still waits on a push endpoint that never answers")
	}
}

func TestAlertsSayOneBlock(t *testing.T) {
	s := halt(999, 1000)
	r := reminderAlert(Reminder{Height: 1000, Label: "upgrade", Lead: Lead{Blocks: 1}}, s)
	if r.Title != "upgrade: #1000 in 1 block" {
		t.Errorf("reminder title = %q, want one block", r.Title)
	}
	if got := approachingTitle(s.Halt.Height, s.Head); got != "Coordinated halt at #1000 in 1 block" {
		t.Errorf("halt title = %q, want one block", got)
	}
}

func TestAReminderOpensItsCard(t *testing.T) {
	r := reminderAlert(Reminder{Height: 1000, Since: "2026-11-19T10:00:00Z", Label: "up & go", Lead: Lead{Blocks: 100}}, halt(900, 2000))
	u, err := url.Parse(r.Link)
	if err != nil || u.Path != "/" {
		t.Fatalf("link = %q, %v; want the dashboard with the card", r.Link, err)
	}
	q := u.Query()
	if q.Get("eta") != "1000" || q.Get("since") != "2026-11-19T10:00:00Z" || q.Get("label") != "up & go" {
		t.Errorf("link query = %v, want the card's eta, since and label", q)
	}
	if p := payloadOf("onyx", r); p.URL != r.Link || p.Title != "onyx - "+r.Title {
		t.Errorf("payload = %+v, want the link and the chain-prefixed title", p)
	}
}

func TestHaltReachedReachesChainStuckSubscribers(t *testing.T) {
	devHost, dev := hookServer(t)
	host, bodies := bodyServer(t)
	m := newTestManager(t, false)
	captureLog(t)
	device(t, m, devHost, "/dev", SubscriptionAlert{AlertType: AlertChainStuck})
	sendTo(t, m, host, "/target", map[AlertType]AlertOptions{AlertChainStuck: {}})
	snap := func(height string) *node.Snapshot {
		return &node.Snapshot{Status: &node.Status{
			NodeInfo: node.NodeInfo{Network: "test-1"},
			SyncInfo: node.SyncInfo{LatestBlockHeight: height},
		}}
	}
	m.EvaluateAndNotify(snap("1000"), 1000)
	m.detector.OverrideLastHeightChange(time.Now().Add(-31 * time.Second))
	m.EvaluateAndNotify(snap("1000"), 1000)
	if got := dev.got(); !slices.Equal(got, []string{"/dev"}) {
		t.Errorf("device posts at the halt = %v, want one", got)
	}
	want := "📣 test-1 — Coordinated halt reached at #1000\nThe chain stopped at the scheduled height, as planned."
	if got := bodies(); !slices.Equal(got, []string{want}) {
		t.Errorf("target bodies at the halt = %q, want %q", got, want)
	}

	m.EvaluateAndNotify(snap("1001"), 1000)
	want = "✅ test-1 — Chain resumed\nNew block at height 1001"
	if got := bodies(); !slices.Equal(got, []string{
		"📣 test-1 — Coordinated halt reached at #1000\nThe chain stopped at the scheduled height, as planned.", want}) {
		t.Errorf("target bodies after the resume = %q, want the resume after the halt", got)
	}
	if got := dev.got(); len(got) != 1 {
		t.Errorf("device posts after the resume = %v, want no more", got)
	}
}
