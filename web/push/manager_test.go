package push_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gnoverse/gnockpit/web/push"
)

func TestManager_VAPIDKeys_GeneratedOnFirstRun(t *testing.T) {
	db, _ := push.OpenDB(":memory:")
	defer db.Close()

	m, err := push.NewManager(db, 30)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	pub := m.VAPIDPublicKey()
	if pub == "" {
		t.Fatal("expected non-empty VAPID public key")
	}
}

func TestManager_VAPIDKeys_PersistAcrossReloads(t *testing.T) {
	db, _ := push.OpenDB(":memory:")
	defer db.Close()

	m1, _ := push.NewManager(db, 30)
	pub1 := m1.VAPIDPublicKey()

	m2, _ := push.NewManager(db, 30)
	if m2.VAPIDPublicKey() != pub1 {
		t.Error("expected same VAPID public key on second load")
	}
}

func TestManager_SendPush_RemovesGoneSubscription(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()

	db, _ := push.OpenDB(":memory:")
	defer db.Close()
	m, _ := push.NewManager(db, 30)

	db.SaveSubscription(push.Subscription{
		ID:     "dead",
		Endpoint: srv.URL,
		P256dh: "BNNL5ZaTfK81qhXOx23-wewhigUeFb632jN6LvRWCFH1ubQr77FE_9qV1FuojuRmHP42zmf34rXgW80OvUVDgTk",
		Auth:   "zqbxT6JKstKSY9JKibZLSQ",
	})
	db.SaveSubscriptionAlerts("dead", []push.SubscriptionAlert{
		{SubscriptionID: "dead", AlertType: push.AlertChainStuck, EntityID: ""},
	})

	m.NotifyAlert(push.Alert{
		Type: push.AlertChainStuck, EntityID: "", Firing: true,
		Title: "Test", Body: "test",
	})

	subs, _ := db.AllSubscriptions()
	if len(subs) != 0 {
		t.Errorf("expected dead subscription removed after 410, got %d", len(subs))
	}
}
