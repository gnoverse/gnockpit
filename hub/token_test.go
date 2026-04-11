package hub

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestTokenCreateAndVerify(t *testing.T) {
	store, err := NewTokenStore(testDB(t))
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.Create("probe-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 64 { // 32 bytes hex
		t.Fatalf("token length = %d, want 64", len(token))
	}
	name, err := store.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	if name != "probe-1" {
		t.Errorf("name = %q, want probe-1", name)
	}
}

func TestTokenVerifyInvalid(t *testing.T) {
	store, err := NewTokenStore(testDB(t))
	if err != nil {
		t.Fatal(err)
	}
	store.Create("probe-1")
	if _, err := store.Verify("badtoken"); err == nil {
		t.Error("expected error for invalid token")
	}
}

func TestTokenRevoke(t *testing.T) {
	store, err := NewTokenStore(testDB(t))
	if err != nil {
		t.Fatal(err)
	}
	token, _ := store.Create("probe-1")
	if err := store.Revoke("probe-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verify(token); err == nil {
		t.Error("expected error for revoked token")
	}
}

func TestTokenList(t *testing.T) {
	store, err := NewTokenStore(testDB(t))
	if err != nil {
		t.Fatal(err)
	}
	store.Create("a")
	store.Create("b")
	store.Revoke("b")
	tokens, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 2 {
		t.Fatalf("got %d tokens, want 2", len(tokens))
	}
	if tokens[0].Name != "a" || tokens[0].RevokedAt != "" {
		t.Errorf("tokens[0] = %+v", tokens[0])
	}
	if tokens[1].Name != "b" || tokens[1].RevokedAt == "" {
		t.Errorf("tokens[1] = %+v", tokens[1])
	}
}

func TestTokenDuplicateName(t *testing.T) {
	store, err := NewTokenStore(testDB(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("probe-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("probe-1"); err == nil {
		t.Error("expected error for duplicate name")
	}
}
