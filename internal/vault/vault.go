package vault

import (
	"errors"
	"sync"
)

var (
	ErrLocked = errors.New("vault is locked: passphrase required")
)

// Vault holds active encryption key in-memory while app is unlocked.
type Vault struct {
	mu  sync.RWMutex
	key []byte
}

func New() *Vault {
	return &Vault{}
}

// Unlock sets encryption key in memory.
func (v *Vault) Unlock(key []byte) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.key = make([]byte, len(key))
	copy(v.key, key)
}

// Lock clears key from memory.
func (v *Vault) Lock() {
	v.mu.Lock()
	defer v.mu.Unlock()
	for i := range v.key {
		v.key[i] = 0
	}
	v.key = nil
}

// IsUnlocked returns true if vault has active key.
func (v *Vault) IsUnlocked() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.key) > 0
}

// Key returns copy of active key, or ErrLocked.
func (v *Vault) Key() ([]byte, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if len(v.key) == 0 {
		return nil, ErrLocked
	}
	cp := make([]byte, len(v.key))
	copy(cp, v.key)
	return cp, nil
}
