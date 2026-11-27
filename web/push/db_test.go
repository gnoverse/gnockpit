package push_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gnoverse/gnockpit/web/push"
)

func TestOpenDB_CreatesSchema(t *testing.T) {
	db, err := push.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
}

// TestOpenDBTurnsForeignKeysOnForEveryConnection checks that a connection the
// pool opens after dropping one, as it drops a connection whose statement was
// interrupted, enforces foreign keys too: the ON DELETE CASCADEs rely on them.
func TestOpenDBTurnsForeignKeysOnForEveryConnection(t *testing.T) {
	for _, c := range []struct{ name, path string }{
		{"a file path", "push.db"},
		{"a URI with its own parameters", "file:push.db?mode=rwc"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			db, err := push.OpenDB(c.path)
			if err != nil {
				t.Fatalf("OpenDB(%q): %v", c.path, err)
			}
			defer db.Close()
			conn, err := db.SQL().Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			// A driver.ErrBadConn out of Raw makes the pool close the connection.
			if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatalf("drop the connection: %v", err)
			}
			if open := db.SQL().Stats().OpenConnections; open != 0 {
				t.Fatalf("open connections after the drop = %d, want 0", open)
			}

			var on int
			if err := db.SQL().QueryRow("PRAGMA foreign_keys").Scan(&on); err != nil {
				t.Fatal(err)
			}
			if on != 1 {
				t.Errorf("PRAGMA foreign_keys on a new connection = %d, want 1", on)
			}
			if _, err := os.Stat("push.db"); err != nil {
				t.Errorf("the database file: %v", err)
			}
		})
	}
}

// TestOpenDBRefusesAnEmptyPath checks that an empty path opens nothing: its
// DSN would be the parameter alone, which SQLite takes for a file name.
func TestOpenDBRefusesAnEmptyPath(t *testing.T) {
	t.Chdir(t.TempDir())
	db, err := push.OpenDB("")
	if err == nil {
		db.Close()
	}
	if err == nil || err.Error() != "a database path is required" {
		t.Errorf("OpenDB(\"\") = %v, want a database path is required", err)
	}
	if files, err := os.ReadDir("."); err != nil || len(files) != 0 {
		t.Errorf("files made in the working directory = %v (%v), want none", files, err)
	}
}

func TestDB_VAPIDKeys_RoundTrip(t *testing.T) {
	db, _ := push.OpenDB(":memory:")
	defer db.Close()

	if err := db.SaveVAPIDKeys("pubkey", "privkey"); err != nil {
		t.Fatalf("SaveVAPIDKeys: %v", err)
	}
	pub, priv, err := db.LoadVAPIDKeys()
	if err != nil {
		t.Fatalf("LoadVAPIDKeys: %v", err)
	}
	if pub != "pubkey" || priv != "privkey" {
		t.Errorf("got (%q, %q), want (pubkey, privkey)", pub, priv)
	}
}

func TestDB_Subscriptions_CRUD(t *testing.T) {
	db, _ := push.OpenDB(":memory:")
	defer db.Close()

	sub := push.Subscription{
		ID: "test-id", Endpoint: "https://example.com/push",
		P256dh: "p256dh-val", Auth: "auth-val",
	}
	if err := db.SaveSubscription(sub); err != nil {
		t.Fatalf("SaveSubscription: %v", err)
	}

	subs, err := db.AllSubscriptions()
	if err != nil {
		t.Fatalf("AllSubscriptions: %v", err)
	}
	if len(subs) != 1 || subs[0].ID != "test-id" {
		t.Errorf("got %v, want 1 sub with id test-id", subs)
	}

	if err := db.DeleteSubscription("test-id"); err != nil {
		t.Fatalf("DeleteSubscription: %v", err)
	}
	subs, _ = db.AllSubscriptions()
	if len(subs) != 0 {
		t.Errorf("expected 0 subs after delete, got %d", len(subs))
	}
}

