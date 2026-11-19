package push

import (
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
	id, err := m.db.SaveDevice(Subscription{ID: "dev" + path, Endpoint: "http://" + host + path, P256dh: testP256dh, Auth: testAuth}, alerts)
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
	sendTo(t, m, host, "/target", map[AlertType]AlertOptions{AlertHaltApproaching: {Lead: Lead{Time: time.Hour}}})
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
		Added:      []blocks.Member{{Address: "g1addedaddress00000000000000000000000xyz", Power: 1}},
		Removed:    []blocks.Member{{Address: "g1gone", Power: 2}},
		Repowered:  []blocks.PowerChange{{Address: "g1berty", Before: 1, After: 2}},
		Rotated:    []blocks.Rotation{{Operator: "g1op", OldAddress: "g1oldkey0000000000000000000000000000abcd", NewAddress: "g1newkey0000000000000000000000000000efgh", Power: 1}},
		SizeBefore: 54, SizeAfter: 54,
	}
	names := map[string]string{"g1gone": "teritori", "g1berty": "berty", "g1newkey0000000000000000000000000000efgh": "aeddi"}
	name := func(addr string) string {
		if n, ok := names[addr]; ok {
			return n
		}
		return addr
	}
	a := valsetChangeAlert(c, name, now)
	if a.Type != AlertValsetChanged || !a.Notice || !a.Firing || a.Title != "Validator set changed at #1223188" {
		t.Errorf("alert = %+v, want a valset_changed notice titled with the height", a)
	}
	want := strings.Join([]string{
		"+ g1addeda…0xyz joined (power 1)",
		"− teritori left (power 2)",
		"↕ berty power 1 → 2",
		"⇄ aeddi rotated its key g1oldkey…abcd → g1newkey…efgh",
		"Validators: 54 → 54",
		"At 2026-11-19 10:00 UTC (2 h ago)",
	}, "\n")
	if a.Body != want {
		t.Errorf("body =\n%s\nwant\n%s", a.Body, want)
	}
	recent := valsetChangeAlert(blocks.ValsetChange{
		Height: 5, Time: now.Add(-time.Minute), SizeBefore: 3, SizeAfter: 4,
		Added: []blocks.Member{{Address: "g1x", Power: 1}},
	}, name, now)
	if strings.Contains(recent.Body, " ago)") {
		t.Errorf("body of a fresh change = %q, want no age", recent.Body)
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
	got := FormatAlert("onyx", "", Alert{Notice: true, Firing: true, Title: "Validator set changed at #5", Body: "x"})
	if !strings.HasPrefix(got, "🔔 onyx — Validator set changed at #5") {
		t.Errorf("FormatAlert = %q, want a bell for a one-off event", got)
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
			m.EvaluateAndNotify(snap)
		}
	}()
	for range 20 {
		m.NotifyValsetChange(blocks.ValsetChange{Height: 5, Time: time.Now(), SizeBefore: 1, SizeAfter: 1}, func(a string) string { return a })
	}
	<-done
}
