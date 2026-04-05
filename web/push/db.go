package push

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite database for push notification persistence.
type DB struct {
	db *sql.DB
}

// Subscriber pairs a subscription with its recovery notification preference.
type Subscriber struct {
	Sub           Subscription
	RecoveryNotif bool
}

// OpenDB opens (or creates) the SQLite database at path and initializes the schema.
// Use ":memory:" for tests.
func OpenDB(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if err := initSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &DB{db: db}, nil
}

// Close closes the underlying database connection.
func (d *DB) Close() error { return d.db.Close() }

func initSchema(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS vapid_keys (
			id          INTEGER PRIMARY KEY,
			public_key  TEXT NOT NULL,
			private_key TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS subscriptions (
			id         TEXT PRIMARY KEY,
			endpoint   TEXT NOT NULL,
			p256dh     TEXT NOT NULL,
			auth       TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS subscription_alerts (
			subscription_id TEXT NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
			alert_type      TEXT NOT NULL,
			entity_id       TEXT NOT NULL DEFAULT '',
			recovery_notif  INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (subscription_id, alert_type, entity_id)
		);
	`); err != nil {
		return err
	}
	// Migrate renamed alert type (idempotent).
	_, err := db.Exec(`
		UPDATE subscription_alerts
		SET alert_type = 'validator_missing_blocks'
		WHERE alert_type = 'validator_missing_votes'
	`)
	return err
}

// ---- VAPID keys

// SaveVAPIDKeys persists the VAPID key pair (upsert).
func (d *DB) SaveVAPIDKeys(public, private string) error {
	_, err := d.db.Exec(
		`INSERT OR REPLACE INTO vapid_keys (id, public_key, private_key) VALUES (1, ?, ?)`,
		public, private,
	)
	return err
}

// LoadVAPIDKeys returns the stored VAPID key pair, or ("", "", nil) if none exist.
func (d *DB) LoadVAPIDKeys() (public, private string, err error) {
	row := d.db.QueryRow(`SELECT public_key, private_key FROM vapid_keys WHERE id = 1`)
	err = row.Scan(&public, &private)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	return public, private, err
}

// ---- Subscriptions

// SaveSubscription upserts a subscription row.
func (d *DB) SaveSubscription(sub Subscription) error {
	_, err := d.db.Exec(
		`INSERT OR REPLACE INTO subscriptions (id, endpoint, p256dh, auth) VALUES (?, ?, ?, ?)`,
		sub.ID, sub.Endpoint, sub.P256dh, sub.Auth,
	)
	return err
}

// DeleteSubscription removes a subscription and its alert preferences (cascade).
func (d *DB) DeleteSubscription(id string) error {
	_, err := d.db.Exec(`DELETE FROM subscriptions WHERE id = ?`, id)
	return err
}

// AllSubscriptions returns all stored subscriptions.
func (d *DB) AllSubscriptions() ([]Subscription, error) {
	rows, err := d.db.Query(`SELECT id, endpoint, p256dh, auth FROM subscriptions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var subs []Subscription
	for rows.Next() {
		var s Subscription
		if err := rows.Scan(&s.ID, &s.Endpoint, &s.P256dh, &s.Auth); err != nil {
			return nil, err
		}
		subs = append(subs, s)
	}
	return subs, rows.Err()
}

// GetSubscription returns a single subscription by ID.
func (d *DB) GetSubscription(id string) (Subscription, error) {
	var s Subscription
	err := d.db.QueryRow(
		`SELECT id, endpoint, p256dh, auth FROM subscriptions WHERE id = ?`, id,
	).Scan(&s.ID, &s.Endpoint, &s.P256dh, &s.Auth)
	if err != nil {
		return Subscription{}, err
	}
	return s, nil
}

// ---- Subscription alerts

// SaveSubscriptionWithAlerts saves a subscription and its alert preferences atomically.
// If alerts is empty the subscription is saved with no preferences.
func (d *DB) SaveSubscriptionWithAlerts(sub Subscription, alerts []SubscriptionAlert) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT OR REPLACE INTO subscriptions (id, endpoint, p256dh, auth) VALUES (?, ?, ?, ?)`,
		sub.ID, sub.Endpoint, sub.P256dh, sub.Auth,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM subscription_alerts WHERE subscription_id = ?`, sub.ID); err != nil {
		return err
	}
	for _, a := range alerts {
		recovery := 0
		if a.RecoveryNotif {
			recovery = 1
		}
		if _, err := tx.Exec(
			`INSERT INTO subscription_alerts (subscription_id, alert_type, entity_id, recovery_notif) VALUES (?, ?, ?, ?)`,
			sub.ID, string(a.AlertType), a.EntityID, recovery,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SaveSubscriptionAlerts replaces all alert preferences for a subscription atomically.
func (d *DB) SaveSubscriptionAlerts(subscriptionID string, alerts []SubscriptionAlert) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM subscription_alerts WHERE subscription_id = ?`, subscriptionID); err != nil {
		return err
	}
	for _, a := range alerts {
		recovery := 0
		if a.RecoveryNotif {
			recovery = 1
		}
		if _, err := tx.Exec(
			`INSERT INTO subscription_alerts (subscription_id, alert_type, entity_id, recovery_notif) VALUES (?, ?, ?, ?)`,
			subscriptionID, string(a.AlertType), a.EntityID, recovery,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SubscribersForAlert returns all subscriptions opted in for the given alert + entity combination.
func (d *DB) SubscribersForAlert(alertType AlertType, entityID string) ([]Subscriber, error) {
	rows, err := d.db.Query(`
		SELECT s.id, s.endpoint, s.p256dh, s.auth, sa.recovery_notif
		FROM subscriptions s
		JOIN subscription_alerts sa ON sa.subscription_id = s.id
		WHERE sa.alert_type = ? AND sa.entity_id = ?
	`, string(alertType), entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Subscriber
	for rows.Next() {
		var sb Subscriber
		var recoveryInt int
		if err := rows.Scan(&sb.Sub.ID, &sb.Sub.Endpoint, &sb.Sub.P256dh, &sb.Sub.Auth, &recoveryInt); err != nil {
			return nil, err
		}
		sb.RecoveryNotif = recoveryInt == 1
		result = append(result, sb)
	}
	return result, rows.Err()
}

