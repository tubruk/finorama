package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chickenzord/finorama/internal/crypto"
	"github.com/chickenzord/finorama/internal/model"
	"github.com/chickenzord/finorama/internal/store"
)

func TestStoreFlow(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "finorama-test-*")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer st.Close()

	// 1. Not initialized yet
	init, err := st.IsInitialized()
	if err != nil || init {
		t.Fatalf("expected uninitialized store")
	}

	// 2. Initialize
	pass := "my-secret-master-passphrase"
	key, err := st.InitializeMaster(pass)
	if err != nil {
		t.Fatalf("initialize master error: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("expected 32 byte key")
	}

	// Verify derivation
	derivedKey, valid, err := st.VerifyAndDeriveKey(pass)
	if err != nil || !valid {
		t.Fatalf("verify failed: %v", err)
	}
	if string(derivedKey) != string(key) {
		t.Fatalf("derived key mismatch")
	}

	// 3. Add Account
	acc, err := st.AddAccount(key, "Test Acc", "test@domain.com", "kseipassword")
	if err != nil {
		t.Fatalf("add account: %v", err)
	}

	accs, err := st.ListAccounts()
	if err != nil || len(accs) != 1 {
		t.Fatalf("expected 1 account, got %d", len(accs))
	}

	// Decrypt check
	decPass, err := crypto.Decrypt(key, accs[0].EncryptedPassword)
	if err != nil || string(decPass) != "kseipassword" {
		t.Fatalf("account password decrypt failed")
	}

	// 4. Save holdings & snapshot
	holdings := []model.Holding{
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

	err = st.SaveAccountHoldings(acc.ID, holdings, 15000000, "2026-11-25")
	if err != nil {
		t.Fatalf("save holdings error: %v", err)
	}

	// 5. Get portfolio overview
	overview, err := st.GetPortfolioOverview()
	if err != nil {
		t.Fatalf("overview error: %v", err)
	}

	if overview.TotalValue != 15000000 {
		t.Errorf("expected total value 15000000, got %f", overview.TotalValue)
	}
	if overview.TotalEquity != 10000000 {
		t.Errorf("expected equity 10000000, got %f", overview.TotalEquity)
	}
	if overview.TotalCash != 5000000 {
		t.Errorf("expected cash 5000000, got %f", overview.TotalCash)
	}
	if len(overview.Historical) != 1 || overview.Historical[0].TotalValue != 15000000 {
		t.Errorf("expected 1 historical aggregate snapshot")
	}
}
