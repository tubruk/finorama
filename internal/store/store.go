package store

import (
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
	ErrNotInitialized = errors.New("finorama not initialized: master passphrase not set")
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

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
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

	CREATE TABLE IF NOT EXISTS latest_holdings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		asset_type TEXT NOT NULL,
		security_code TEXT NOT NULL,
		security_name TEXT NOT NULL,
		participant TEXT NOT NULL DEFAULT '',
		currency TEXT NOT NULL DEFAULT 'IDR',
		quantity REAL NOT NULL DEFAULT 0,
		price REAL NOT NULL DEFAULT 0,
		total_value REAL NOT NULL DEFAULT 0,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(account_id, asset_type, security_code, participant)
	);

	CREATE TABLE IF NOT EXISTS daily_snapshots (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		date TEXT NOT NULL,
		account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		total_value REAL NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(date, account_id)
	);

	CREATE INDEX IF NOT EXISTS idx_holdings_account ON latest_holdings(account_id);
	CREATE INDEX IF NOT EXISTS idx_holdings_asset ON latest_holdings(asset_type);
	CREATE INDEX IF NOT EXISTS idx_snapshots_date ON daily_snapshots(date);
	`
	_, err := s.db.Exec(query)
	return err
}

// IsInitialized checks if master password is configured.
func (s *Store) IsInitialized() (bool, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM system_config WHERE key IN ('master_salt', 'master_hash')").Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 2, nil
}

// InitializeMaster sets up master salt and hash from passphrase. Returns derived AES key.
func (s *Store) InitializeMaster(passphrase string) ([]byte, error) {
	init, err := s.IsInitialized()
	if err != nil {
		return nil, err
	}
	if init {
		return nil, errors.New("finorama already initialized")
	}

	salt, err := crypto.GenerateSalt()
	if err != nil {
		return nil, err
	}

	hash := crypto.HashPassphrase(passphrase, salt)
	saltB64 := base64.StdEncoding.EncodeToString(salt)

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("INSERT INTO system_config (key, value) VALUES ('master_salt', ?), ('master_hash', ?)", saltB64, hash); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return crypto.DeriveKey(passphrase, salt), nil
}

// VerifyAndDeriveKey validates passphrase against stored master hash.
func (s *Store) VerifyAndDeriveKey(passphrase string) ([]byte, bool, error) {
	var saltB64, masterHash string
	err := s.db.QueryRow("SELECT value FROM system_config WHERE key = 'master_salt'").Scan(&saltB64)
	if err != nil {
		return nil, false, err
	}
	err = s.db.QueryRow("SELECT value FROM system_config WHERE key = 'master_hash'").Scan(&masterHash)
	if err != nil {
		return nil, false, err
	}

	salt, err := base64.StdEncoding.DecodeString(saltB64)
	if err != nil {
		return nil, false, err
	}

	if !crypto.VerifyPassphrase(passphrase, salt, masterHash) {
		return nil, false, nil
	}

	return crypto.DeriveKey(passphrase, salt), true, nil
}

// AddAccount creates account with encrypted password.
func (s *Store) AddAccount(key []byte, name, username, password string) (*model.Account, error) {
	encPass, err := crypto.Encrypt(key, []byte(password))
	if err != nil {
		return nil, fmt.Errorf("encrypt password: %w", err)
	}

	now := time.Now()
	res, err := s.db.Exec(`
		INSERT INTO accounts (name, username, encrypted_password, is_active, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?)
	`, name, username, encPass, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert account: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
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
func (s *Store) ListAccounts() ([]model.Account, error) {
	rows, err := s.db.Query(`
		SELECT id, name, username, encrypted_password, is_active, last_synced_at, last_sync_error, created_at, updated_at
		FROM accounts ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []model.Account
	for rows.Next() {
		var a model.Account
		var lastSync sql.NullTime
		var lastErr sql.NullString
		if err := rows.Scan(&a.ID, &a.Name, &a.Username, &a.EncryptedPassword, &a.IsActive, &lastSync, &lastErr, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		if lastSync.Valid {
			a.LastSyncedAt = &lastSync.Time
		}
		if lastErr.Valid {
			a.LastSyncError = lastErr.String
		}
		accounts = append(accounts, a)
	}
	return accounts, nil
}

