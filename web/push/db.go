package push

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

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
	Lead          Lead // halt_approaching's
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

// SQL returns the underlying database handle so sibling stores (e.g. the
// block history) can share the single connection.
func (d *DB) SQL() *sql.DB { return d.db }

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
			lead            TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (subscription_id, alert_type, entity_id)
		);
		CREATE TABLE IF NOT EXISTS push_sent (
			owner      TEXT NOT NULL, -- device:<id> or target:<fingerprint>
			alert_type TEXT NOT NULL,
			key        TEXT NOT NULL, -- <chain>:<height>
			PRIMARY KEY (owner, alert_type, key)
		) WITHOUT ROWID;
		CREATE TABLE IF NOT EXISTS push_reminders (
			subscription_id TEXT PRIMARY KEY REFERENCES subscriptions(id) ON DELETE CASCADE,
			chain           TEXT NOT NULL,
			height          INTEGER NOT NULL,
			since           TEXT NOT NULL,
			label           TEXT NOT NULL,
			lead            TEXT NOT NULL
		);
	`); err != nil {
		return err
	}
	if err := migrateDevices(db); err != nil {
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

// SaveDevice stores sub, keeping its id when its endpoint is known, and
// replaces its alerts with alerts, but for the stored alerts that stay: those
// of the types keep names, and those on a validator that shown, the addresses
// of the validators the saving menu showed, does not list and alerts does not
// name. It returns the device's id, or "" when the device has no alert and no
// reminder left and was deleted.
func (d *DB) SaveDevice(sub Subscription, alerts []SubscriptionAlert, keep []AlertType, shown []string) (string, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	id, err := upsertDevice(tx, sub)
	if err != nil {
		return "", err
	}
	// Kept whole, the validator alerts need no list of the unshown ones.
	var unshown []string
	if !slices.Contains(keep, AlertValidatorMissingBlocks) {
		if unshown, err = unshownValidators(tx, id, alerts, shown); err != nil {
			return "", err
		}
	}
	if err := replaceAlerts(tx, id, alerts, keep, unshown); err != nil {
		return "", err
	}
	kept, err := keepDevice(tx, id)
	if err != nil {
		return "", err
	}
	if !kept {
		id = ""
	}
	return id, tx.Commit()
}

// upsertDevice stores sub under the id its endpoint has, or sub.ID for a new
// endpoint, and returns that id.
func upsertDevice(tx *sql.Tx, sub Subscription) (string, error) {
	id := sub.ID
	err := tx.QueryRow(`SELECT id FROM subscriptions WHERE endpoint = ?`, sub.Endpoint).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.Exec(`INSERT INTO subscriptions (id, endpoint, p256dh, auth) VALUES (?, ?, ?, ?)`, id, sub.Endpoint, sub.P256dh, sub.Auth)
	case err == nil:
		_, err = tx.Exec(`UPDATE subscriptions SET p256dh = ?, auth = ? WHERE id = ?`, sub.P256dh, sub.Auth, id)
	}
	if err != nil {
		return "", fmt.Errorf("save device: %w", err)
	}
	return id, nil
}

// unshownValidators returns the validators device id has a stored alert on
// that shown does not list and alerts does not name.
func unshownValidators(tx *sql.Tx, id string, alerts []SubscriptionAlert, shown []string) ([]string, error) {
	rows, err := tx.Query(
		`SELECT entity_id FROM subscription_alerts WHERE subscription_id = ? AND alert_type = ?`,
		id, string(AlertValidatorMissingBlocks),
	)
	if err != nil {
		return nil, fmt.Errorf("read validator alerts: %w", err)
	}
	defer rows.Close()
	var unshown []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("read validator alerts: %w", err)
		}
		named := slices.ContainsFunc(alerts, func(a SubscriptionAlert) bool {
			return a.AlertType == AlertValidatorMissingBlocks && a.EntityID == v
		})
		if !named && !slices.Contains(shown, v) {
			unshown = append(unshown, v)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read validator alerts: %w", err)
	}
	return unshown, nil
}

// keepDevice deletes the device id when it has no alert and no reminder
// left, and reports whether it stays.
func keepDevice(tx *sql.Tx, id string) (bool, error) {
	var left int
	if err := tx.QueryRow(
		`SELECT (SELECT COUNT(*) FROM subscription_alerts WHERE subscription_id = ?) + (SELECT COUNT(*) FROM push_reminders WHERE subscription_id = ?)`,
		id, id,
	).Scan(&left); err != nil {
		return false, fmt.Errorf("device %s: %w", id, err)
	}
	if left > 0 {
		return true, nil
	}
	if _, err := tx.Exec(`DELETE FROM subscriptions WHERE id = ?`, id); err != nil {
		return false, fmt.Errorf("delete device: %w", err)
	}
	return false, nil
}

// SaveSubscriptionAlerts replaces all alert preferences for a subscription atomically.
func (d *DB) SaveSubscriptionAlerts(subscriptionID string, alerts []SubscriptionAlert) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := replaceAlerts(tx, subscriptionID, alerts, nil, nil); err != nil {
		return err
	}
	return tx.Commit()
}

// replaceAlerts replaces the alerts of subscriptionID with alerts, but for its
// stored alerts of the types keep names and its validator alerts on the
// validators unshown lists, which stay.
func replaceAlerts(tx *sql.Tx, subscriptionID string, alerts []SubscriptionAlert, keep []AlertType, unshown []string) error {
	drop := `DELETE FROM subscription_alerts WHERE subscription_id = ?`
	args := []any{subscriptionID}
	if len(keep) > 0 {
		drop += ` AND alert_type NOT IN (` + placeholders(len(keep)) + `)`
		for _, t := range keep {
			args = append(args, string(t))
		}
	}
	if len(unshown) > 0 {
		drop += ` AND NOT (alert_type = ? AND entity_id IN (` + placeholders(len(unshown)) + `))`
		args = append(args, string(AlertValidatorMissingBlocks))
		for _, v := range unshown {
			args = append(args, v)
		}
	}
	if _, err := tx.Exec(drop, args...); err != nil {
		return fmt.Errorf("clear alerts: %w", err)
	}
	for _, a := range alerts {
		recovery := 0
		if a.RecoveryNotif {
			recovery = 1
		}
		if _, err := tx.Exec(
			`INSERT INTO subscription_alerts (subscription_id, alert_type, entity_id, recovery_notif, lead) VALUES (?, ?, ?, ?, ?)`,
			subscriptionID, string(a.AlertType), a.EntityID, recovery, a.Lead,
		); err != nil {
			return fmt.Errorf("save alert %s: %w", a.AlertType, err)
		}
	}
	return nil
}

// placeholders is the list of n query parameters an IN clause takes, n ≥ 1.
func placeholders(n int) string {
	return `?` + strings.Repeat(`, ?`, n-1)
}

// migrateDevices keeps one subscription per push endpoint (the latest row,
// the alerts of the others going with them), makes the endpoint unique, and
// adds the alerts' lead column to a database from before it.
func migrateDevices(db *sql.DB) error {
	if _, err := db.Exec(`
		DELETE FROM subscriptions WHERE rowid NOT IN (SELECT MAX(rowid) FROM subscriptions GROUP BY endpoint);
		CREATE UNIQUE INDEX IF NOT EXISTS subscriptions_endpoint ON subscriptions(endpoint);
	`); err != nil {
		return fmt.Errorf("one device per endpoint: %w", err)
	}
	var lead int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('subscription_alerts') WHERE name = 'lead'`).Scan(&lead); err != nil {
		return fmt.Errorf("read subscription_alerts columns: %w", err)
	}
	if lead == 0 {
		if _, err := db.Exec(`ALTER TABLE subscription_alerts ADD COLUMN lead TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add the alerts' lead: %w", err)
		}
	}
	return nil
}

// MarkSent records that the alert of type t keyed key went to owner, and
// reports whether it had not yet.
func (d *DB) MarkSent(owner string, t AlertType, key string) (bool, error) {
	res, err := d.db.Exec(`INSERT OR IGNORE INTO push_sent (owner, alert_type, key) VALUES (?, ?, ?)`, owner, string(t), key)
	if err != nil {
		return false, fmt.Errorf("mark %s sent to %s: %w", t, owner, err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// SubscribersForAlert returns all subscriptions opted in for the given alert + entity combination.
func (d *DB) SubscribersForAlert(alertType AlertType, entityID string) ([]Subscriber, error) {
	rows, err := d.db.Query(`
		SELECT s.id, s.endpoint, s.p256dh, s.auth, sa.recovery_notif, sa.lead
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
		var lead string
		if err := rows.Scan(&sb.Sub.ID, &sb.Sub.Endpoint, &sb.Sub.P256dh, &sb.Sub.Auth, &recoveryInt, &lead); err != nil {
			return nil, err
		}
		sb.RecoveryNotif = recoveryInt == 1
		if lead != "" {
			var err error
			if sb.Lead, err = ParseLead(lead); err != nil {
				return nil, fmt.Errorf("subscription %s: %w", sb.Sub.ID, err)
			}
		}
		result = append(result, sb)
	}
	return result, rows.Err()
}
