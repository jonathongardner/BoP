package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxAllowanceCatchUp = 500

var ErrAllowanceNotFound = errors.New("allowance not found")
var ErrAllowanceInvalidSplits = errors.New("split percentages must total 100")
var ErrAllowanceInvalidInterval = errors.New("interval must be between 1 and 366 days")
var ErrAllowanceInvalidDayOfMonth = errors.New("day_of_month must be between 1 and 31 for monthly allowances")
var ErrAllowanceInvalidDayOfWeek = errors.New("day_of_week must be between 0 (Sunday) and 6 (Saturday) for weekly allowances")
var ErrAllowanceNoSplits = errors.New("at least one account split is required")

type AllowanceSplit struct {
	AccountID int64 `json:"account_id"`
	Percent   int   `json:"percent"`
}

type Allowance struct {
	ID           int64            `json:"id"`
	PersonID     int64            `json:"person_id"`
	AmountCents  int64            `json:"amount_cents"`
	IntervalDays int              `json:"interval_days"`
	DayOfMonth   *int             `json:"day_of_month,omitempty"`
	DayOfWeek    *int             `json:"day_of_week,omitempty"`
	Description  string           `json:"description"`
	NextDueAt    string           `json:"next_due_at"`
	Splits       []AllowanceSplit `json:"splits"`
}

type UpsertAllowanceInput struct {
	AmountCents  int64
	IntervalDays int
	DayOfMonth   *int
	DayOfWeek    *int
	Description  string
	Splits       []AllowanceSplit
}

func (s *Store) migrateAllowances() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS allowances (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		person_id INTEGER NOT NULL UNIQUE REFERENCES persons(id) ON DELETE CASCADE,
		amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
		interval_days INTEGER NOT NULL CHECK (interval_days >= 0 AND interval_days <= 366),
		day_of_month INTEGER CHECK (day_of_month IS NULL OR (day_of_month >= 1 AND day_of_month <= 31)),
		day_of_week INTEGER CHECK (day_of_week IS NULL OR (day_of_week >= 0 AND day_of_week <= 6)),
		enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
		description TEXT NOT NULL DEFAULT '',
		next_due_at TEXT NOT NULL
	)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS allowance_splits (
		allowance_id INTEGER NOT NULL REFERENCES allowances(id) ON DELETE CASCADE,
		account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		percent INTEGER NOT NULL CHECK (percent > 0 AND percent <= 100),
		PRIMARY KEY (allowance_id, account_id)
	)`); err != nil {
		return err
	}
	if err := s.migrateAllowanceMonthDay(); err != nil {
		return err
	}
	if err := s.migrateAllowanceDayOfWeek(); err != nil {
		return err
	}
	return s.migrateAllowanceIntervalAllowsMonthly()
}

func (s *Store) migrateAllowanceDayOfWeek() error {
	_, err := s.db.Exec(`ALTER TABLE allowances ADD COLUMN day_of_week INTEGER CHECK (day_of_week IS NULL OR (day_of_week >= 0 AND day_of_week <= 6))`)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "duplicate column") {
			return err
		}
	}
	_, err = s.db.Exec(`UPDATE allowances SET day_of_week = 0 WHERE interval_days IN (7, 14) AND day_of_month IS NULL AND day_of_week IS NULL`)
	return err
}

func (s *Store) migrateAllowanceMonthDay() error {
	_, err := s.db.Exec(`ALTER TABLE allowances ADD COLUMN day_of_month INTEGER CHECK (day_of_month IS NULL OR (day_of_month >= 1 AND day_of_month <= 31))`)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "duplicate column") {
			return err
		}
	}
	// Convert legacy "every 30 days" rows to calendar monthly on the 1st.
	_, err = s.db.Exec(`UPDATE allowances SET interval_days = 0, day_of_month = 1 WHERE interval_days = 30 AND (day_of_month IS NULL OR day_of_month < 1)`)
	return err
}

func (s *Store) migrateAllowanceIntervalAllowsMonthly() error {
	var ddl string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'allowances'`).Scan(&ddl); err != nil {
		return err
	}
	ddlLower := strings.ToLower(ddl)
	if strings.Contains(ddlLower, "interval_days >= 0") || !strings.Contains(ddlLower, "interval_days >= 1") {
		return nil
	}

	if _, err := s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer func() { _, _ = s.db.Exec(`PRAGMA foreign_keys=ON`) }()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`CREATE TABLE allowances_new (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		person_id INTEGER NOT NULL UNIQUE REFERENCES persons(id) ON DELETE CASCADE,
		amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
		interval_days INTEGER NOT NULL CHECK (interval_days >= 0 AND interval_days <= 366),
		day_of_month INTEGER CHECK (day_of_month IS NULL OR (day_of_month >= 1 AND day_of_month <= 31)),
		day_of_week INTEGER CHECK (day_of_week IS NULL OR (day_of_week >= 0 AND day_of_week <= 6)),
		enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
		description TEXT NOT NULL DEFAULT '',
		next_due_at TEXT NOT NULL
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE TEMP TABLE _allowance_splits_backup AS SELECT * FROM allowance_splits`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO allowances_new (id, person_id, amount_cents, interval_days, day_of_month, day_of_week, enabled, description, next_due_at)
		SELECT id, person_id, amount_cents,
			CASE WHEN day_of_month IS NOT NULL AND day_of_month >= 1 THEN 0 ELSE interval_days END,
			day_of_month, day_of_week, enabled, description, next_due_at
		FROM allowances`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE allowances`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE allowances_new RENAME TO allowances`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM allowance_splits`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO allowance_splits SELECT * FROM _allowance_splits_backup`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ProcessDueAllowances(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM allowances WHERE enabled = 1`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, id := range ids {
		if err := s.processAllowanceDue(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) processAllowanceDue(ctx context.Context, allowanceID int64) error {
	a, err := s.getAllowanceByID(ctx, allowanceID)
	if err != nil {
		return err
	}
	splits := a.Splits

	now := time.Now().UTC()
	nextDue, err := parseSQLiteTime(a.NextDueAt)
	if err != nil {
		return fmt.Errorf("allowance %d: invalid next_due_at: %w", allowanceID, err)
	}
	desc, err := normalizeDescription(a.Description)
	if err != nil {
		return err
	}
	if desc == "" {
		desc = "Allowance"
	}

	applied := 0
	for !nextDue.After(now) && applied < maxAllowanceCatchUp {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}

		amounts := distributeAllowanceAmount(a.AmountCents, splits)
		for accountID, cents := range amounts {
			if cents <= 0 {
				continue
			}
			if err := depositInTx(ctx, tx, accountID, cents, desc); err != nil {
				_ = tx.Rollback()
				return err
			}
		}

		nextDue = advanceAllowanceDue(nextDue, a.IntervalDays, a.DayOfMonth, a.DayOfWeek)
		if _, err := tx.ExecContext(ctx, `UPDATE allowances SET next_due_at = ? WHERE id = ?`, formatSQLiteTime(nextDue), allowanceID); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		applied++
	}
	return nil
}

