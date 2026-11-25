package model

import "time"

type SystemConfig struct {
	MasterSalt string `json:"master_salt"`
	MasterHash string `json:"master_hash"`
}

type Account struct {
	ID                int64     `json:"id"`
	Name              string    `json:"name"`
	Username          string    `json:"username"`
	EncryptedPassword string    `json:"-"`
	IsActive          bool      `json:"is_active"`
	LastSyncedAt      *time.Time `json:"last_synced_at"`
	LastSyncError     string    `json:"last_sync_error"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type AccountSummary struct {
	AccountID   int64     `json:"account_id"`
	AccountName string    `json:"account_name"`
	Username    string    `json:"username"`
	TotalValue  float64   `json:"total_value"`
	LastSyncedAt *time.Time `json:"last_synced_at"`
}

type Holding struct {
	ID           int64     `json:"id"`
	AccountID    int64     `json:"account_id"`
	AccountName  string    `json:"account_name,omitempty"`
	CapturedAt   time.Time `json:"captured_at"`
	CapturedDate string    `json:"captured_date"`
	AssetType    string    `json:"asset_type"` // EQUITY, MUTUAL_FUND, BOND, CASH, OTHER
	SecurityCode string    `json:"security_code"`
	SecurityName string    `json:"security_name"`
	Participant  string    `json:"participant"`
	Currency     string    `json:"currency"`
	Quantity     float64   `json:"quantity"`
	Price        float64   `json:"price"`
	TotalValue   float64   `json:"total_value"`
}

type DailySnapshot struct {
	Date       string  `json:"date"` // YYYY-MM-DD
	AccountID  int64   `json:"account_id"`
	TotalValue float64 `json:"total_value"`
}

type PortfolioOverview struct {
	TotalValue      float64          `json:"total_value"`
	TotalEquity     float64          `json:"total_equity"`
	TotalMutualFund float64          `json:"total_mutual_fund"`
	TotalBond       float64          `json:"total_bond"`
	TotalCash       float64          `json:"total_cash"`
	TotalOther      float64          `json:"total_other"`
	Accounts        []AccountSummary `json:"accounts"`
	Holdings        []Holding        `json:"holdings"`
	Historical      []DailyAggregate `json:"historical"`
}

type DailyAggregate struct {
	Date       string  `json:"date"`
	TotalValue float64 `json:"total_value"`
}
