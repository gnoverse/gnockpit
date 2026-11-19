package blocks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PutValset stores the members of the validator set with hash, unless it is
// stored. A set has members: an empty one is refused, not stored for good.
func (s *Store) PutValset(ctx context.Context, hash string, members []Member) error {
	if len(members) == 0 {
		return fmt.Errorf("validator set %s: no member", hash)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO blocks_valsets(hash) VALUES(?)`, hash)
	if err != nil {
		return fmt.Errorf("insert validator set %s: %w", hash, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("insert validator set %s: %w", hash, err)
	}
	if n == 0 {
		return nil
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("validator set %s id: %w", hash, err)
	}
	ids, err := loadValidatorIDs(ctx, tx)
	if err != nil {
		return err
	}
	for i, m := range members {
		vid, err := ids.id(ctx, m.Address)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE blocks_validators SET pub_key = ? WHERE id = ? AND pub_key IS NULL`, m.PubKey, vid,
		); err != nil {
			return fmt.Errorf("record the key of %s: %w", m.Address, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO blocks_valset_members(valset, idx, validator, power) VALUES(?, ?, ?, ?)`, id, i, vid, m.Power,
		); err != nil {
			return fmt.Errorf("insert member %d of validator set %s: %w", i, hash, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit validator set %s: %w", hash, err)
	}
	return nil
}

// Valset returns the members of the stored validator set with hash, in set
// order.
func (s *Store) Valset(ctx context.Context, hash string) ([]Member, bool, error) {
	var out []Member
	err := s.each(ctx, func(rows *sql.Rows) error {
		var m Member
		var key sql.NullString
		if err := rows.Scan(&m.Address, &key, &m.Power); err != nil {
			return err
		}
		m.PubKey = key.String
		out = append(out, m)
		return nil
	}, `SELECT v.address, v.pub_key, m.power FROM blocks_valset_members m
	    JOIN blocks_valsets s ON s.id = m.valset JOIN blocks_validators v ON v.id = m.validator
	    WHERE s.hash = ? ORDER BY m.idx`, hash)
	if err != nil {
		return nil, false, fmt.Errorf("validator set %s: %w", hash, err)
	}
	return out, len(out) > 0, nil
}

// validatorIDs resolves addresses to their blocks_validators ids within a
// transaction, adding the addresses not stored yet.
type validatorIDs struct {
	tx  *sql.Tx
	ids map[string]int64
}

func loadValidatorIDs(ctx context.Context, tx *sql.Tx) (*validatorIDs, error) {
	v := &validatorIDs{tx: tx, ids: map[string]int64{}}
	rows, err := tx.QueryContext(ctx, `SELECT id, address FROM blocks_validators`)
	if err != nil {
		return nil, fmt.Errorf("load validators: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var addr string
		if err := rows.Scan(&id, &addr); err != nil {
			return nil, fmt.Errorf("load validators: %w", err)
		}
		v.ids[addr] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load validators: %w", err)
	}
	return v, nil
}

func (v *validatorIDs) id(ctx context.Context, addr string) (int64, error) {
	if id, ok := v.ids[addr]; ok {
		return id, nil
	}
	res, err := v.tx.ExecContext(ctx, `INSERT INTO blocks_validators(address) VALUES(?)`, addr)
	if err != nil {
		return 0, fmt.Errorf("add validator %s: %w", addr, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("add validator %s: %w", addr, err)
	}
	v.ids[addr] = id
	return id, nil
}

// ValsetHashAt returns the hash of the validator set of the stored height.
func (s *Store) ValsetHashAt(ctx context.Context, height int64) (string, bool, error) {
	var hash string
	err := s.db.QueryRowContext(ctx,
		`SELECT v.hash FROM blocks b JOIN blocks_valsets v ON v.id = b.valset WHERE b.height = ?`, height,
	).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("validator set at %d: %w", height, err)
	}
	return hash, true, nil
}
