//go:build page

package web

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakePushService stands in for the browser vendor's push service: it
// answers each delivery 201, or with the code failNext set, and records it.
type fakePushService struct {
	srv *httptest.Server

	mu         sync.Mutex
	deliveries []pushDelivery
	nextStatus int // the code of the next answer; 0 for 201
}

// pushDelivery is one message gnockpit sent to the push service.
type pushDelivery struct {
	endpoint string // the subscription endpoint it was posted to
	ttl      int    // its TTL header, in seconds
	bodyLen  int    // the length of its encrypted body
}

func newFakePushService(t *testing.T) *fakePushService {
	t.Helper()
	f := &fakePushService{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePushService) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	ttl, err := strconv.Atoi(r.Header.Get("TTL"))
	if err != nil {
		http.Error(w, "bad TTL", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.deliveries = append(f.deliveries, pushDelivery{endpoint: f.srv.URL + r.URL.Path, ttl: ttl, bodyLen: len(body)})
	status := http.StatusCreated
	if f.nextStatus != 0 {
		status, f.nextStatus = f.nextStatus, 0
	}
	f.mu.Unlock()
	w.WriteHeader(status)
}

// received returns the deliveries so far, oldest first.
func (f *fakePushService) received() []pushDelivery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.deliveries)
}

// failNext answers the next delivery with status instead of 201.
func (f *fakePushService) failNext(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextStatus = status
}

// pushStub is the push subscription the browser holds in a test: an
// endpoint on the fake push service and the keys the server encrypts to.
type pushStub struct {
	endpoint   string
	p256dh     []byte // an uncompressed P-256 public key
	auth       []byte // the 16-byte authentication secret
	serverKey  []byte // the server's VAPID public key, the only one subscribe takes
	permission notificationPermission
}

// notificationPermission is the browser's notification permission in a
// test: its state until the page asks for it, then the answer it gets.
// Headless Chromium reads "denied" whatever the context was granted, so the
// stub sets it.
type notificationPermission struct {
	State  string `json:"state"`
	Answer string `json:"answer"`
}

// withNotificationPermission makes the push stub's notification permission
// read state, and asking for it answer answer; the stub grants it otherwise.
// The scenario must have push.
func withNotificationPermission(state, answer string) pageOption {
	return func(c *pageConfig) {
		c.notificationPermission = &notificationPermission{State: state, Answer: answer}
	}
}

// newPushStub makes a subscription on f for the server whose VAPID public
// key is serverKey, base64url-encoded as the server gives it. chosen is the
// notification permission withNotificationPermission set; nil grants it.
func newPushStub(t *testing.T, f *fakePushService, serverKey string, chosen *notificationPermission) *pushStub {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("push stub key: %v", err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatalf("push stub auth: %v", err)
	}
	vapid, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(serverKey, "="))
	if err != nil {
		t.Fatalf("push stub: the server's VAPID key %q: %v", serverKey, err)
	}
	permission := notificationPermission{State: "granted", Answer: "granted"}
	if chosen != nil {
		if chosen.State == "" || chosen.Answer == "" {
			t.Fatalf("withNotificationPermission(%q, %q): want a state and an answer", chosen.State, chosen.Answer)
		}
		permission = *chosen
	}
	return &pushStub{
		endpoint:   f.srv.URL + "/push/" + artifactName.ReplaceAllString(t.Name(), "_"),
		p256dh:     key.PublicKey().Bytes(),
		auth:       auth,
		serverKey:  vapid,
		permission: permission,
	}
}

// initScript replaces the browser's push subscription with the stub's. The
// subscription is held from the start, as by a browser that subscribed
// before. unsubscribe ends it for the tab, reloads included; subscribe holds
// it again, and refuses options other than the server's key with
// userVisibleOnly, as a push service does. The notification permission is
// the stub's too.
func (s *pushStub) initScript() string {
	// The keys go as base64 for getKey and as base64url for toJSON, as a
	// browser's subscription gives them.
	cfg, err := json.Marshal(struct {
		Endpoint   string                 `json:"endpoint"`
		P256dh     string                 `json:"p256dh"`
		Auth       string                 `json:"auth"`
		P256dhURL  string                 `json:"p256dhURL"`
		AuthURL    string                 `json:"authURL"`
		ServerKey  string                 `json:"serverKey"`
		Permission notificationPermission `json:"permission"`
	}{
		Endpoint:   s.endpoint,
		P256dh:     base64.StdEncoding.EncodeToString(s.p256dh),
		Auth:       base64.StdEncoding.EncodeToString(s.auth),
		P256dhURL:  base64.RawURLEncoding.EncodeToString(s.p256dh),
		AuthURL:    base64.RawURLEncoding.EncodeToString(s.auth),
		ServerKey:  base64.StdEncoding.EncodeToString(s.serverKey),
		Permission: s.permission,
	})
	if err != nil {
		panic(fmt.Sprintf("push stub config: %v", err))
	}
	return fmt.Sprintf(pushStubScript, cfg)
}

// pushStubScript is the init script of the push stub; %s is its config. The
// subscription's end is kept in sessionStorage, which outlives a reload.
const pushStubScript = `(() => {
    if (!('PushManager' in window)) return
    const cfg = %s
    if ('Notification' in window) {
        let permission = cfg.permission.state
        Object.defineProperty(Notification, 'permission', {
            configurable: true,
            get: () => permission,
        })
        Notification.requestPermission = async () => {
            permission = cfg.permission.answer
            return permission
        }
    }
    const ENDED = 'gnockpit-test-push-unsubscribed'
    const toBuffer = (b64) => Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)).buffer
    // keyBytes reads an applicationServerKey: a buffer, a view or a
    // base64url string; null for anything else.
    const keyBytes = (key) => {
        if (typeof key === 'string') {
            const b64 = key.replace(/-/g, '+').replace(/_/g, '/')
            return new Uint8Array(toBuffer(b64 + '='.repeat((4 - (b64.length %% 4)) %% 4)))
        }
        if (key instanceof ArrayBuffer) return new Uint8Array(key)
        if (ArrayBuffer.isView(key)) return new Uint8Array(key.buffer, key.byteOffset, key.byteLength)
        return null
    }
    const serverKey = new Uint8Array(toBuffer(cfg.serverKey))
    const isServerKey = (key) => {
        const got = keyBytes(key)
        return got !== null && got.length === serverKey.length && got.every((b, i) => b === serverKey[i])
    }
    const sub = {
        endpoint: cfg.endpoint,
        expirationTime: null,
        options: { userVisibleOnly: true, applicationServerKey: serverKey.buffer },
        getKey(name) {
            if (name === 'p256dh') return toBuffer(cfg.p256dh)
            if (name === 'auth') return toBuffer(cfg.auth)
            return null
        },
        toJSON() {
            return {
                endpoint: cfg.endpoint,
                expirationTime: null,
                keys: { p256dh: cfg.p256dhURL, auth: cfg.authURL },
            }
        },
        async unsubscribe() {
            sessionStorage.setItem(ENDED, '1')
            return true
        },
    }
    PushManager.prototype.getSubscription = async function () {
        return sessionStorage.getItem(ENDED) === null ? sub : null
    }
    PushManager.prototype.subscribe = async function (options) {
        if (!options || options.userVisibleOnly !== true || !isServerKey(options.applicationServerKey)) {
            throw new DOMException('the push service takes the server key with userVisibleOnly only', 'InvalidAccessError')
        }
        sessionStorage.removeItem(ENDED)
        return sub
    }
})()
`
