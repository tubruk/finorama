package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/chickenzord/finorama/internal/crypto"
	"github.com/chickenzord/finorama/internal/model"
	_ "modernc.org/sqlite"
)

var (
	ErrNotInitialized  = errors.New("finorama not initialized: master passphrase not set")
	ErrAccountNotFound = errors.New("account not found")
)

type Store struct {
	db *sql.DB
	mu sync.Mutex
}

func Open(dbPath string) (*Store, error) {
	// Enable WAL mode and busy timeout for concurrent safety
	dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// DB wrappers providing context-aware execution
func (s *Store) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("exec query: %w", err)
	}
	return res, nil
}

func (s *Store) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return rows, nil
}

func (s *Store) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, query, args...)
}

func (s *Store) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	return tx, nil
}

func (s *Store) migrate(ctx context.Context) error {
	query := `
	CREATE TABLE IF NOT EXISTS system_config (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		username TEXT NOT NULL UNIQUE,
		encrypted_password TEXT NOT NULL,
		is_active INTEGER NOT NULL DEFAULT 1,
		last_synced_at DATETIME,
		last_sync_error TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS holdings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		captured_at DATETIME NOT NULL,
		captured_date TEXT NOT NULL,
		asset_type TEXT NOT NULL,
		security_code TEXT NOT NULL,
		security_name TEXT NOT NULL,
		participant TEXT NOT NULL DEFAULT '',
		currency TEXT NOT NULL DEFAULT 'IDR',
		quantity REAL NOT NULL DEFAULT 0,
		price REAL NOT NULL DEFAULT 0,
		total_value REAL NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(captured_date, account_id, asset_type, security_code, participant)
	);

	CREATE INDEX IF NOT EXISTS idx_holdings_acc_captured ON holdings(account_id, captured_at DESC);
	CREATE INDEX IF NOT EXISTS idx_holdings_date ON holdings(captured_date);
	CREATE INDEX IF NOT EXISTS idx_holdings_asset ON holdings(asset_type);
	`
	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("execute schema migration: %w", err)
	}
	return nil
}

// IsInitialized checks if master password is configured.
func (s *Store) IsInitialized(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM system_config WHERE key IN ('master_salt', 'master_hash')").Scan(&count)
	if err != nil {
		return false, fmt.Errorf("scan config count: %w", err)
	}
	return count == 2, nil
}

// InitializeMaster sets up master salt and hash from passphrase. Returns derived AES key.
func (s *Store) InitializeMaster(ctx context.Context, passphrase string) ([]byte, error) {
	init, err := s.IsInitialized(ctx)
	if err != nil {
		return nil, fmt.Errorf("check initialized: %w", err)
	}
	if init {
		return nil, errors.New("finorama already initialized")
	}

	salt, err := crypto.GenerateSalt()
	if err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	hash := crypto.HashPassphrase(passphrase, salt)
	saltB64 := base64.StdEncoding.EncodeToString(salt)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "INSERT INTO system_config (key, value) VALUES ('master_salt', ?), ('master_hash', ?)", saltB64, hash); err != nil {
		return nil, fmt.Errorf("insert master credentials: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit master credentials: %w", err)
	}

	return crypto.DeriveKey(passphrase, salt), nil
}

// VerifyAndDeriveKey validates passphrase against stored master hash.
func (s *Store) VerifyAndDeriveKey(ctx context.Context, passphrase string) ([]byte, bool, error) {
	var saltB64, masterHash string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM system_config WHERE key = 'master_salt'").Scan(&saltB64)
	if err != nil {
		return nil, false, fmt.Errorf("query master_salt: %w", err)
	}
	err = s.db.QueryRowContext(ctx, "SELECT value FROM system_config WHERE key = 'master_hash'").Scan(&masterHash)
	if err != nil {
		return nil, false, fmt.Errorf("query master_hash: %w", err)
	}

	salt, err := base64.StdEncoding.DecodeString(saltB64)
	if err != nil {
		return nil, false, fmt.Errorf("decode master_salt: %w", err)
	}

	if !crypto.VerifyPassphrase(passphrase, salt, masterHash) {
		return nil, false, nil
	}

	return crypto.DeriveKey(passphrase, salt), true, nil
}

