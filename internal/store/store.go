package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

var ErrInsufficientFunds = errors.New("insufficient funds")
var ErrInvalidAmount = errors.New("amount must be positive")
var ErrAccountNotFound = errors.New("account not found")
var ErrPersonNotFound = errors.New("person not found")
var ErrDescriptionTooLong = errors.New("description must be at most 255 characters")
var ErrAccountHasBalance = errors.New("account balance must be zero to delete")
var ErrTransactionNotFound = errors.New("transaction not found")
var ErrDeleteTransactionBalance = errors.New("cannot delete transaction: balance would become negative")

type Person struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Account struct {
	ID           int64  `json:"id"`
	PersonID     int64  `json:"person_id"`
	Name         string `json:"name"`
	BalanceCents int64  `json:"balance_cents"`
}

type PersonWithAccounts struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	Accounts []Account `json:"accounts"`
}

type Transaction struct {
	ID          int64  `json:"id"`
	AccountID   int64  `json:"account_id"`
	Kind        string `json:"kind"`
	AmountCents int64  `json:"amount_cents"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_busy_timeout=5000", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(time.Hour)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS persons (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE COLLATE NOCASE
	)`); err != nil {
		return err
	}

	accountsExists, err := s.tableExists("accounts")
	if err != nil {
		return err
	}

	if !accountsExists {
		if _, err := s.db.Exec(`CREATE TABLE accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			person_id INTEGER NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			balance_cents INTEGER NOT NULL DEFAULT 0 CHECK (balance_cents >= 0),
			UNIQUE(person_id, name COLLATE NOCASE)
		)`); err != nil {
			return err
		}
		if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS transactions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
			kind TEXT NOT NULL CHECK (kind IN ('deposit', 'withdraw')),
			amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
			description TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`); err != nil {
			return err
		}
		if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_transactions_account ON transactions(account_id, created_at DESC)`); err != nil {
			return err
		}
		return s.migrateTransactionDescription()
	}

	hasPersonID, err := s.columnExists("accounts", "person_id")
	if err != nil {
		return err
	}
	if !hasPersonID {
		if err := s.migrateAccountsAddPersonColumn(); err != nil {
			return err
		}
	}

	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS transactions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		kind TEXT NOT NULL CHECK (kind IN ('deposit', 'withdraw')),
		amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
		description TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_transactions_account ON transactions(account_id, created_at DESC)`); err != nil {
		return err
	}
	if err := s.migrateTransactionDescription(); err != nil {
		return err
	}
	return nil
}

func (s *Store) tableExists(name string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) columnExists(table, col string) (bool, error) {
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return false, err
		}
		if strings.EqualFold(c, col) {
			return true, rows.Err()
		}
	}
	return false, rows.Err()
}

func (s *Store) migrateAccountsAddPersonColumn() error {
	if _, err := s.db.Exec(`INSERT INTO persons (name) SELECT 'Household' WHERE NOT EXISTS (SELECT 1 FROM persons LIMIT 1)`); err != nil {
		return err
	}
	var pid int64
	if err := s.db.QueryRow(`SELECT id FROM persons ORDER BY id LIMIT 1`).Scan(&pid); err != nil {
		return err
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

	if _, err := tx.Exec(`ALTER TABLE accounts RENAME TO accounts_old`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE TABLE accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		person_id INTEGER NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		balance_cents INTEGER NOT NULL DEFAULT 0 CHECK (balance_cents >= 0),
		UNIQUE(person_id, name COLLATE NOCASE)
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO accounts (id, person_id, name, balance_cents)
		SELECT id, ?, name, balance_cents FROM accounts_old`, pid); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE accounts_old`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *Store) migrateTransactionDescription() error {
	_, err := s.db.Exec(`ALTER TABLE transactions ADD COLUMN description TEXT NOT NULL DEFAULT ''`)
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "duplicate column") {
		return nil
	}
	return err
}

func (s *Store) CreatePerson(ctx context.Context, name string) (*Person, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("name required")
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO persons (name) VALUES (?)`, name)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &Person{ID: id, Name: name}, nil
}

