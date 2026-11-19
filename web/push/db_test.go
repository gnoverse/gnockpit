package push_test

import (
	"database/sql"
	"path/filepath"
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
		[]push.SubscriptionAlert{{AlertType: push.AlertChainStuck}})
	if err != nil || first != "new1" {
		t.Fatalf("first save = %q, %v; want new1", first, err)
	}
	second, err := db.SaveDevice(push.Subscription{ID: "new2", Endpoint: "https://x.com/e", P256dh: "p2", Auth: "a2"},
		[]push.SubscriptionAlert{{AlertType: push.AlertValsetChanged}})
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
	if _, err := db.SaveDevice(sub, []push.SubscriptionAlert{{AlertType: push.AlertChainStuck}}); err != nil {
		t.Fatal(err)
	}
	id, err := db.SaveDevice(sub, nil)
	if err != nil || id != "" {
		t.Errorf("save with no alert = %q, %v; want \"\": the device is gone", id, err)
	}
	if subs, _ := db.AllSubscriptions(); len(subs) != 0 {
		t.Errorf("subscriptions = %v, want none", subs)
	}
}

func TestSaveDeviceStoresTheHaltLead(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.SaveDevice(push.Subscription{ID: "d1", Endpoint: "https://x.com/e", P256dh: "p", Auth: "a"},
		[]push.SubscriptionAlert{{AlertType: push.AlertHaltApproaching, Lead: "100 blocks"}}); err != nil {
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
		[]push.SubscriptionAlert{{AlertType: push.AlertHaltApproaching, Lead: "1h"}}); err != nil {
		t.Errorf("SaveDevice with a lead on the migrated database: %v", err)
	}
}
