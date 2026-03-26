package push_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gnoverse/gnockpit/web/push"
)

func TestManager_VAPIDKeys_GeneratedOnFirstRun(t *testing.T) {
	db, err := push.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	m, err := push.NewManager(db, 30, 10)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	pub := m.VAPIDPublicKey()
	if pub == "" {
		t.Fatal("expected non-empty VAPID public key")
	}
}

func TestManager_VAPIDKeys_PersistAcrossReloads(t *testing.T) {
	db, err := push.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	m1, err := push.NewManager(db, 30, 10)
	if err != nil {
		t.Fatalf("NewManager (first): %v", err)
	}
	pub1 := m1.VAPIDPublicKey()

	m2, err := push.NewManager(db, 30, 10)
	if err != nil {
		t.Fatalf("NewManager (second): %v", err)
	}
	if m2.VAPIDPublicKey() != pub1 {
		t.Error("expected same VAPID public key on second load")
	}
}

func testSendPushRemovesStaleSubscription(t *testing.T, statusCode int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
	}))
	defer srv.Close()

	db, err := push.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	m, err := push.NewManager(db, 30, 10)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := db.SaveSubscription(push.Subscription{
		ID:       "dead",
		Endpoint: srv.URL,
		P256dh:   "BNNL5ZaTfK81qhXOx23-wewhigUeFb632jN6LvRWCFH1ubQr77FE_9qV1FuojuRmHP42zmf34rXgW80OvUVDgTk",
		Auth:     "zqbxT6JKstKSY9JKibZLSQ",
	}); err != nil {
		t.Fatalf("SaveSubscription: %v", err)
	}
	if err := db.SaveSubscriptionAlerts("dead", []push.SubscriptionAlert{
		{SubscriptionID: "dead", AlertType: push.AlertChainStuck, EntityID: ""},
	}); err != nil {
		t.Fatalf("SaveSubscriptionAlerts: %v", err)
	}

	m.NotifyAlert(push.Alert{
		Type: push.AlertChainStuck, EntityID: "", Firing: true,
		Title: "Test", Body: "test",
	})

	subs, err := db.AllSubscriptions()
	if err != nil {
		t.Fatalf("AllSubscriptions: %v", err)
	}
	if len(subs) != 0 {
		t.Errorf("status %d: expected stale subscription removed, got %d", statusCode, len(subs))
	}
}

func TestManager_SendPush_RemovesGoneSubscription(t *testing.T) {
	testSendPushRemovesStaleSubscription(t, http.StatusGone)
}

func TestManager_SendPush_RemovesBadRequestSubscription(t *testing.T) {
	// WNS (Edge on Windows) returns 400 for expired channels instead of 410.
	testSendPushRemovesStaleSubscription(t, http.StatusBadRequest)
}
