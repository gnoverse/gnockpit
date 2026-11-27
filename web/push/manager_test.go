package push_test

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gnoverse/gnockpit/node"
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

// logsOf sends the log into a buffer until the test ends.
func logsOf(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

func testSendPushRemovesStaleSubscription(t *testing.T, statusCode int) {
	t.Helper()
	logs := logsOf(t)
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
		Title: "Test", Body: push.Body{{Text: "test"}},
	})

	subs, err := db.AllSubscriptions()
	if err != nil {
		t.Fatalf("AllSubscriptions: %v", err)
	}
	if len(subs) != 0 {
		t.Errorf("status %d: expected stale subscription removed, got %d", statusCode, len(subs))
	}
	if want := fmt.Sprintf("push: subscription dead expired (%d), removing", statusCode); !strings.Contains(logs.String(), want) {
		t.Errorf("logs = %q, want %q", logs, want)
	}
}

func TestManager_SendPush_RemovesGoneSubscription(t *testing.T) {
	testSendPushRemovesStaleSubscription(t, http.StatusGone)
}

func TestManager_SendPush_RemovesBadRequestSubscription(t *testing.T) {
	// WNS (Edge on Windows) returns 400 for expired channels instead of 410.
	testSendPushRemovesStaleSubscription(t, http.StatusBadRequest)
}

// The keys of a browser's push subscription, which a push is encrypted to.
const (
	testP256dh = "BNNL5ZaTfK81qhXOx23-wewhigUeFb632jN6LvRWCFH1ubQr77FE_9qV1FuojuRmHP42zmf34rXgW80OvUVDgTk"
	testAuth   = "zqbxT6JKstKSY9JKibZLSQ"
)

// managerWithDevice returns a manager on an in-memory database closed with
// the test, holding device "test-sub" whose push service is at endpoint.
func managerWithDevice(t *testing.T, endpoint string) *push.Manager {
	t.Helper()
	db, err := push.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	m, err := push.NewManager(db, 30, 10)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := db.SaveSubscription(push.Subscription{ID: "test-sub", Endpoint: endpoint, P256dh: testP256dh, Auth: testAuth}); err != nil {
		t.Fatalf("SaveSubscription: %v", err)
	}
	return m
}

func TestManager_SendTest_Success(t *testing.T) {
	logs := logsOf(t)
	var received bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	m := managerWithDevice(t, srv.URL)

	if err := m.SendTest("test-sub"); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	if !received {
		t.Error("expected push service to receive a request")
	}
	if want := "push: send to test-sub: status 201"; !strings.Contains(logs.String(), want) {
		t.Errorf("logs = %q, want %q", logs, want)
	}
}

func TestManager_SendTest_NotFound(t *testing.T) {
	m := managerWithDevice(t, "https://push.example/unused")
	if err := m.SendTest("nonexistent"); !errors.Is(err, push.ErrSubscriptionNotFound) {
		t.Errorf("SendTest(nonexistent) = %v, want %v", err, push.ErrSubscriptionNotFound)
	}
}

// TestManager_SendTest_ReturnsThePushServiceFailure checks that a test push
// the push service refuses, or never answers, is an ErrDelivery error that
// carries the push service's status and the start of its answer, quoted so
// that it forges no log line; the status is logged once, by the caller.
func TestManager_SendTest_ReturnsThePushServiceFailure(t *testing.T) {
	const said = "push service says no\nforged: log line"
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string // the error's end
	}{
		{name: "gone", status: http.StatusGone, body: said, want: fmt.Sprintf("status 410: %q", said)},
		{name: "server error", status: http.StatusInternalServerError, body: said, want: fmt.Sprintf("status 500: %q", said)},
		{
			name: "long answer", status: http.StatusBadGateway, body: strings.Repeat("x", 2000),
			want: fmt.Sprintf("status 502: %q", strings.Repeat("x", 1024)),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := logsOf(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			m := managerWithDevice(t, srv.URL)

			err := m.SendTest("test-sub")
			if !errors.Is(err, push.ErrDelivery) || !strings.HasSuffix(err.Error(), tc.want) {
				t.Errorf("SendTest = %v, want an ErrDelivery error ending %s", err, tc.want)
			}
			if status := fmt.Sprintf("status %d", tc.status); strings.Contains(logs.String(), status) {
				t.Errorf("logs = %q, want no %q: the error carries it", logs, status)
			}
		})
	}
	t.Run("unreachable", func(t *testing.T) {
		logsOf(t)
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()
		m := managerWithDevice(t, srv.URL)

		if err := m.SendTest("test-sub"); !errors.Is(err, push.ErrDelivery) {
			t.Errorf("SendTest = %v, want an ErrDelivery error", err)
		}
	})
}

func TestManager_EvaluateAndNotify_CapturesChainName(t *testing.T) {
	db, err := push.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	m, err := push.NewManager(db, 30, 10)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	snap := &node.Snapshot{
		Status: &node.Status{
			NodeInfo: node.NodeInfo{Network: "mychain"},
			SyncInfo: node.SyncInfo{LatestBlockHeight: "100"},
		},
		Consensus: &node.ConsensusState{},
	}
	m.EvaluateAndNotify(snap, 0)
	if m.ChainName() != "mychain" {
		t.Errorf("ChainName() = %q, want %q", m.ChainName(), "mychain")
	}
}
