package syncer

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
	store      *store.Store
	vault      *vault.Vault
	authDir    string
	cron       *cron.Cron
	accountDelay time.Duration
	mu         sync.Mutex
	isSyncing  bool
}

func New(st *store.Store, v *vault.Vault, dataDir string) *Syncer {
	authDir := filepath.Join(dataDir, "ksei_sessions")
	_ = os.MkdirAll(authDir, 0700)

	s := &Syncer{
		store:        st,
		vault:        v,
		authDir:      authDir,
		cron:         cron.New(),
		accountDelay: DefaultAccountSyncDelay,
	}
	return s
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

	captureDate := time.Now().Format("2006-01-02")
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
		if err := s.syncAccount(ctx, key, acc, captureDate); err != nil {
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

	captureDate := time.Now().Format("2006-01-02")
	if err := s.syncAccount(ctx, key, *acc, captureDate); err != nil {
		_ = s.store.UpdateAccountSyncStatus(acc.ID, err)
		return err
	}

	return nil
}

func (s *Syncer) syncAccount(ctx context.Context, key []byte, acc model.Account, captureDate string) error {
	plainPassBytes, err := crypto.Decrypt(key, acc.EncryptedPassword)
	if err != nil {
		return fmt.Errorf("decrypt account password: %w", err)
	}
	password := string(plainPassBytes)

	// User specific session store
	accSessionDir := filepath.Join(s.authDir, fmt.Sprintf("acc_%d", acc.ID))
	_ = os.MkdirAll(accSessionDir, 0700)

	authStore, err := goksei.NewFileAuthStore(accSessionDir)
	if err != nil {
		return fmt.Errorf("init auth store: %w", err)
	}

	client := goksei.NewClient(goksei.ClientOpts{
		Username:      acc.Username,
		Password:      password,
		PlainPassword: true,
		AuthStore:     authStore,
		Timeout:       60 * time.Second,
	})

	var holdings []model.Holding
	var totalPortfolioValue float64

	// 1. Fetch Equities
	eqBalances, err := client.GetShareBalances(goksei.EquityType)
	if err != nil {
		return fmt.Errorf("get equity balances: %w", err)
	}
	for _, item := range eqBalances.Data {
		val := item.CurrentValue()
		totalPortfolioValue += val
		holdings = append(holdings, model.Holding{
			AccountID:    acc.ID,
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
			totalPortfolioValue += val
			holdings = append(holdings, model.Holding{
				AccountID:    acc.ID,
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
			totalPortfolioValue += val
			holdings = append(holdings, model.Holding{
				AccountID:    acc.ID,
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
			totalPortfolioValue += val
			bankName, ok := goksei.CustodianBankNameByID(item.BankID)
			if !ok {
				bankName = item.BankID
			}
			holdings = append(holdings, model.Holding{
				AccountID:    acc.ID,
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

	// Persist holdings and snapshot
	if err := s.store.SaveAccountHoldings(acc.ID, holdings, totalPortfolioValue, captureDate); err != nil {
		return fmt.Errorf("persist holdings: %w", err)
	}

	return nil
}