func distributeAllowanceAmount(total int64, splits []AllowanceSplit) map[int64]int64 {
	out := make(map[int64]int64, len(splits))
	var allocated int64
	for i, sp := range splits {
		if i == len(splits)-1 {
			out[sp.AccountID] = total - allocated
			continue
		}
		amt := total * int64(sp.Percent) / 100
		out[sp.AccountID] = amt
		allocated += amt
	}
	return out
}

func depositInTx(ctx context.Context, tx *sql.Tx, accountID, amountCents int64, description string) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM accounts WHERE id = ?`, accountID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAccountNotFound
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance_cents = balance_cents + ? WHERE id = ?`, amountCents, accountID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO transactions (account_id, kind, amount_cents, description) VALUES (?, 'deposit', ?, ?)`,
		accountID, amountCents, description,
	)
	return err
}

func (s *Store) GetAllowanceForPerson(ctx context.Context, personID int64) (*Allowance, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM persons WHERE id = ?`, personID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPersonNotFound
		}
		return nil, err
	}

	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM allowances WHERE person_id = ?`, personID,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAllowanceNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.getAllowanceByID(ctx, id)
}

func (s *Store) getAllowanceByID(ctx context.Context, allowanceID int64) (*Allowance, error) {
	var a Allowance
	var dayOfMonth, dayOfWeek sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, person_id, amount_cents, interval_days, day_of_month, day_of_week, ifnull(description, ''), next_due_at
		 FROM allowances WHERE id = ?`, allowanceID,
	).Scan(&a.ID, &a.PersonID, &a.AmountCents, &a.IntervalDays, &dayOfMonth, &dayOfWeek, &a.Description, &a.NextDueAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAllowanceNotFound
		}
		return nil, err
	}
	if dayOfMonth.Valid {
		d := int(dayOfMonth.Int64)
		a.DayOfMonth = &d
	}
	if dayOfWeek.Valid {
		d := int(dayOfWeek.Int64)
		a.DayOfWeek = &d
	}

	splits, err := s.listAllowanceSplits(ctx, allowanceID)
	if err != nil {
		return nil, err
	}
	a.Splits = splits
	return &a, nil
}

