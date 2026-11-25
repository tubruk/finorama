package syncer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/chickenzord/finorama/internal/crypto"
	"github.com/chickenzord/finorama/internal/model"
	"github.com/chickenzord/finorama/internal/store"
	"github.com/chickenzord/finorama/internal/vault"
	"github.com/chickenzord/goksei"
	"github.com/robfig/cron/v3"
)

const (
	// DefaultAccountSyncDelay introduces rate-limit friendly spacing between syncing different accounts
	DefaultAccountSyncDelay = 5 * time.Second
)

type Syncer struct {
	store        *store.Store
	vault        *vault.Vault
	authStores   map[int64]*MemoryAuthStore
	authMu       sync.Mutex
	cron         *cron.Cron
	accountDelay time.Duration
	mu           sync.Mutex
	isSyncing    bool
}

func New(st *store.Store, v *vault.Vault, _ string) *Syncer {
	s := &Syncer{
		store:        st,
		vault:        v,
		authStores:   make(map[int64]*MemoryAuthStore),
		cron:         cron.New(),
		accountDelay: DefaultAccountSyncDelay,
	}
	return s
}

// ClearSessionCache wipes all in-memory KSEI session tokens.
func (s *Syncer) ClearSessionCache() {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	for _, store := range s.authStores {
		_ = store.Close()
	}
	s.authStores = make(map[int64]*MemoryAuthStore)
}

func (s *Syncer) getAuthStore(accountID int64) *MemoryAuthStore {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	st, exists := s.authStores[accountID]
	if !exists {
		st = NewMemoryAuthStore()
		s.authStores[accountID] = st
	}
	return st
}

// SetAccountDelay allows customizing delay between consecutive account syncs.
func (s *Syncer) SetAccountDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accountDelay = d
}

// StartCron starts cron job capturing at 01:00 AM daily
func (s *Syncer) StartCron() {
	schedule := "0 1 * * *"
	timeout := 10 * time.Minute

	s.mu.Lock()
	delay := s.accountDelay
	s.mu.Unlock()

	slog.Info("Starting periodic portfolio sync worker",
		"schedule", schedule,
		"account_delay", delay.String(),
		"job_timeout", timeout.String(),
	)

	// Daily at 01:00 AM (just after midnight update)
	_, err := s.cron.AddFunc(schedule, func() {
		slog.Info("Starting scheduled portfolio capture")
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := s.SyncAll(ctx); err != nil {
			slog.Error("Scheduled portfolio capture error", "err", err)
		} else {
			slog.Info("Scheduled portfolio capture complete")
		}
	})
	if err != nil {
		slog.Error("Failed to schedule cron", "schedule", schedule, "err", err)
		return
	}
	s.cron.Start()
}

func (s *Syncer) Stop() {
	s.cron.Stop()
}

// IsSyncing returns whether a sync is currently in progress.
func (s *Syncer) IsSyncing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isSyncing
}

// SyncAll syncs all active accounts.
func (s *Syncer) SyncAll(ctx context.Context) error {
	s.mu.Lock()
	if s.isSyncing {
		s.mu.Unlock()
		return fmt.Errorf("sync already in progress")
	}
	s.isSyncing = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.isSyncing = false
		s.mu.Unlock()
	}()

	key, err := s.vault.Key()
	if err != nil {
		return fmt.Errorf("cannot sync: %w", err)
	}
	// Zero out initial key check bytes
	for i := range key {
		key[i] = 0
	}

	accounts, err := s.store.ListAccounts(ctx)
	if err != nil {
		return fmt.Errorf("list accounts: %w", err)
	}

	capturedAt := time.Now()
	var syncErrors []string
	syncedCount := 0

	s.mu.Lock()
	delay := s.accountDelay
	s.mu.Unlock()

	for _, acc := range accounts {
		if !acc.IsActive {
			continue
		}

		// Re-check vault key before syncing each account
		key, err := s.vault.Key()
		if err != nil {
			syncErrors = append(syncErrors, fmt.Sprintf("vault locked before syncing %s: %v", acc.Name, err))
			break
		}

		// Wait between consecutive accounts to avoid rate limiting
		if syncedCount > 0 && delay > 0 {
			slog.Info("Waiting before syncing next account", "delay", delay.String(), "account", acc.Name)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				for i := range key {
					key[i] = 0
				}
				return ctx.Err()
			}
		}

		syncedCount++
		syncErr := s.syncAccount(ctx, key, acc, capturedAt)
		// Explicitly zero out key copy bytes after syncing account
		for i := range key {
			key[i] = 0
		}

		if syncErr != nil {
			slog.Error("Failed to sync account", "name", acc.Name, "username", acc.Username, "err", syncErr)
			_ = s.store.UpdateAccountSyncStatus(ctx, acc.ID, syncErr)
			syncErrors = append(syncErrors, fmt.Sprintf("%s: %v", acc.Name, syncErr))
		}
	}

	if len(syncErrors) > 0 {
		return fmt.Errorf("sync completed with errors: %s", strings.Join(syncErrors, "; "))
	}

	return nil
}