func TestDB_SubscriptionAlerts_SaveAndLoad(t *testing.T) {
	db, _ := push.OpenDB(":memory:")
	defer db.Close()

	db.SaveSubscription(push.Subscription{ID: "s1", Endpoint: "https://x.com", P256dh: "p", Auth: "a"})

	alerts := []push.SubscriptionAlert{
		{SubscriptionID: "s1", AlertType: push.AlertChainStuck, EntityID: "", RecoveryNotif: true},
		{SubscriptionID: "s1", AlertType: push.AlertValidatorMissingBlocks, EntityID: "g1aaa", RecoveryNotif: false},
	}
	if err := db.SaveSubscriptionAlerts("s1", alerts); err != nil {
		t.Fatalf("SaveSubscriptionAlerts: %v", err)
	}

	loaded, err := db.SubscribersForAlert(push.AlertChainStuck, "")
	if err != nil {
		t.Fatalf("SubscribersForAlert: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Sub.ID != "s1" {
		t.Errorf("expected 1 subscriber, got %v", loaded)
	}
	if !loaded[0].RecoveryNotif {
		t.Error("expected recovery_notif true")
	}
}

func TestDB_SubscriptionAlerts_DeleteCascades(t *testing.T) {
	db, _ := push.OpenDB(":memory:")
	defer db.Close()

	db.SaveSubscription(push.Subscription{ID: "s1", Endpoint: "https://x.com", P256dh: "p", Auth: "a"})
	db.SaveSubscriptionAlerts("s1", []push.SubscriptionAlert{
		{SubscriptionID: "s1", AlertType: push.AlertChainStuck, EntityID: ""},
	})
	db.DeleteSubscription("s1")

	loaded, _ := db.SubscribersForAlert(push.AlertChainStuck, "")
	if len(loaded) != 0 {
		t.Errorf("expected cascade delete, got %d rows", len(loaded))
	}
}

func TestDB_GetSubscription(t *testing.T) {
	db, _ := push.OpenDB(":memory:")
	defer db.Close()

	sub := push.Subscription{ID: "get-me", Endpoint: "https://x.com/push", P256dh: "p", Auth: "a"}
	if err := db.SaveSubscription(sub); err != nil {
		t.Fatalf("SaveSubscription: %v", err)
	}

	got, err := db.GetSubscription("get-me")
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if got.ID != "get-me" || got.Endpoint != "https://x.com/push" || got.P256dh != "p" || got.Auth != "a" {
		t.Errorf("got %+v, want matching fields", got)
	}
}

func TestDB_GetSubscription_NotFound(t *testing.T) {
	db, _ := push.OpenDB(":memory:")
	defer db.Close()

	_, err := db.GetSubscription("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent subscription")
	}
}