// AddAccount creates account with encrypted password.
func (s *Store) AddAccount(ctx context.Context, key []byte, name, username, password string) (*model.Account, error) {
	encPass, err := crypto.Encrypt(key, []byte(password))
	if err != nil {
		return nil, fmt.Errorf("encrypt password: %w", err)
	}

	now := time.Now()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO accounts (name, username, encrypted_password, is_active, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?)
	`, name, username, encPass, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert account: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("get last insert id: %w", err)
	}

	return &model.Account{
		ID:        id,
		Name:      name,
		Username:  username,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// ListAccounts returns all registered accounts.
func (s *Store) ListAccounts(ctx context.Context) ([]model.Account, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, username, encrypted_password, is_active, last_synced_at, last_sync_error, created_at, updated_at
		FROM accounts ORDER BY id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query accounts: %w", err)
	}
	defer rows.Close()

	var accounts []model.Account
	for rows.Next() {
		var a model.Account
		var lastSync sql.NullTime
		var lastErr sql.NullString
		if err := rows.Scan(&a.ID, &a.Name, &a.Username, &a.EncryptedPassword, &a.IsActive, &lastSync, &lastErr, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan account row: %w", err)
		}
		if lastSync.Valid {
			a.LastSyncedAt = &lastSync.Time
		}
		if lastErr.Valid {
			a.LastSyncError = lastErr.String
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accounts: %w", err)
	}
	return accounts, nil
}

// GetAccountByID retrieves account by ID.
func (s *Store) GetAccountByID(ctx context.Context, id int64) (*model.Account, error) {
	var a model.Account
	var lastSync sql.NullTime
	var lastErr sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, username, encrypted_password, is_active, last_synced_at, last_sync_error, created_at, updated_at
		FROM accounts WHERE id = ?
	`, id).Scan(&a.ID, &a.Name, &a.Username, &a.EncryptedPassword, &a.IsActive, &lastSync, &lastErr, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan account: %w", err)
	}
	if lastSync.Valid {
		a.LastSyncedAt = &lastSync.Time
	}
	if lastErr.Valid {
		a.LastSyncError = lastErr.String
	}
	return &a, nil
}

// DeleteAccount removes an account and associated holdings/snapshots.
func (s *Store) DeleteAccount(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM accounts WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	return nil
}

// UpdateAccountSyncStatus updates last sync time and error message.
func (s *Store) UpdateAccountSyncStatus(ctx context.Context, accountID int64, syncErr error) error {
	now := time.Now()
	errStr := ""
	if syncErr != nil {
		errStr = syncErr.Error()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE accounts
		SET last_synced_at = ?, last_sync_error = ?, updated_at = ?
		WHERE id = ?
	`, now, errStr, now, accountID)
	if err != nil {
		return fmt.Errorf("update account sync status: %w", err)
	}
	return nil
}

// SaveAccountHoldings records or updates holdings for an account on capturedAt date.
func (s *Store) SaveAccountHoldings(ctx context.Context, accountID int64, holdings []model.Holding, capturedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save holdings tx: %w", err)
	}
	defer tx.Rollback()

	capturedDate := capturedAt.Format("2006-01-02")

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO holdings (
			account_id, captured_at, captured_date, asset_type, security_code,
			security_name, participant, currency, quantity, price, total_value
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(captured_date, account_id, asset_type, security_code, participant) DO UPDATE SET
			captured_at = excluded.captured_at,
			quantity = excluded.quantity,
			price = excluded.price,
			total_value = excluded.total_value,
			security_name = excluded.security_name
	`)
	if err != nil {
		return fmt.Errorf("prepare insert holding stmt: %w", err)
	}
	defer stmt.Close()

	for _, h := range holdings {
		if _, err := stmt.ExecContext(ctx, 
			accountID, capturedAt, capturedDate, h.AssetType, h.SecurityCode,
			h.SecurityName, h.Participant, h.Currency, h.Quantity, h.Price, h.TotalValue,
		); err != nil {
			return fmt.Errorf("insert holding (%s): %w", h.SecurityCode, err)
		}
	}

	// Update account last sync
	now := time.Now()
	_, err = tx.ExecContext(ctx, `
		UPDATE accounts
		SET last_synced_at = ?, last_sync_error = '', updated_at = ?
		WHERE id = ?
	`, now, now, accountID)
	if err != nil {
		return fmt.Errorf("update account last_synced: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save holdings tx: %w", err)
	}
	return nil
}