func (s *Store) ListPersonsWithAccounts(ctx context.Context) ([]PersonWithAccounts, error) {
	pRows, err := s.db.QueryContext(ctx, `SELECT id, name FROM persons ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	var personsOrder []PersonWithAccounts
	for pRows.Next() {
		var p PersonWithAccounts
		if err := pRows.Scan(&p.ID, &p.Name); err != nil {
			_ = pRows.Close()
			return nil, err
		}
		personsOrder = append(personsOrder, p)
	}
	if err := pRows.Err(); err != nil {
		_ = pRows.Close()
		return nil, err
	}
	if err := pRows.Close(); err != nil {
		return nil, err
	}

	out := make([]PersonWithAccounts, 0, len(personsOrder))
	for _, p := range personsOrder {
		accts, err := s.listAccountsForPerson(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		if accts == nil {
			accts = []Account{}
		}
		p.Accounts = accts
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) listAccountsForPerson(ctx context.Context, personID int64) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, person_id, name, balance_cents FROM accounts WHERE person_id = ? ORDER BY name COLLATE NOCASE`,
		personID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.PersonID, &a.Name, &a.BalanceCents); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, person_id, name, balance_cents FROM accounts ORDER BY person_id, name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.PersonID, &a.Name, &a.BalanceCents); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) CreateAccount(ctx context.Context, personID int64, name string) (*Account, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("name required")
	}
	if personID <= 0 {
		return nil, fmt.Errorf("person required")
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM persons WHERE id = ?`, personID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPersonNotFound
		}
		return nil, err
	}

	res, err := s.db.ExecContext(ctx, `INSERT INTO accounts (person_id, name) VALUES (?, ?)`, personID, name)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &Account{ID: id, PersonID: personID, Name: name, BalanceCents: 0}, nil
}

func (s *Store) ListTransactions(ctx context.Context, accountID int64) ([]Transaction, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, account_id, kind, amount_cents, ifnull(description, ''), created_at FROM transactions WHERE account_id = ? ORDER BY datetime(created_at) DESC, id DESC`,
		accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.ID, &t.AccountID, &t.Kind, &t.AmountCents, &t.Description, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func normalizeDescription(s string) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 255 {
		return "", ErrDescriptionTooLong
	}
	return s, nil
}

func (s *Store) Deposit(ctx context.Context, accountID int64, amountCents int64, description string) error {
	if amountCents <= 0 {
		return ErrInvalidAmount
	}
	desc, err := normalizeDescription(description)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

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
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO transactions (account_id, kind, amount_cents, description) VALUES (?, 'deposit', ?, ?)`,
		accountID, amountCents, desc,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Withdraw(ctx context.Context, accountID int64, amountCents int64, description string) error {
	if amountCents <= 0 {
		return ErrInvalidAmount
	}
	desc, err := normalizeDescription(description)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`UPDATE accounts SET balance_cents = balance_cents - ? WHERE id = ? AND balance_cents >= ?`,
		amountCents, accountID, amountCents,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var bal int64
		err := tx.QueryRowContext(ctx, `SELECT balance_cents FROM accounts WHERE id = ?`, accountID).Scan(&bal)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrAccountNotFound
			}
			return err
		}
		return ErrInsufficientFunds
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO transactions (account_id, kind, amount_cents, description) VALUES (?, 'withdraw', ?, ?)`,
		accountID, amountCents, desc,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteAccount(ctx context.Context, accountID int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE id = ? AND balance_cents = 0`, accountID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	var bal int64
	err = s.db.QueryRowContext(ctx, `SELECT balance_cents FROM accounts WHERE id = ?`, accountID).Scan(&bal)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAccountNotFound
		}
		return err
	}
	return ErrAccountHasBalance
}

func (s *Store) DeleteTransaction(ctx context.Context, accountID, transactionID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var kind string
	var amount int64
	err = tx.QueryRowContext(ctx,
		`SELECT kind, amount_cents FROM transactions WHERE id = ? AND account_id = ?`,
		transactionID, accountID,
	).Scan(&kind, &amount)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTransactionNotFound
		}
		return err
	}

	switch kind {
	case "deposit":
		var bal int64
		if err := tx.QueryRowContext(ctx, `SELECT balance_cents FROM accounts WHERE id = ?`, accountID).Scan(&bal); err != nil {
			return err
		}
		if bal < amount {
			return ErrDeleteTransactionBalance
		}
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance_cents = balance_cents - ? WHERE id = ?`, amount, accountID); err != nil {
			return err
		}
	case "withdraw":
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance_cents = balance_cents + ? WHERE id = ?`, amount, accountID); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown transaction kind")
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM transactions WHERE id = ? AND account_id = ?`, transactionID, accountID); err != nil {
		return err
	}
	return tx.Commit()
}
