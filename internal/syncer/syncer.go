package syncer

import (
	"context"
	"fmt"
	"log"
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
	// Daily at 01:00 AM (just after midnight update)
	_, err := s.cron.AddFunc("0 1 * * *", func() {
		log.Println("[Syncer] Starting scheduled portfolio capture...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := s.SyncAll(ctx); err != nil {
			log.Printf("[Syncer] Scheduled capture error: %v", err)
		} else {
			log.Println("[Syncer] Scheduled portfolio capture complete.")
		}
	})
	if err != nil {
		log.Printf("[Syncer] Failed to schedule cron: %v", err)
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

	accounts, err := s.store.ListAccounts()
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

		// Wait between consecutive accounts to avoid rate limiting
		if syncedCount > 0 && delay > 0 {
			log.Printf("[Syncer] Waiting %v before syncing next account (%s)...", delay, acc.Name)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		syncedCount++
		if err := s.syncAccount(ctx, key, acc, capturedAt); err != nil {
			log.Printf("[Syncer] Failed to sync account %s (%s): %v", acc.Name, acc.Username, err)
			_ = s.store.UpdateAccountSyncStatus(acc.ID, err)
			syncErrors = append(syncErrors, fmt.Sprintf("%s: %v", acc.Name, err))
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

	acc, err := s.store.GetAccountByID(accountID)
	if err != nil {
		return err
	}

	capturedAt := time.Now()
	if err := s.syncAccount(ctx, key, *acc, capturedAt); err != nil {
		_ = s.store.UpdateAccountSyncStatus(acc.ID, err)
		return err
	}

	return nil
}

func (s *Syncer) syncAccount(ctx context.Context, key []byte, acc model.Account, capturedAt time.Time) error {
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

	// 1. Fetch Equities
	eqBalances, err := client.GetShareBalances(goksei.EquityType)
	if err != nil {
		return fmt.Errorf("get equity balances: %w", err)
	}
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

	// 2. Fetch Mutual Funds
	mfBalances, err := client.GetShareBalances(goksei.MutualFundType)
	if err != nil {
		log.Printf("[Syncer] Mutual fund balances error for %s: %v", acc.Username, err)
	} else {
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

	// 3. Fetch Bonds
	bondBalances, err := client.GetShareBalances(goksei.BondType)
	if err != nil {
		log.Printf("[Syncer] Bond balances error for %s: %v", acc.Username, err)
	} else {
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

	// 4. Fetch Cash balances (RDN)
	cashResp, err := client.GetCashBalances()
	if err != nil {
		log.Printf("[Syncer] Cash balances error for %s: %v", acc.Username, err)
	} else {
		for _, item := range cashResp.Data {
			val := item.CurrentBalance()
			bankName, ok := goksei.CustodianBankNameByID(item.BankID)
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

	// Persist holdings
	if err := s.store.SaveAccountHoldings(acc.ID, holdings, capturedAt); err != nil {
		return fmt.Errorf("persist holdings: %w", err)
	}

	return nil
}
