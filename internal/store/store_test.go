package store_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

	err = st.SaveAccountHoldings(acc.ID, holdings1, t1)
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

	err = st.SaveAccountHoldings(acc.ID, holdings2, t2)
	if err != nil {
		t.Fatalf("save holdings day 2 error: %v", err)
	}

	// 5. Get portfolio overview (should aggregate from latest day 2026-11-25)
	overview, err := st.GetPortfolioOverview()
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
