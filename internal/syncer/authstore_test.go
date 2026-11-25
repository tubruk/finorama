package syncer_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chickenzord/finorama/internal/syncer"
)

type sampleSession struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func TestMemoryAuthStore_Creation(t *testing.T) {
	s := syncer.NewMemoryAuthStore()
	if s == nil {
		t.Fatal("expected non-nil MemoryAuthStore")
	}
}

func TestMemoryAuthStore_GetAndSet(t *testing.T) {
	s := syncer.NewMemoryAuthStore()

	// Validation on empty key or nil value
	if err := s.Set("", "value"); err == nil {
		t.Error("expected error setting empty key")
	}
	if err := s.Set("key1", nil); err == nil {
		t.Error("expected error setting nil value")
	}

	var dummy string
	if _, err := s.Get("", &dummy); err == nil {
		t.Error("expected error getting empty key")
	}
	if _, err := s.Get("key1", nil); err == nil {
		t.Error("expected error getting into nil pointer")
	}

	// Non-existent key
	found, err := s.Get("nonexistent", &dummy)
	if err != nil {
		t.Fatalf("unexpected error getting non-existent key: %v", err)
	}
	if found {
		t.Error("expected found to be false for non-existent key")
	}

	// Normal Get and Set with string
	val := "session-token-123"
	if err := s.Set("session", val); err != nil {
		t.Fatalf("failed to set session: %v", err)
	}

	var got string
	found, err = s.Get("session", &got)
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	if !found {
		t.Fatal("expected session to be found")
	}
	if got != val {
		t.Errorf("got %q, want %q", got, val)
	}

	// Struct / JSON payload
	sess := sampleSession{
		Token:     "abc-token",
		ExpiresAt: time.Now().Add(1 * time.Hour).Truncate(time.Second),
	}
	if err := s.Set("token_obj", sess); err != nil {
		t.Fatalf("failed to set token struct: %v", err)
	}

	var gotSess sampleSession
	found, err = s.Get("token_obj", &gotSess)
	if err != nil {
		t.Fatalf("failed to get token struct: %v", err)
	}
	if !found {
		t.Fatal("expected token struct to be found")
	}
	if gotSess.Token != sess.Token || !gotSess.ExpiresAt.Equal(sess.ExpiresAt) {
		t.Errorf("got %+v, want %+v", gotSess, sess)
	}

	// Delete
	if err := s.Delete("token_obj"); err != nil {
		t.Fatalf("failed to delete key: %v", err)
	}
	if err := s.Delete(""); err == nil {
		t.Error("expected error deleting empty key")
	}

	found, err = s.Get("token_obj", &gotSess)
	if err != nil || found {
		t.Errorf("expected deleted key not to be found, found=%v err=%v", found, err)
	}
}

func TestMemoryAuthStore_TokenExpiryRetrieval(t *testing.T) {
	s := syncer.NewMemoryAuthStore()

	now := time.Now()
	expiredSession := sampleSession{
		Token:     "expired-token",
		ExpiresAt: now.Add(-10 * time.Minute),
	}
	validSession := sampleSession{
		Token:     "valid-token",
		ExpiresAt: now.Add(10 * time.Minute),
	}

	if err := s.Set("expired", expiredSession); err != nil {
		t.Fatalf("set expired: %v", err)
	}
	if err := s.Set("valid", validSession); err != nil {
		t.Fatalf("set valid: %v", err)
	}

	var gotExpired sampleSession
	found, err := s.Get("expired", &gotExpired)
	if err != nil || !found {
		t.Fatalf("expected expired entry in store, found=%v err=%v", found, err)
	}
	if !time.Now().After(gotExpired.ExpiresAt) {
		t.Errorf("expected token to be expired")
	}

	var gotValid sampleSession
	found, err = s.Get("valid", &gotValid)
	if err != nil || !found {
		t.Fatalf("expected valid entry in store, found=%v err=%v", found, err)
	}
	if time.Now().After(gotValid.ExpiresAt) {
		t.Errorf("expected token to be valid")
	}
}

func TestMemoryAuthStore_ConcurrentReadWrite(t *testing.T) {
	s := syncer.NewMemoryAuthStore()

	var wg sync.WaitGroup
	workers := 10
	iterations := 100

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", workerID)

			for j := 0; j < iterations; j++ {
				val := fmt.Sprintf("val-%d-%d", workerID, j)
				if err := s.Set(key, val); err != nil {
					t.Errorf("worker %d set failed: %v", workerID, err)
				}

				var readVal string
				found, err := s.Get(key, &readVal)
				if err != nil {
					t.Errorf("worker %d get failed: %v", workerID, err)
				}
				if found && readVal == "" {
					t.Errorf("worker %d empty readVal", workerID)
				}
			}
		}(i)
	}

	wg.Wait()
}

func TestMemoryAuthStore_Close(t *testing.T) {
	s := syncer.NewMemoryAuthStore()

	if err := s.Set("key1", "val1"); err != nil {
		t.Fatalf("failed to set: %v", err)
	}
	if err := s.Set("key2", "val2"); err != nil {
		t.Fatalf("failed to set: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("failed to close: %v", err)
	}

	var val string
	found, err := s.Get("key1", &val)
	if err != nil {
		t.Fatalf("unexpected error getting closed key: %v", err)
	}
	if found {
		t.Error("expected key1 to be wiped after Close()")
	}
}
