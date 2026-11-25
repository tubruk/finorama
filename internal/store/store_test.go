package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chickenzord/finorama/internal/crypto"
	"github.com/chickenzord/finorama/internal/model"
	"github.com/chickenzord/finorama/internal/store"
)

func newTestStore(t *testing.T) (*store.Store, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "finorama-test-*")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	dbPath := filepath.Join(tmpDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("store open: %v", err)
	}
	cleanup := func() {
		st.Close()
		os.RemoveAll(tmpDir)
	}
	return st, cleanup
}

func TestStoreFlow(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// 1. Not initialized yet
	init, err := st.IsInitialized(ctx)
	if err != nil || init {
		t.Fatalf("expected uninitialized store")
	}

	// 2. Initialize
	pass := "my-secret-master-passphrase"
	key, err := st.InitializeMaster(ctx, pass)
	if err != nil {
		t.Fatalf("initialize master error: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("expected 32 byte key")
	}

	// Double init should fail
	if _, err := st.InitializeMaster(ctx, pass); err == nil {
		t.Fatalf("expected error on re-initializing master")
	}

	// Verify derivation
	derivedKey, valid, err := st.VerifyAndDeriveKey(ctx, pass)
	if err != nil || !valid {
		t.Fatalf("verify failed: %v", err)
	}
	if string(derivedKey) != string(key) {
		t.Fatalf("derived key mismatch")
	}

	// 3. Add Account
	acc, err := st.AddAccount(ctx, key, "Test Acc", "test@domain.com", "kseipassword")
	if err != nil {
		t.Fatalf("add account: %v", err)
	}

	accs, err := st.ListAccounts(ctx)
	if err != nil || len(accs) != 1 {
		t.Fatalf("expected 1 account, got %d", len(accs))
	}

	// Decrypt check
	decPass, err := crypto.Decrypt(key, accs[0].EncryptedPassword)
	if err != nil || string(decPass) != "kseipassword" {
		t.Fatalf("account password decrypt failed")
	}

	// 4. Save holdings across multiple days with second precision
	t1 := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	holdings1 := []model.Holding{
		{
			AssetType:    "EQUITY",
			SecurityCode: "BBCA",
			SecurityName: "Bank Central Asia",
			Participant:  "Mandiri Sekuritas",
			Quantity:     1000,
			Price:        10000,
			TotalValue:   10000000,
		},
		{
			AssetType:    "CASH",
			SecurityCode: "RDN-BCA",
			SecurityName: "Cash RDN (BCA)",
			Participant:  "BCA",
			Quantity:     5000000,
			Price:        1,
			TotalValue:   5000000,
		},
	}

	err = st.SaveAccountHoldings(ctx, acc.ID, holdings1, t1)
	if err != nil {
		t.Fatalf("save holdings day 1 error: %v", err)
	}

	t2 := time.Date(2026, 11, 25, 14, 30, 45, 0, time.UTC)
	holdings2 := []model.Holding{
		{
			AssetType:    "EQUITY",
			SecurityCode: "BBCA",
			SecurityName: "Bank Central Asia",
			Participant:  "Mandiri Sekuritas",
			Quantity:     1000,
			Price:        11000,
			TotalValue:   11000000,
		},
		{
			AssetType:    "CASH",
			SecurityCode: "RDN-BCA",
			SecurityName: "Cash RDN (BCA)",
			Participant:  "BCA",
			Quantity:     4000000,
			Price:        1,
			TotalValue:   4000000,
		},
	}

	err = st.SaveAccountHoldings(ctx, acc.ID, holdings2, t2)
	if err != nil {
		t.Fatalf("save holdings day 2 error: %v", err)
	}

	// 5. Get portfolio overview (should aggregate from latest day 2026-11-25)
	overview, err := st.GetPortfolioOverview(ctx)
	if err != nil {
		t.Fatalf("overview error: %v", err)
	}

	if overview.TotalValue != 15000000 {
		t.Errorf("expected total value 15000000, got %f", overview.TotalValue)
	}
	if overview.TotalEquity != 11000000 {
		t.Errorf("expected equity 11000000, got %f", overview.TotalEquity)
	}
	if overview.TotalCash != 4000000 {
		t.Errorf("expected cash 4000000, got %f", overview.TotalCash)
	}
	if len(overview.Historical) != 2 {
		t.Errorf("expected 2 historical aggregate snapshots, got %d", len(overview.Historical))
	}
}

func TestStoreUninitializedChecks(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	init, err := st.IsInitialized(ctx)
	if err != nil {
		t.Fatalf("IsInitialized err: %v", err)
	}
	if init {
		t.Fatalf("expected uninitialized store")
	}

	// VerifyAndDeriveKey on uninitialized store should fail or return false
	_, valid, err := st.VerifyAndDeriveKey(ctx, "any-passphrase")
	if err == nil && valid {
		t.Fatalf("expected error or invalid status when verifying uninitialized vault")
	}
}

func TestAccountConstraintsAndCRUD(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	key, err := st.InitializeMaster(ctx, "masterpassphrase123")
	if err != nil {
		t.Fatalf("init master: %v", err)
	}

	// 1. Add first account
	acc1, err := st.AddAccount(ctx, key, "Account One", "user1@example.com", "pass1")
	if err != nil {
		t.Fatalf("add account 1: %v", err)
	}
	if acc1.ID <= 0 {
		t.Fatalf("expected positive account ID, got %d", acc1.ID)
	}

	// 2. Duplicate username should fail due to UNIQUE constraint
	_, err = st.AddAccount(ctx, key, "Account Duplicate", "user1@example.com", "pass2")
	if err == nil {
		t.Fatalf("expected duplicate username to fail, got nil error")
	}

	// 3. GetAccountByID existing
	found, err := st.GetAccountByID(ctx, acc1.ID)
	if err != nil {
		t.Fatalf("get account by id: %v", err)
	}
	if found.Username != "user1@example.com" {
		t.Errorf("expected username user1@example.com, got %s", found.Username)
	}

	// 4. GetAccountByID non-existent
	_, err = st.GetAccountByID(ctx, 999999)
	if !errors.Is(err, store.ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound, got %v", err)
	}

	// 5. UpdateAccountSyncStatus with error
	syncErr := errors.New("connection timeout during sync")
	if err := st.UpdateAccountSyncStatus(ctx, acc1.ID, syncErr); err != nil {
		t.Fatalf("update sync status err: %v", err)
	}

	updated, err := st.GetAccountByID(ctx, acc1.ID)
	if err != nil {
		t.Fatalf("get account after sync status update: %v", err)
	}
	if updated.LastSyncError != syncErr.Error() {
		t.Errorf("expected last sync error %q, got %q", syncErr.Error(), updated.LastSyncError)
	}
	if updated.LastSyncedAt == nil {
		t.Errorf("expected last synced at to be set")
	}

	// Update sync status with success (nil error)
	if err := st.UpdateAccountSyncStatus(ctx, acc1.ID, nil); err != nil {
		t.Fatalf("update sync status success: %v", err)
	}
	updated, err = st.GetAccountByID(ctx, acc1.ID)
	if err != nil {
		t.Fatalf("get account after sync success: %v", err)
	}
	if updated.LastSyncError != "" {
		t.Errorf("expected empty last sync error, got %q", updated.LastSyncError)
	}

	// 6. DeleteAccount for existing account
	if err := st.DeleteAccount(ctx, acc1.ID); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	_, err = st.GetAccountByID(ctx, acc1.ID)
	if !errors.Is(err, store.ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound after deletion, got %v", err)
	}

	// Delete non-existent account should succeed without error (idempotent DELETE)
	if err := st.DeleteAccount(ctx, 999999); err != nil {
		t.Fatalf("expected deleting non-existent account to succeed, got %v", err)
	}
}

func TestSaveAccountHoldingsEdgeCases(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	key, err := st.InitializeMaster(ctx, "masterpassphrase123")
	if err != nil {
		t.Fatalf("init master: %v", err)
	}

	acc1, err := st.AddAccount(ctx, key, "Account 1", "acc1@example.com", "pass1")
	if err != nil {
		t.Fatalf("add acc1: %v", err)
	}
	acc2, err := st.AddAccount(ctx, key, "Account 2", "acc2@example.com", "pass2")
	if err != nil {
		t.Fatalf("add acc2: %v", err)
	}

	date1 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	// 1. Empty holdings slice
	err = st.SaveAccountHoldings(ctx, acc1.ID, []model.Holding{}, date1)
	if err != nil {
		t.Fatalf("save empty holdings failed: %v", err)
	}

	// Account last sync should be updated even if holdings is empty
	acc1Updated, err := st.GetAccountByID(ctx, acc1.ID)
	if err != nil {
		t.Fatalf("get acc1: %v", err)
	}
	if acc1Updated.LastSyncedAt == nil {
		t.Errorf("expected LastSyncedAt to be updated after empty holdings save")
	}

	holdings, err := st.GetHoldingsByAccountID(ctx, acc1.ID)
	if err != nil {
		t.Fatalf("get holdings for empty acc: %v", err)
	}
	if len(holdings) != 0 {
		t.Errorf("expected 0 holdings, got %d", len(holdings))
	}

	// 2. Add holdings for acc1 and acc2 on the same day
	hAcc1 := []model.Holding{
		{
			AssetType:    "EQUITY",
			SecurityCode: "TLKM",
			SecurityName: "Telkom Indonesia",
			Participant:  "Indo Premier",
			Quantity:     100,
			Price:        3000,
			TotalValue:   300000,
		},
	}
	hAcc2 := []model.Holding{
		{
			AssetType:    "MUTUAL_FUND",
			SecurityCode: "RDPU01",
			SecurityName: "Reksa Dana Pasar Uang",
			Participant:  "Bareksa",
			Quantity:     1000,
			Price:        1500,
			TotalValue:   1500000,
		},
		{
			AssetType:    "BOND",
			SecurityCode: "ORI020",
			SecurityName: "Obligasi Ritel Indonesia",
			Participant:  "Bareksa",
			Quantity:     10,
			Price:        1000000,
			TotalValue:   10000000,
		},
	}

	if err := st.SaveAccountHoldings(ctx, acc1.ID, hAcc1, date1); err != nil {
		t.Fatalf("save acc1 holdings: %v", err)
	}
	if err := st.SaveAccountHoldings(ctx, acc2.ID, hAcc2, date1); err != nil {
		t.Fatalf("save acc2 holdings: %v", err)
	}

	// Verify isolation
	h1Ret, err := st.GetHoldingsByAccountID(ctx, acc1.ID)
	if err != nil || len(h1Ret) != 1 {
		t.Fatalf("expected 1 holding for acc1, got %d", len(h1Ret))
	}
	if h1Ret[0].SecurityCode != "TLKM" {
		t.Errorf("expected TLKM, got %s", h1Ret[0].SecurityCode)
	}

	h2Ret, err := st.GetHoldingsByAccountID(ctx, acc2.ID)
	if err != nil || len(h2Ret) != 2 {
		t.Fatalf("expected 2 holdings for acc2, got %d", len(h2Ret))
	}

	// 3. Update existing date holdings (upsert on conflict)
	updatedHAcc1 := []model.Holding{
		{
			AssetType:    "EQUITY",
			SecurityCode: "TLKM",
			SecurityName: "Telkom Indonesia",
			Participant:  "Indo Premier",
			Quantity:     200,
			Price:        3500,
			TotalValue:   700000,
		},
	}
	laterDate1 := time.Date(2026, 11, 20, 16, 0, 0, 0, time.UTC)
	if err := st.SaveAccountHoldings(ctx, acc1.ID, updatedHAcc1, laterDate1); err != nil {
		t.Fatalf("update existing date holdings: %v", err)
	}

	h1Ret, err = st.GetHoldingsByAccountID(ctx, acc1.ID)
	if err != nil || len(h1Ret) != 1 {
		t.Fatalf("expected 1 holding after upsert, got %d", len(h1Ret))
	}
	if h1Ret[0].TotalValue != 700000 || h1Ret[0].Quantity != 200 {
		t.Errorf("expected updated quantity 200, total_value 700000, got %f, %f", h1Ret[0].Quantity, h1Ret[0].TotalValue)
	}

	// 4. Cascade delete: deleting acc1 should remove its holdings
	if err := st.DeleteAccount(ctx, acc1.ID); err != nil {
		t.Fatalf("delete acc1: %v", err)
	}
	h1AfterDel, err := st.GetHoldingsByAccountID(ctx, acc1.ID)
	if err != nil {
		t.Fatalf("get holdings after acc delete: %v", err)
	}
	if len(h1AfterDel) != 0 {
		t.Errorf("expected 0 holdings after account deletion, got %d", len(h1AfterDel))
	}

	// acc2 holdings should remain intact
	h2AfterDel, err := st.GetHoldingsByAccountID(ctx, acc2.ID)
	if err != nil || len(h2AfterDel) != 2 {
		t.Errorf("expected acc2 holdings untouched, got %d", len(h2AfterDel))
	}
}

func TestPortfolioOverviewEmptyVsPopulated(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// 1. Empty database portfolio overview
	overview, err := st.GetPortfolioOverview(ctx)
	if err != nil {
		t.Fatalf("empty GetPortfolioOverview: %v", err)
	}
	if overview.TotalValue != 0 {
		t.Errorf("expected 0 total value, got %f", overview.TotalValue)
	}
	if len(overview.Accounts) != 0 {
		t.Errorf("expected 0 accounts, got %d", len(overview.Accounts))
	}
	if len(overview.Holdings) != 0 {
		t.Errorf("expected 0 holdings, got %d", len(overview.Holdings))
	}
	if len(overview.Historical) != 0 {
		t.Errorf("expected 0 historical records, got %d", len(overview.Historical))
	}

	// 2. Initialized but no accounts
	key, err := st.InitializeMaster(ctx, "masterpassphrase123")
	if err != nil {
		t.Fatalf("init master: %v", err)
	}

	overview, err = st.GetPortfolioOverview(ctx)
	if err != nil {
		t.Fatalf("GetPortfolioOverview after init: %v", err)
	}
	if overview.TotalValue != 0 || len(overview.Accounts) != 0 {
		t.Errorf("expected empty portfolio overview when no accounts exist")
	}

	// 3. Accounts exist but no holdings
	acc, err := st.AddAccount(ctx, key, "Empty Holder", "empty@domain.com", "pass")
	if err != nil {
		t.Fatalf("add account: %v", err)
	}

	overview, err = st.GetPortfolioOverview(ctx)
	if err != nil {
		t.Fatalf("GetPortfolioOverview with account without holdings: %v", err)
	}
	if len(overview.Accounts) != 1 {
		t.Fatalf("expected 1 account summary, got %d", len(overview.Accounts))
	}
	if overview.Accounts[0].TotalValue != 0 {
		t.Errorf("expected account total value 0, got %f", overview.Accounts[0].TotalValue)
	}
	if overview.TotalValue != 0 {
		t.Errorf("expected portfolio total value 0, got %f", overview.TotalValue)
	}

	// 4. Populated across asset types
	now := time.Now()
	testHoldings := []model.Holding{
		{AssetType: "EQUITY", SecurityCode: "BBRI", SecurityName: "Bank BRI", Quantity: 100, Price: 5000, TotalValue: 500000},
		{AssetType: "MUTUAL_FUND", SecurityCode: "MF1", SecurityName: "Fund One", Quantity: 10, Price: 20000, TotalValue: 200000},
		{AssetType: "BOND", SecurityCode: "FR0080", SecurityName: "Fixed Rate Bond", Quantity: 1, Price: 1000000, TotalValue: 1000000},
		{AssetType: "CASH", SecurityCode: "IDR", SecurityName: "Cash", Quantity: 150000, Price: 1, TotalValue: 150000},
		{AssetType: "COMMODITY", SecurityCode: "GOLD", SecurityName: "Gold Bullion", Quantity: 1, Price: 50000, TotalValue: 50000}, // Other
	}

	if err := st.SaveAccountHoldings(ctx, acc.ID, testHoldings, now); err != nil {
		t.Fatalf("save holdings: %v", err)
	}

	overview, err = st.GetPortfolioOverview(ctx)
	if err != nil {
		t.Fatalf("populated overview: %v", err)
	}

	expectedTotal := 500000.0 + 200000.0 + 1000000.0 + 150000.0 + 50000.0
	if overview.TotalValue != expectedTotal {
		t.Errorf("expected total value %f, got %f", expectedTotal, overview.TotalValue)
	}
	if overview.TotalEquity != 500000.0 {
		t.Errorf("expected equity 500000, got %f", overview.TotalEquity)
	}
	if overview.TotalMutualFund != 200000.0 {
		t.Errorf("expected mutual fund 200000, got %f", overview.TotalMutualFund)
	}
	if overview.TotalBond != 1000000.0 {
		t.Errorf("expected bond 1000000, got %f", overview.TotalBond)
	}
	if overview.TotalCash != 150000.0 {
		t.Errorf("expected cash 150000, got %f", overview.TotalCash)
	}
	if overview.TotalOther != 50000.0 {
		t.Errorf("expected other 50000, got %f", overview.TotalOther)
	}
	if len(overview.Holdings) != 5 {
		t.Errorf("expected 5 holdings, got %d", len(overview.Holdings))
	}
	if len(overview.Historical) != 1 {
		t.Errorf("expected 1 historical point, got %d", len(overview.Historical))
	}
}