func openTestDB(t *testing.T) *push.DB {
	t.Helper()
	db, err := push.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSaveDeviceKeepsItsIdAcrossSaves(t *testing.T) {
	db := openTestDB(t)
	first, err := db.SaveDevice(push.Subscription{ID: "new1", Endpoint: "https://x.com/e", P256dh: "p", Auth: "a"},
		[]push.SubscriptionAlert{{AlertType: push.AlertChainStuck}}, nil, nil)
	if err != nil || first != "new1" {
		t.Fatalf("first save = %q, %v; want new1", first, err)
	}
	second, err := db.SaveDevice(push.Subscription{ID: "new2", Endpoint: "https://x.com/e", P256dh: "p2", Auth: "a2"},
		[]push.SubscriptionAlert{{AlertType: push.AlertValsetChanged}}, nil, nil)
	if err != nil || second != "new1" {
		t.Fatalf("second save = %q, %v; want new1 kept", second, err)
	}
	if subs, _ := db.AllSubscriptions(); len(subs) != 1 || subs[0].P256dh != "p2" {
		t.Errorf("subscriptions = %v, want one, with the second save's keys", subs)
	}
	if stuck, _ := db.SubscribersForAlert(push.AlertChainStuck, ""); len(stuck) != 0 {
		t.Errorf("chain_stuck subscribers = %v, want none: the second save replaced the alerts", stuck)
	}
	if changed, _ := db.SubscribersForAlert(push.AlertValsetChanged, ""); len(changed) != 1 {
		t.Errorf("valset_changed subscribers = %v, want the device", changed)
	}
}

func TestSaveDeviceDeletesADeviceLeftWithNothing(t *testing.T) {
	db := openTestDB(t)
	sub := push.Subscription{ID: "d1", Endpoint: "https://x.com/e", P256dh: "p", Auth: "a"}
	if _, err := db.SaveDevice(sub, []push.SubscriptionAlert{{AlertType: push.AlertChainStuck}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	id, err := db.SaveDevice(sub, nil, nil, nil)
	if err != nil || id != "" {
		t.Errorf("save with no alert = %q, %v; want \"\": the device is gone", id, err)
	}
	if subs, _ := db.AllSubscriptions(); len(subs) != 0 {
		t.Errorf("subscriptions = %v, want none", subs)
	}
}

// TestSaveDeviceKeepsTheAlertsOfAnUnshownValidator checks the validator
// alerts a save leaves: the device's stored ones on a validator the saving
// menu did not show stay as stored, unless the save names that validator,
// whose alert it replaces; one on a validator the menu showed is replaced
// like any other alert.
func TestSaveDeviceKeepsTheAlertsOfAnUnshownValidator(t *testing.T) {
	db := openTestDB(t)
	sub := push.Subscription{ID: "d1", Endpoint: "https://x.com/e", P256dh: "p", Auth: "a"}
	val := func(entity string, recovery bool) push.SubscriptionAlert {
		return push.SubscriptionAlert{AlertType: push.AlertValidatorMissingBlocks, EntityID: entity, RecoveryNotif: recovery}
	}
	if _, err := db.SaveDevice(sub, []push.SubscriptionAlert{val("g1shown", false), val("g1unshown", true), val("g1named", false)},
		nil, []string{"g1shown", "g1unshown", "g1named"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveDevice(sub, []push.SubscriptionAlert{val("g1named", true)}, nil, []string{"g1shown"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		entity   string
		recovery []bool // the recovery of each subscriber
		why      string
	}{
		{"g1shown", nil, "shown and left out, replaced"},
		{"g1unshown", []bool{true}, "not shown, kept as stored"},
		{"g1named", []bool{true}, "not shown and named, the save's"},
	} {
		subs, err := db.SubscribersForAlert(push.AlertValidatorMissingBlocks, tc.entity)
		if err != nil {
			t.Fatalf("%s subscribers: %v", tc.entity, err)
		}
		var recovery []bool
		for _, s := range subs {
			recovery = append(recovery, s.RecoveryNotif)
		}
		if !slices.Equal(recovery, tc.recovery) {
			t.Errorf("%s subscribers' recovery = %v, want %v: %s", tc.entity, recovery, tc.recovery, tc.why)
		}
	}
}

// TestSaveDeviceWithNothingShownKeepsTheDevice checks a save with no alert
// from a menu that showed no validator: the device's stored alert on a
// validator it did not show stays, and so does the device, its id answered.
func TestSaveDeviceWithNothingShownKeepsTheDevice(t *testing.T) {
	db := openTestDB(t)
	sub := push.Subscription{ID: "d1", Endpoint: "https://x.com/e", P256dh: "p", Auth: "a"}
	departed := push.SubscriptionAlert{AlertType: push.AlertValidatorMissingBlocks, EntityID: "g1gone"}
	if _, err := db.SaveDevice(sub, []push.SubscriptionAlert{departed}, nil, []string{"g1gone"}); err != nil {
		t.Fatal(err)
	}
	id, err := db.SaveDevice(sub, nil, nil, []string{})
	if err != nil || id != "d1" {
		t.Errorf("save with no alert, nothing shown = %q, %v; want d1: the device keeps its alert", id, err)
	}
	if subs, err := db.SubscribersForAlert(push.AlertValidatorMissingBlocks, "g1gone"); err != nil || len(subs) != 1 {
		t.Errorf("g1gone subscribers = %+v, %v; want the device", subs, err)
	}
}

func TestSaveDeviceStoresTheHaltLead(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.SaveDevice(push.Subscription{ID: "d1", Endpoint: "https://x.com/e", P256dh: "p", Auth: "a"},
		[]push.SubscriptionAlert{{AlertType: push.AlertHaltApproaching, Lead: "100 blocks"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err := db.SubscribersForAlert(push.AlertHaltApproaching, "")
	if err != nil || len(got) != 1 || got[0].Lead != (push.Lead{Blocks: 100}) {
		t.Errorf("subscribers = %+v, %v; want one with a 100-block lead", got, err)
	}
}

func TestOpenDBDedupesEndpoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`
		CREATE TABLE subscriptions (id TEXT PRIMARY KEY, endpoint TEXT NOT NULL, p256dh TEXT NOT NULL, auth TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
		CREATE TABLE subscription_alerts (subscription_id TEXT NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
			alert_type TEXT NOT NULL, entity_id TEXT NOT NULL DEFAULT '', recovery_notif INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (subscription_id, alert_type, entity_id));
		INSERT INTO subscriptions (id, endpoint, p256dh, auth) VALUES ('older', 'https://x.com/e', 'p', 'a'), ('newer', 'https://x.com/e', 'p', 'a');
		INSERT INTO subscription_alerts (subscription_id, alert_type) VALUES ('older', 'chain_stuck'), ('newer', 'chain_stuck');
	`); err != nil {
		t.Fatal(err)
	}
	old.Close()
	db, err := push.OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if subs, _ := db.AllSubscriptions(); len(subs) != 1 || subs[0].ID != "newer" {
		t.Errorf("subscriptions = %v, want the latest row of the endpoint", subs)
	}
	if _, err := db.SaveDevice(push.Subscription{ID: "x", Endpoint: "https://x.com/e", P256dh: "p", Auth: "a"},
		[]push.SubscriptionAlert{{AlertType: push.AlertHaltApproaching, Lead: "1h"}}, nil, nil); err != nil {
		t.Errorf("SaveDevice with a lead on the migrated database: %v", err)
	}
}

func testReminder(height int64) push.Reminder {
	return push.Reminder{Chain: "test-1", Height: height, Since: "2026-11-19T10:00:00Z", Label: "upgrade", Lead: push.Lead{Blocks: 100}}
}

var reminderDevice = push.Subscription{ID: "r1", Endpoint: "https://x.com/r", P256dh: "p", Auth: "a"}

func TestSetReminderReplacesTheDevicesReminder(t *testing.T) {
	db := openTestDB(t)
	first, err := db.SetReminder(reminderDevice, testReminder(1000))
	if err != nil || first != "r1" {
		t.Fatalf("SetReminder = %q, %v; want r1", first, err)
	}
	again := reminderDevice
	again.ID = "r2"
	second, err := db.SetReminder(again, testReminder(2000))
	if err != nil || second != "r1" {
		t.Fatalf("second SetReminder = %q, %v; want the device's id r1", second, err)
	}
	if got, ok, err := db.Reminder("r1"); err != nil || !ok || got != testReminder(2000) {
		t.Errorf("Reminder = %+v, %v, %v; want the second", got, ok, err)
	}
}

func TestDeleteReminderDropsADeviceLeftWithNothing(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.SetReminder(reminderDevice, testReminder(1000)); err != nil {
		t.Fatal(err)
	}
	if kept, err := db.DeleteReminder("r1"); err != nil || kept {
		t.Errorf("DeleteReminder = %v, %v; want the device gone", kept, err)
	}
	if subs, _ := db.AllSubscriptions(); len(subs) != 0 {
		t.Errorf("subscriptions = %v, want none", subs)
	}
	if _, err := db.SaveDevice(reminderDevice, []push.SubscriptionAlert{{AlertType: push.AlertChainStuck}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetReminder(reminderDevice, testReminder(1000)); err != nil {
		t.Fatal(err)
	}
	if kept, err := db.DeleteReminder("r1"); err != nil || !kept {
		t.Errorf("DeleteReminder with an alert left = %v, %v; want the device kept", kept, err)
	}
}

func TestSaveDeviceKeepsADeviceWithAReminder(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.SetReminder(reminderDevice, testReminder(1000)); err != nil {
		t.Fatal(err)
	}
	if id, err := db.SaveDevice(reminderDevice, nil, nil, nil); err != nil || id != "r1" {
		t.Errorf("SaveDevice with no alert = %q, %v; want r1 kept for its reminder", id, err)
	}
	if _, ok, _ := db.Reminder("r1"); !ok {
		t.Error("the reminder went with the alerts")
	}
}

func TestRemindersGoWithAnExpiredDevice(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.SetReminder(reminderDevice, testReminder(1000)); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteSubscription("r1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.Reminder("r1"); err != nil || ok {
		t.Errorf("Reminder after the device = %v, %v; want none", ok, err)
	}
}

// readReminder returns the one reminder db holds, as Reminders reads it.
func readReminder(t *testing.T, db *push.DB) push.DeviceReminder {
	t.Helper()
	rs, err := db.Reminders()
	if err != nil || len(rs) != 1 {
		t.Fatalf("Reminders = %+v, %v; want one", rs, err)
	}
	return rs[0]
}

func TestDropReminderLeavesAReplacedOne(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.SetReminder(reminderDevice, testReminder(1000)); err != nil {
		t.Fatal(err)
	}
	old := readReminder(t, db)
	// The page replaces the reminder while the old one is being sent.
	if _, err := db.SetReminder(reminderDevice, testReminder(2000)); err != nil {
		t.Fatal(err)
	}
	if err := db.DropReminder(old); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := db.Reminder("r1"); !ok || got != testReminder(2000) {
		t.Errorf("Reminder = %+v, %v; want the replacement kept", got, ok)
	}
	if err := db.DropReminder(readReminder(t, db)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.Reminder("r1"); ok {
		t.Error("the matching reminder stays")
	}
	if subs, _ := db.AllSubscriptions(); len(subs) != 1 {
		t.Errorf("devices = %v, want the device kept", subs)
	}
}
