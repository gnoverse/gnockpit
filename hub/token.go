package hub

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// TokenStore manages bearer tokens for probe authentication.
type TokenStore struct {
	db *sql.DB
}

// TokenInfo is a token row for display (never exposes the raw token).
type TokenInfo struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	RevokedAt string `json:"revoked_at,omitempty"`
	LastSeen  string `json:"last_seen,omitempty"`
}

// NewTokenStore wraps a sql.DB and ensures the tokens table exists.
func NewTokenStore(db *sql.DB) (*TokenStore, error) {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS tokens (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			name        TEXT NOT NULL UNIQUE,
			token_hash  TEXT NOT NULL,
			created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			revoked_at  DATETIME,
			last_seen   DATETIME
		)
	`); err != nil {
		return nil, fmt.Errorf("create tokens table: %w", err)
	}
	return &TokenStore{db: db}, nil
}

// Create generates a new bearer token for a probe with the given name.
// Returns the raw token (shown once to the operator).
func (s *TokenStore) Create(name string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	token := hex.EncodeToString(raw)
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash token: %w", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO tokens (name, token_hash) VALUES (?, ?)`,
		name, string(hash),
	); err != nil {
		return "", fmt.Errorf("insert token: %w", err)
	}
	return token, nil
}

// Verify checks a raw bearer token against all active (non-revoked) tokens.
// Returns the token name on success, or an error.
func (s *TokenStore) Verify(rawToken string) (name string, err error) {
	// Load all tokens into memory first, then close rows before doing bcrypt
	// (avoids deadlock with SetMaxOpenConns(1) when updating last_seen)
	type tokenRow struct {
		name string
		hash string
	}
	rows, err := s.db.Query(`SELECT name, token_hash FROM tokens WHERE revoked_at IS NULL`)
	if err != nil {
		return "", err
	}
	var tokens []tokenRow
	for rows.Next() {
		var t tokenRow
		if err := rows.Scan(&t.name, &t.hash); err != nil {
			rows.Close()
			return "", err
		}
		tokens = append(tokens, t)
	}
	rows.Close()

	for _, t := range tokens {
		if bcrypt.CompareHashAndPassword([]byte(t.hash), []byte(rawToken)) == nil {
			s.db.Exec(`UPDATE tokens SET last_seen = ? WHERE name = ?`, time.Now().UTC().Format(time.RFC3339), t.name)
			return t.name, nil
		}
	}
	return "", fmt.Errorf("invalid token")
}

// Revoke marks a token as revoked by name.
func (s *TokenStore) Revoke(name string) error {
	res, err := s.db.Exec(
		`UPDATE tokens SET revoked_at = ? WHERE name = ? AND revoked_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339), name,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("token %q not found or already revoked", name)
	}
	return nil
}

// List returns all tokens (active and revoked).
func (s *TokenStore) List() ([]TokenInfo, error) {
	rows, err := s.db.Query(`SELECT id, name, created_at, COALESCE(revoked_at,''), COALESCE(last_seen,'') FROM tokens ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []TokenInfo
	for rows.Next() {
		var t TokenInfo
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt, &t.RevokedAt, &t.LastSeen); err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}
