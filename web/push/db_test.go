package push_test

import (
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

func TestDB_AlertState_RoundTrip(t *testing.T) {
	db, _ := push.OpenDB(":memory:")
	defer db.Close()

	firing, err := db.IsAlertFiring(push.AlertChainStuck, "")
	if err != nil {
		t.Fatalf("IsAlertFiring: %v", err)
	}
	if firing {
		t.Error("expected not firing initially")
	}

	if err := db.SetAlertFiring(push.AlertChainStuck, "", true); err != nil {
		t.Fatalf("SetAlertFiring: %v", err)
	}
	firing, _ = db.IsAlertFiring(push.AlertChainStuck, "")
	if !firing {
		t.Error("expected firing after set")
	}
}

func TestDB_SubscriptionAlerts_SaveAndLoad(t *testing.T) {
	db, _ := push.OpenDB(":memory:")
	defer db.Close()

	db.SaveSubscription(push.Subscription{ID: "s1", Endpoint: "https://x.com", P256dh: "p", Auth: "a"})

	alerts := []push.SubscriptionAlert{
		{SubscriptionID: "s1", AlertType: push.AlertChainStuck, EntityID: "", RecoveryNotif: true},
		{SubscriptionID: "s1", AlertType: push.AlertValidatorMissingVotes, EntityID: "g1aaa", RecoveryNotif: false},
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
