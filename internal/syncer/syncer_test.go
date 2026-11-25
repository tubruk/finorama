package syncer_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chickenzord/finorama/internal/store"
	"github.com/chickenzord/finorama/internal/syncer"
	"github.com/chickenzord/finorama/internal/vault"
)

func setupTestSyncer(t *testing.T) (*syncer.Syncer, *vault.Vault, *store.Store, func()) {
	tmpDir, err := os.MkdirTemp("", "finorama-syncer-test-*")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}

	st, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}

	v := vault.New()
	s := syncer.New(st, v, tmpDir)

	cleanup := func() {
		s.Stop()
		_ = st.Close()
		_ = os.RemoveAll(tmpDir)
	}

	return s, v, st, cleanup
}

func TestNewSyncer(t *testing.T) {
	s, _, _, cleanup := setupTestSyncer(t)
	defer cleanup()

	if s == nil {
		t.Fatal("expected non-nil syncer")
	}
	if s.IsSyncing() {
		t.Error("expected IsSyncing to be false on new syncer")
	}
}

func TestSyncer_IsSyncingTransitionsAndConcurrency(t *testing.T) {
	s, _, _, cleanup := setupTestSyncer(t)
	defer cleanup()

	if s.IsSyncing() {
		t.Fatal("initially should not be syncing")
	}

	// Concurrent querying of IsSyncing
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.IsSyncing()
		}()
	}
	wg.Wait()
}

func TestSyncer_SyncAll_VaultLocked(t *testing.T) {
	s, v, _, cleanup := setupTestSyncer(t)
	defer cleanup()

	// Ensure vault is locked
	v.Lock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := s.SyncAll(ctx)
	if err == nil {
		t.Fatal("expected error when vault is locked, got nil")
	}

	if !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("expected vault.ErrLocked, got: %v", err)
	}

	// Syncer should not remain in syncing state
	if s.IsSyncing() {
		t.Error("isSyncing flag should be reset to false after error")
	}
}

func TestSyncer_SyncAccountByID_VaultLocked(t *testing.T) {
	s, v, _, cleanup := setupTestSyncer(t)
	defer cleanup()

	v.Lock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := s.SyncAccountByID(ctx, 12345)
	if err == nil {
		t.Fatal("expected error when vault is locked, got nil")
	}

	if !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("expected vault.ErrLocked, got: %v", err)
	}

	if s.IsSyncing() {
		t.Error("isSyncing flag should be reset to false after error")
	}
}

func TestSyncer_SyncAccountByID_AccountNotFound(t *testing.T) {
	s, v, st, cleanup := setupTestSyncer(t)
	defer cleanup()

	// Initialize and unlock vault
	key, err := st.InitializeMaster(context.Background(), "masterpassword123")
	if err != nil {
		t.Fatalf("init master: %v", err)
	}
	v.Unlock(key)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Sync non-existent account ID
	err = s.SyncAccountByID(ctx, 999999)
	if err == nil {
		t.Fatal("expected error when syncing non-existent account, got nil")
	}

	if s.IsSyncing() {
		t.Error("isSyncing flag should be reset to false after error")
	}
}

func TestSyncer_ClearSessionCache(t *testing.T) {
	s, _, _, cleanup := setupTestSyncer(t)
	defer cleanup()

	// ClearSessionCache shouldn't panic when empty or with existing stores
	s.ClearSessionCache()
	s.ClearSessionCache()
}

func TestSyncer_CronLifecycle(t *testing.T) {
	s, _, _, cleanup := setupTestSyncer(t)
	defer cleanup()

	s.SetAccountDelay(10 * time.Millisecond)

	// Start cron scheduler
	s.StartCron()

	// Give it a moment to run in background
	time.Sleep(50 * time.Millisecond)

	// Stop cron scheduler
	s.Stop()
}
