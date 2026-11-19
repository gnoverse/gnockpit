package push

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
)

// Reminder is a device's reminder before a height, set from its ETA card.
type Reminder struct {
	Chain  string
	Height int64
	Since  string // the card's since, RFC 3339
	Label  string
	Lead   Lead
}

// DeviceReminder is a reminder and the device it goes to.
type DeviceReminder struct {
	Sub      Subscription
	Reminder Reminder
}

// SetReminder stores sub as SaveDevice does, its alerts untouched, and
// replaces its reminder with r. It returns the device's id.
func (d *DB) SetReminder(sub Subscription, r Reminder) (string, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	id, err := upsertDevice(tx, sub)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(
		`INSERT OR REPLACE INTO push_reminders (subscription_id, chain, height, since, label, lead) VALUES (?, ?, ?, ?, ?, ?)`,
		id, r.Chain, r.Height, r.Since, r.Label, r.Lead.String(),
	); err != nil {
		return "", fmt.Errorf("save reminder: %w", err)
	}
	return id, tx.Commit()
}

// Reminder returns the reminder of device id, if it has one.
func (d *DB) Reminder(id string) (Reminder, bool, error) {
	var r Reminder
	var lead string
	err := d.db.QueryRow(`SELECT chain, height, since, label, lead FROM push_reminders WHERE subscription_id = ?`, id).
		Scan(&r.Chain, &r.Height, &r.Since, &r.Label, &lead)
	if errors.Is(err, sql.ErrNoRows) {
		return Reminder{}, false, nil
	}
	if err == nil {
		r.Lead, err = ParseLead(lead)
	}
	if err != nil {
		return Reminder{}, false, fmt.Errorf("reminder of %s: %w", id, err)
	}
	return r, true, nil
}

// Reminders returns every device's reminder.
func (d *DB) Reminders() ([]DeviceReminder, error) {
	rows, err := d.db.Query(`SELECT s.id, s.endpoint, s.p256dh, s.auth, r.chain, r.height, r.since, r.label, r.lead
		FROM push_reminders r JOIN subscriptions s ON s.id = r.subscription_id`)
	if err != nil {
		return nil, fmt.Errorf("reminders: %w", err)
	}
	defer rows.Close()
	var out []DeviceReminder
	for rows.Next() {
		var dr DeviceReminder
		var lead string
		if err := rows.Scan(&dr.Sub.ID, &dr.Sub.Endpoint, &dr.Sub.P256dh, &dr.Sub.Auth,
			&dr.Reminder.Chain, &dr.Reminder.Height, &dr.Reminder.Since, &dr.Reminder.Label, &lead); err != nil {
			return nil, fmt.Errorf("reminders: %w", err)
		}
		if dr.Reminder.Lead, err = ParseLead(lead); err != nil {
			return nil, fmt.Errorf("reminder of %s: %w", dr.Sub.ID, err)
		}
		out = append(out, dr)
	}
	return out, rows.Err()
}

// DeleteReminder deletes the reminder of device id, and the device when it
// has no alert left. It reports whether the device stays.
func (d *DB) DeleteReminder(id string) (bool, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM push_reminders WHERE subscription_id = ?`, id); err != nil {
		return false, fmt.Errorf("delete reminder of %s: %w", id, err)
	}
	kept, err := keepDevice(tx, id)
	if err != nil {
		return false, err
	}
	return kept, tx.Commit()
}

// evaluateReminders sends each reminder of s's chain whose lead is met, and
// deletes it; a reminder of another chain is deleted unsent.
func (m *Manager) evaluateReminders(s Schedule) {
	reminders, err := m.db.Reminders()
	if err != nil {
		log.Printf("push: %v", err)
		return
	}
	for _, dr := range reminders {
		r := dr.Reminder
		switch {
		case r.Chain != s.Chain:
		case r.Lead.Met(r.Height, s.Head, s.TimeLeft):
			m.sendPush(dr.Sub, reminderAlert(r, s))
		default:
			continue
		}
		if _, err := m.db.DeleteReminder(dr.Sub.ID); err != nil {
			log.Printf("push: %v", err)
		}
	}
}

// reminderAlert tells how far r's height is: "upgrade: #1262000 in 98
// blocks", and the time left when the model gives it.
func reminderAlert(r Reminder, s Schedule) Alert {
	title := fmt.Sprintf("#%d in %d blocks", r.Height, r.Height-s.Head)
	if s.Head >= r.Height {
		title = fmt.Sprintf("#%d reached", r.Height)
	}
	if r.Label != "" {
		title = r.Label + ": " + title
	}
	var body string
	if left, ok := s.TimeLeft(r.Height); ok && s.Head < r.Height {
		body = fmt.Sprintf("About %s left.", about(left))
	}
	return Alert{Type: AlertETAReminder, Firing: true, Notice: true, Title: title, Body: body}
}