// GetAccountByID retrieves account by ID.
func (s *Store) GetAccountByID(id int64) (*model.Account, error) {
	var a model.Account
	var lastSync sql.NullTime
	var lastErr sql.NullString
	err := s.db.QueryRow(`
		SELECT id, name, username, encrypted_password, is_active, last_synced_at, last_sync_error, created_at, updated_at
		FROM accounts WHERE id = ?
	`, id).Scan(&a.ID, &a.Name, &a.Username, &a.EncryptedPassword, &a.IsActive, &lastSync, &lastErr, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, err
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
func (s *Store) DeleteAccount(id int64) error {
	_, err := s.db.Exec("DELETE FROM accounts WHERE id = ?", id)
	return err
}

// UpdateAccountSyncStatus updates last sync time and error message.
func (s *Store) UpdateAccountSyncStatus(accountID int64, syncErr error) error {
	now := time.Now()
	errStr := ""
	if syncErr != nil {
		errStr = syncErr.Error()
	}
	_, err := s.db.Exec(`
		UPDATE accounts
		SET last_synced_at = ?, last_sync_error = ?, updated_at = ?
		WHERE id = ?
	`, now, errStr, now, accountID)
	return err
}

// SaveAccountHoldings replaces all current holdings for an account and records daily snapshot.
func (s *Store) SaveAccountHoldings(accountID int64, holdings []model.Holding, totalValue float64, captureDate string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete existing latest holdings for this account
	if _, err := tx.Exec("DELETE FROM latest_holdings WHERE account_id = ?", accountID); err != nil {
		return fmt.Errorf("delete old holdings: %w", err)
	}

	stmt, err := tx.Prepare(`
		INSERT INTO latest_holdings (account_id, asset_type, security_code, security_name, participant, currency, quantity, price, total_value, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now()
	for _, h := range holdings {
		if _, err := stmt.Exec(accountID, h.AssetType, h.SecurityCode, h.SecurityName, h.Participant, h.Currency, h.Quantity, h.Price, h.TotalValue, now); err != nil {
			return fmt.Errorf("insert holding (%s): %w", h.SecurityCode, err)
		}
	}

	// Update or insert daily snapshot
	_, err = tx.Exec(`
		INSERT INTO daily_snapshots (date, account_id, total_value, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(date, account_id) DO UPDATE SET total_value = excluded.total_value, created_at = excluded.created_at
	`, captureDate, accountID, totalValue, now)
	if err != nil {
		return fmt.Errorf("save daily snapshot: %w", err)
	}

	// Update account last sync
	_, err = tx.Exec(`
		UPDATE accounts
		SET last_synced_at = ?, last_sync_error = '', updated_at = ?
		WHERE id = ?
	`, now, now, accountID)
	if err != nil {
		return fmt.Errorf("update account last_synced: %w", err)
	}

	return tx.Commit()
}

// GetHoldingsByAccountID returns latest holdings for a specific account.
func (s *Store) GetHoldingsByAccountID(accountID int64) ([]model.Holding, error) {
	rows, err := s.db.Query(`
		SELECT h.id, h.account_id, a.name, h.asset_type, h.security_code, h.security_name,
		       h.participant, h.currency, h.quantity, h.price, h.total_value, h.updated_at
		FROM latest_holdings h
		JOIN accounts a ON h.account_id = a.id
		WHERE h.account_id = ?
		ORDER BY h.total_value DESC
	`, accountID)
	if err != nil {
		return nil, fmt.Errorf("query holdings by account: %w", err)
	}
	defer rows.Close()

	var holdings []model.Holding
	for rows.Next() {
		var h model.Holding
		if err := rows.Scan(&h.ID, &h.AccountID, &h.AccountName, &h.AssetType, &h.SecurityCode, &h.SecurityName, &h.Participant, &h.Currency, &h.Quantity, &h.Price, &h.TotalValue, &h.UpdatedAt); err != nil {
			return nil, err
		}
		holdings = append(holdings, h)
	}
	return holdings, nil
}

// GetPortfolioOverview calculates totals and aggregates current holdings and historical snapshots.
func (s *Store) GetPortfolioOverview() (*model.PortfolioOverview, error) {
	overview := &model.PortfolioOverview{
		Accounts:   make([]model.AccountSummary, 0),
		Holdings:   make([]model.Holding, 0),
		Historical: make([]model.DailyAggregate, 0),
	}

	// Per-account totals
	rows, err := s.db.Query(`
		SELECT a.id, a.name, a.username, a.last_synced_at, COALESCE(SUM(h.total_value), 0) as total_val
		FROM accounts a
		LEFT JOIN latest_holdings h ON a.id = h.account_id
		WHERE a.is_active = 1
		GROUP BY a.id
		ORDER BY a.name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query account summaries: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var sm model.AccountSummary
		var lastSync sql.NullTime
		if err := rows.Scan(&sm.AccountID, &sm.AccountName, &sm.Username, &lastSync, &sm.TotalValue); err != nil {
			return nil, err
		}
		if lastSync.Valid {
			sm.LastSyncedAt = &lastSync.Time
		}
		overview.Accounts = append(overview.Accounts, sm)
		overview.TotalValue += sm.TotalValue
	}

	// Asset breakdown totals
	assetRows, err := s.db.Query(`
		SELECT asset_type, COALESCE(SUM(total_value), 0)
		FROM latest_holdings
		GROUP BY asset_type
	`)
	if err != nil {
		return nil, fmt.Errorf("query asset breakdown: %w", err)
	}
	defer assetRows.Close()

	for assetRows.Next() {
		var assetType string
		var val float64
		if err := assetRows.Scan(&assetType, &val); err != nil {
			return nil, err
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

	// Holdings list with account name
	holdRows, err := s.db.Query(`
		SELECT h.id, h.account_id, a.name, h.asset_type, h.security_code, h.security_name,
		       h.participant, h.currency, h.quantity, h.price, h.total_value, h.updated_at
		FROM latest_holdings h
		JOIN accounts a ON h.account_id = a.id
		ORDER BY h.total_value DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query holdings: %w", err)
	}
	defer holdRows.Close()

	for holdRows.Next() {
		var h model.Holding
		if err := holdRows.Scan(&h.ID, &h.AccountID, &h.AccountName, &h.AssetType, &h.SecurityCode, &h.SecurityName, &h.Participant, &h.Currency, &h.Quantity, &h.Price, &h.TotalValue, &h.UpdatedAt); err != nil {
			return nil, err
		}
		overview.Holdings = append(overview.Holdings, h)
	}

	// Historical aggregated timeseries (sum of account snapshots per date)
	histRows, err := s.db.Query(`
		SELECT date, SUM(total_value) as total_val
		FROM daily_snapshots
		GROUP BY date
		ORDER BY date ASC
		LIMIT 90
	`)
	if err != nil {
		return nil, fmt.Errorf("query history: %w", err)
	}
	defer histRows.Close()

	for histRows.Next() {
		var d model.DailyAggregate
		if err := histRows.Scan(&d.Date, &d.TotalValue); err != nil {
			return nil, err
		}
		overview.Historical = append(overview.Historical, d)
	}

	return overview, nil
}
