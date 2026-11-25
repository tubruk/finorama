package syncer

import (
	"encoding/json"
	"fmt"
	"sync"
)

// MemoryAuthStore implements goksei.AuthStore (gokv.Store) in memory.
// Credentials and session tokens never hit disk and are wiped when the process stops.
type MemoryAuthStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func NewMemoryAuthStore() *MemoryAuthStore {
	return &MemoryAuthStore{
		data: make(map[string][]byte),
	}
}

func (m *MemoryAuthStore) Set(k string, v any) error {
	if k == "" {
		return fmt.Errorf("key cannot be empty")
	}
	if v == nil {
		return fmt.Errorf("value cannot be nil")
	}

	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal value: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[k] = b
	return nil
}

func (m *MemoryAuthStore) Get(k string, v any) (bool, error) {
	if k == "" {
		return false, fmt.Errorf("key cannot be empty")
	}
	if v == nil {
		return false, fmt.Errorf("target pointer cannot be nil")
	}

	m.mu.RLock()
	val, exists := m.data[k]
	m.mu.RUnlock()

	if !exists {
		return false, nil
	}

	if err := json.Unmarshal(val, v); err != nil {
		return false, fmt.Errorf("unmarshal value: %w", err)
	}
	return true, nil
}

func (m *MemoryAuthStore) Delete(k string) error {
	if k == "" {
		return fmt.Errorf("key cannot be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, k)
	return nil
}

func (m *MemoryAuthStore) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = make(map[string][]byte)
	return nil
}