// GetHoldingsByAccountID returns the latest holdings for a specific account.
func (s *Store) GetHoldingsByAccountID(ctx context.Context, accountID int64) ([]model.Holding, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT h.id, h.account_id, a.name, h.captured_at, h.captured_date, h.asset_type, h.security_code, h.security_name,
		       h.participant, h.currency, h.quantity, h.price, h.total_value
		FROM holdings h
		JOIN accounts a ON h.account_id = a.id
		WHERE h.account_id = ?
		  AND h.captured_date = (
			SELECT MAX(h2.captured_date) FROM holdings h2 WHERE h2.account_id = ?
		  )
		ORDER BY h.total_value DESC
	`, accountID, accountID)
	if err != nil {
		return nil, fmt.Errorf("query holdings by account: %w", err)
	}
	defer rows.Close()

	var holdings []model.Holding
	for rows.Next() {
		var h model.Holding
		if err := rows.Scan(&h.ID, &h.AccountID, &h.AccountName, &h.CapturedAt, &h.CapturedDate, &h.AssetType, &h.SecurityCode, &h.SecurityName, &h.Participant, &h.Currency, &h.Quantity, &h.Price, &h.TotalValue); err != nil {
			return nil, fmt.Errorf("scan holding row: %w", err)
		}
		holdings = append(holdings, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate holdings: %w", err)
	}
	return holdings, nil
}

// GetPortfolioOverview calculates totals and aggregates current holdings and historical snapshots.
func (s *Store) GetPortfolioOverview(ctx context.Context) (*model.PortfolioOverview, error) {
	overview := &model.PortfolioOverview{
		Accounts:   make([]model.AccountSummary, 0),
		Holdings:   make([]model.Holding, 0),
		Historical: make([]model.DailyAggregate, 0),
	}

	// Latest holdings per account
	latestHoldingsSubquery := `
		SELECT h.*
		FROM holdings h
		INNER JOIN (
			SELECT account_id, MAX(captured_date) as max_date
			FROM holdings
			GROUP BY account_id
		) latest ON h.account_id = latest.account_id AND h.captured_date = latest.max_date
	`

	// Per-account totals from their latest capture
	accRows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT a.id, a.name, a.username, a.last_synced_at, COALESCE(SUM(lh.total_value), 0) as total_val
		FROM accounts a
		LEFT JOIN (%s) lh ON a.id = lh.account_id
		WHERE a.is_active = 1
		GROUP BY a.id
		ORDER BY a.name ASC
	`, latestHoldingsSubquery))
	if err != nil {
		return nil, fmt.Errorf("query account summaries: %w", err)
	}
	defer accRows.Close()

	for accRows.Next() {
		var sm model.AccountSummary
		var lastSync sql.NullTime
		if err := accRows.Scan(&sm.AccountID, &sm.AccountName, &sm.Username, &lastSync, &sm.TotalValue); err != nil {
			return nil, fmt.Errorf("scan account summary row: %w", err)
		}
		if lastSync.Valid {
			sm.LastSyncedAt = &lastSync.Time
		}
		overview.Accounts = append(overview.Accounts, sm)
		overview.TotalValue += sm.TotalValue
	}
	if err := accRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account summaries: %w", err)
	}

	// Asset breakdown totals from latest holdings
	assetRows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT asset_type, COALESCE(SUM(total_value), 0)
		FROM (%s)
		GROUP BY asset_type
	`, latestHoldingsSubquery))
	if err != nil {
		return nil, fmt.Errorf("query asset breakdown: %w", err)
	}
	defer assetRows.Close()

	for assetRows.Next() {
		var assetType string
		var val float64
		if err := assetRows.Scan(&assetType, &val); err != nil {
			return nil, fmt.Errorf("scan asset breakdown row: %w", err)
		}
		switch assetType {
		case "EQUITY":
			overview.TotalEquity = val
		case "MUTUAL_FUND":
			overview.TotalMutualFund = val
		case "BOND":
			overview.TotalBond = val
		case "CASH":
			overview.TotalCash = val
		default:
			overview.TotalOther += val
		}
	}
	if err := assetRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate asset breakdown: %w", err)
	}

	// Current latest holdings list across accounts
	holdRows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT lh.id, lh.account_id, a.name, lh.captured_at, lh.captured_date, lh.asset_type,
		       lh.security_code, lh.security_name, lh.participant, lh.currency, lh.quantity, lh.price, lh.total_value
		FROM (%s) lh
		JOIN accounts a ON lh.account_id = a.id
		ORDER BY lh.total_value DESC
	`, latestHoldingsSubquery))
	if err != nil {
		return nil, fmt.Errorf("query holdings: %w", err)
	}
	defer holdRows.Close()

	for holdRows.Next() {
		var h model.Holding
		if err := holdRows.Scan(&h.ID, &h.AccountID, &h.AccountName, &h.CapturedAt, &h.CapturedDate, &h.AssetType, &h.SecurityCode, &h.SecurityName, &h.Participant, &h.Currency, &h.Quantity, &h.Price, &h.TotalValue); err != nil {
			return nil, fmt.Errorf("scan holding row: %w", err)
		}
		overview.Holdings = append(overview.Holdings, h)
	}
	if err := holdRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate holdings: %w", err)
	}

	// Historical aggregated timeseries (sum of holdings value per captured_date on the fly)
	histRows, err := s.db.QueryContext(ctx, `
		SELECT captured_date, SUM(total_value) as total_val
		FROM holdings
		GROUP BY captured_date
		ORDER BY captured_date ASC
		LIMIT 90
	`)
	if err != nil {
		return nil, fmt.Errorf("query history: %w", err)
	}
	defer histRows.Close()

	for histRows.Next() {
		var d model.DailyAggregate
		if err := histRows.Scan(&d.Date, &d.TotalValue); err != nil {
			return nil, fmt.Errorf("scan history row: %w", err)
		}
		overview.Historical = append(overview.Historical, d)
	}
	if err := histRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate history: %w", err)
	}

	return overview, nil
}