// SyncAccountByID syncs a single account manually.
func (s *Syncer) SyncAccountByID(ctx context.Context, accountID int64) error {
	s.mu.Lock()
	if s.isSyncing {
		s.mu.Unlock()
		return fmt.Errorf("another sync is already in progress")
	}
	s.isSyncing = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.isSyncing = false
		s.mu.Unlock()
	}()

	key, err := s.vault.Key()
	if err != nil {
		return fmt.Errorf("vault locked: %w", err)
	}

	acc, err := s.store.GetAccountByID(ctx, accountID)
	if err != nil {
		return err
	}

	capturedAt := time.Now()
	err = s.syncAccount(ctx, key, *acc, capturedAt)
	for i := range key {
		key[i] = 0
	}
	if err != nil {
		_ = s.store.UpdateAccountSyncStatus(ctx, acc.ID, err)
		return err
	}

	return nil
}

func (s *Syncer) syncAccount(ctx context.Context, key []byte, acc model.Account, capturedAt time.Time) error {
	slog.Info("Syncing account", "account", acc.Name, "username", acc.Username)

	plainPassBytes, err := crypto.Decrypt(key, acc.EncryptedPassword)
	if err != nil {
		return fmt.Errorf("decrypt account password: %w", err)
	}
	password := string(plainPassBytes)

	authStore := s.getAuthStore(acc.ID)

	client := goksei.NewClient(goksei.ClientOpts{
		Username:      acc.Username,
		Password:      password,
		PlainPassword: true,
		AuthStore:     authStore,
		Timeout:       60 * time.Second,
	})

	var holdings []model.Holding
	capturedDate := capturedAt.Format("2006-01-02")
	var fetchErrors []string

	// 1. Fetch Equities
	if err := ctx.Err(); err != nil {
		return err
	}
	tEq := time.Now()
	eqBalances, err := client.GetShareBalances(goksei.EquityType)
	if err != nil {
		return fmt.Errorf("get equity balances: %w", err)
	}
	eqCount := 0
	if eqBalances != nil && eqBalances.Data != nil {
		eqCount = len(eqBalances.Data)
		for _, item := range eqBalances.Data {
			val := item.CurrentValue()
			holdings = append(holdings, model.Holding{
				AccountID:    acc.ID,
				CapturedAt:   capturedAt,
				CapturedDate: capturedDate,
				AssetType:    "EQUITY",
				SecurityCode: item.Symbol(),
				SecurityName: item.FullName,
				Participant:  item.Participant,
				Currency:     item.Currency,
				Quantity:     item.Amount,
				Price:        item.ClosingPrice,
				TotalValue:   val,
			})
		}
	}
	slog.Info("Fetched equities", "account", acc.Name, "count", eqCount, "duration", time.Since(tEq).String())

	// 2. Fetch Mutual Funds
	if err := ctx.Err(); err != nil {
		return err
	}
	tMf := time.Now()
	mfBalances, err := client.GetShareBalances(goksei.MutualFundType)
	if err != nil {
		slog.Error("Mutual fund balances error", "account", acc.Name, "username", acc.Username, "err", err)
		fetchErrors = append(fetchErrors, fmt.Sprintf("mutual funds: %v", err))
	} else {
		mfCount := 0
		if mfBalances != nil && mfBalances.Data != nil {
			mfCount = len(mfBalances.Data)
			for _, item := range mfBalances.Data {
				val := item.CurrentValue()
				holdings = append(holdings, model.Holding{
					AccountID:    acc.ID,
					CapturedAt:   capturedAt,
					CapturedDate: capturedDate,
					AssetType:    "MUTUAL_FUND",
					SecurityCode: item.Symbol(),
					SecurityName: item.FullName,
					Participant:  item.Participant,
					Currency:     item.Currency,
					Quantity:     item.Amount,
					Price:        item.ClosingPrice,
					TotalValue:   val,
				})
			}
		}
		slog.Info("Fetched mutual funds", "account", acc.Name, "count", mfCount, "duration", time.Since(tMf).String())
	}

	// 3. Fetch Bonds
	if err := ctx.Err(); err != nil {
		return err
	}
	tBond := time.Now()
	bondBalances, err := client.GetShareBalances(goksei.BondType)
	if err != nil {
		slog.Error("Bond balances error", "account", acc.Name, "username", acc.Username, "err", err)
		fetchErrors = append(fetchErrors, fmt.Sprintf("bonds: %v", err))
	} else {
		bondCount := 0
		if bondBalances != nil && bondBalances.Data != nil {
			bondCount = len(bondBalances.Data)
			for _, item := range bondBalances.Data {
				val := item.CurrentValue()
				holdings = append(holdings, model.Holding{
					AccountID:    acc.ID,
					CapturedAt:   capturedAt,
					CapturedDate: capturedDate,
					AssetType:    "BOND",
					SecurityCode: item.Symbol(),
					SecurityName: item.FullName,
					Participant:  item.Participant,
					Currency:     item.Currency,
					Quantity:     item.Amount,
					Price:        item.ClosingPrice,
					TotalValue:   val,
				})
			}
		}
		slog.Info("Fetched bonds", "account", acc.Name, "count", bondCount, "duration", time.Since(tBond).String())
	}

	// 4. Fetch Cash balances (RDN)
	if err := ctx.Err(); err != nil {
		return err
	}
	tCash := time.Now()
	cashResp, err := client.GetCashBalances()
	if err != nil {
		slog.Error("Cash balances error", "account", acc.Name, "username", acc.Username, "err", err)
		fetchErrors = append(fetchErrors, fmt.Sprintf("cash balances: %v", err))
	} else {
		cashCount := 0
		if cashResp != nil && cashResp.Data != nil {
			cashCount = len(cashResp.Data)
			for _, item := range cashResp.Data {
				val := item.CurrentBalance()
				bankName, ok := goksei.CustodianBankNameByCode(item.BankID)
				if !ok {
					bankName = item.BankID
				}
				holdings = append(holdings, model.Holding{
					AccountID:    acc.ID,
					CapturedAt:   capturedAt,
					CapturedDate: capturedDate,
					AssetType:    "CASH",
					SecurityCode: fmt.Sprintf("RDN-%s-%s", item.BankID, item.AccountNumber),
					SecurityName: fmt.Sprintf("Cash RDN (%s)", bankName),
					Participant:  bankName,
					Currency:     item.Currency,
					Quantity:     val,
					Price:        1,
					TotalValue:   val,
				})
			}
		}
		slog.Info("Fetched cash balances", "account", acc.Name, "count", cashCount, "duration", time.Since(tCash).String())
	}

	// If any balance calls failed, do not wipe/corrupt holdings with partial data
	if len(fetchErrors) > 0 {
		return fmt.Errorf("fetch balances failed: %s", strings.Join(fetchErrors, "; "))
	}

	// Persist holdings
	if err := s.store.SaveAccountHoldings(ctx, acc.ID, holdings, capturedAt); err != nil {
		return fmt.Errorf("persist holdings: %w", err)
	}

	slog.Info("Account holdings saved", "account", acc.Name, "total_records", len(holdings))
	return nil
}