func (s *Store) listAllowanceSplits(ctx context.Context, allowanceID int64) ([]AllowanceSplit, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT account_id, percent FROM allowance_splits WHERE allowance_id = ? ORDER BY account_id`,
		allowanceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AllowanceSplit
	for rows.Next() {
		var sp AllowanceSplit
		if err := rows.Scan(&sp.AccountID, &sp.Percent); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	if out == nil {
		out = []AllowanceSplit{}
	}
	return out, rows.Err()
}

func (s *Store) UpsertAllowance(ctx context.Context, personID int64, in UpsertAllowanceInput) (*Allowance, error) {
	if in.AmountCents <= 0 {
		return nil, ErrInvalidAmount
	}
	intervalDays, dayOfMonth, dayOfWeek, err := normalizeAllowanceSchedule(in.IntervalDays, in.DayOfMonth, in.DayOfWeek)
	if err != nil {
		return nil, err
	}
	if len(in.Splits) == 0 {
		return nil, ErrAllowanceNoSplits
	}
	if err := validateAllowanceSplits(ctx, s.db, personID, in.Splits); err != nil {
		return nil, err
	}
	desc, err := normalizeDescription(in.Description)
	if err != nil {
		return nil, err
	}

	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM persons WHERE id = ?`, personID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPersonNotFound
		}
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var allowanceID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM allowances WHERE person_id = ?`, personID).Scan(&allowanceID)
	isNew := errors.Is(err, sql.ErrNoRows)
	if err != nil && !isNew {
		return nil, err
	}

	if isNew {
		now := time.Now().UTC()
		nextDue := initialNextDue(now, intervalDays, dayOfMonth, dayOfWeek)
		res, err := tx.ExecContext(ctx,
			`INSERT INTO allowances (person_id, amount_cents, interval_days, day_of_month, day_of_week, enabled, description, next_due_at)
			 VALUES (?, ?, ?, ?, ?, 1, ?, ?)`,
			personID, in.AmountCents, intervalDays, nullInt(dayOfMonth), nullInt(dayOfWeek), desc, formatSQLiteTime(nextDue),
		)
		if err != nil {
			return nil, err
		}
		allowanceID, err = res.LastInsertId()
		if err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.ExecContext(ctx,
			`UPDATE allowances SET amount_cents = ?, interval_days = ?, day_of_month = ?, day_of_week = ?, description = ? WHERE id = ?`,
			in.AmountCents, intervalDays, nullInt(dayOfMonth), nullInt(dayOfWeek), desc, allowanceID,
		); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM allowance_splits WHERE allowance_id = ?`, allowanceID); err != nil {
			return nil, err
		}
	}

	for _, sp := range in.Splits {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO allowance_splits (allowance_id, account_id, percent) VALUES (?, ?, ?)`,
			allowanceID, sp.AccountID, sp.Percent,
		); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetAllowanceForPerson(ctx, personID)
}

func (s *Store) DeleteAllowance(ctx context.Context, personID int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM allowances WHERE person_id = ?`, personID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrAllowanceNotFound
	}
	return nil
}

func (s *Store) loadAllowanceForPerson(ctx context.Context, personID int64) (*Allowance, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM allowances WHERE person_id = ?`, personID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.getAllowanceByID(ctx, id)
}

func validateAllowanceSplits(ctx context.Context, q sqlQuerier, personID int64, splits []AllowanceSplit) error {
	sum := 0
	seen := make(map[int64]struct{}, len(splits))
	for _, sp := range splits {
		if sp.Percent <= 0 || sp.Percent > 100 {
			return fmt.Errorf("each split percent must be between 1 and 100")
		}
		sum += sp.Percent
		if _, dup := seen[sp.AccountID]; dup {
			return fmt.Errorf("duplicate account in splits")
		}
		seen[sp.AccountID] = struct{}{}

		var acctPerson int64
		err := q.QueryRowContext(ctx, `SELECT person_id FROM accounts WHERE id = ?`, sp.AccountID).Scan(&acctPerson)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAccountNotFound
		}
		if err != nil {
			return err
		}
		if acctPerson != personID {
			return fmt.Errorf("account does not belong to this person")
		}
	}
	if sum != 100 {
		return ErrAllowanceInvalidSplits
	}
	return nil
}

type sqlQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func parseSQLiteTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unrecognized time format")
}

func formatSQLiteTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func nullInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
