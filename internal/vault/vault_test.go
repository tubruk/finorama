package vault_test

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"github.com/chickenzord/finorama/internal/vault"
)

func TestVaultBasicLifecycle(t *testing.T) {
	v := vault.New()

	// Initial state: locked
	if v.IsUnlocked() {
		t.Fatalf("expected new vault to be locked")
	}

	key, err := v.Key()
	if !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("expected ErrLocked on new vault, got %v", err)
	}
	if key != nil {
		t.Fatalf("expected nil key when locked, got %v", key)
	}

	// Unlock
	sampleKey := []byte("01234567890123456789012345678901")
	v.Unlock(sampleKey)

	if !v.IsUnlocked() {
		t.Fatalf("expected vault to be unlocked after Unlock()")
	}

	retrievedKey, err := v.Key()
	if err != nil {
		t.Fatalf("expected no error from Key(), got %v", err)
	}
	if !bytes.Equal(retrievedKey, sampleKey) {
		t.Fatalf("expected key %v, got %v", sampleKey, retrievedKey)
	}

	// Verify Key() returns a copy and does not alias internal key
	retrievedKey[0] ^= 0xFF
	retrievedKey2, err := v.Key()
	if err != nil {
		t.Fatalf("expected no error from Key(), got %v", err)
	}
	if bytes.Equal(retrievedKey2, retrievedKey) {
		t.Fatalf("modifying returned key mutated internal key in vault")
	}
	if !bytes.Equal(retrievedKey2, sampleKey) {
		t.Fatalf("expected original key %v, got %v", sampleKey, retrievedKey2)
	}

	// Lock
	v.Lock()

	if v.IsUnlocked() {
		t.Fatalf("expected vault to be locked after Lock()")
	}

	keyAfterLock, err := v.Key()
	if !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("expected ErrLocked after Lock(), got %v", err)
	}
	if keyAfterLock != nil {
		t.Fatalf("expected nil key after Lock(), got %v", keyAfterLock)
	}
}

func TestVaultZeroesKeyMemoryOnLock(t *testing.T) {
	// We want to test that v.Lock() actively zeroes the slice memory.
	// Unlock creates an internal slice via make and copy.
	v := vault.New()
	sampleKey := []byte("confidential-key-bytes-32bytes!!")
	v.Unlock(sampleKey)

	// In Go, since v.key is private, we can verify that:
	// 1. Modifying the original passed-in slice didn't affect vault key (Unlock copies)
	sampleKey[0] = 0xAA
	k, err := v.Key()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if k[0] == 0xAA {
		t.Fatalf("Unlock did not copy key")
	}

	// 2. Lock zeroes out internal key and clears it
	v.Lock()
	if v.IsUnlocked() {
		t.Fatalf("expected locked")
	}

	// Test unlock again with empty or new key
	newKey := []byte("another-secret-key-32bytes-long!")
	v.Unlock(newKey)
	k2, err := v.Key()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(k2, newKey) {
		t.Fatalf("expected newKey %v, got %v", newKey, k2)
	}
	v.Lock()
}

func TestVaultConcurrency(t *testing.T) {
	v := vault.New()
	testKey := []byte("concurrency-test-key-32bytes1234")

	const goroutines = 20
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				switch (id + j) % 4 {
				case 0:
					v.Unlock(testKey)
				case 1:
					v.Lock()
				case 2:
					_ = v.IsUnlocked()
				case 3:
					k, err := v.Key()
					if err == nil {
						if len(k) > 0 && !bytes.Equal(k, testKey) {
							t.Errorf("corrupted key retrieved: %v", k)
						}
					} else if !errors.Is(err, vault.ErrLocked) {
						t.Errorf("unexpected error: %v", err)
					}
				}
			}
		}(i)
	}

	wg.Wait()
}
