package cache

import (
	"context"
	"strings"
	"sync"
)

// MemStore is a Store over a map: what a development node and every test in
// this repository composes a cache against.
//
// It returns WHOLE KEYS from List, the way a real kvdb does, so the prefix
// stripping in seed is exercised against something that behaves like the thing
// it will meet.
type MemStore struct {
	mu     sync.RWMutex
	values map[string][]byte
}

func NewMemStore() *MemStore { return &MemStore{values: map[string][]byte{}} }

func (m *MemStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.values[key]
	return v, ok, nil
}

func (m *MemStore) Put(_ context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
	return nil
}

func (m *MemStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, key)
	return nil
}

func (m *MemStore) List(_ context.Context, prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var keys []string
	for k := range m.values {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

// Len is how many rows the store holds, for a test asserting that an eviction
// actually removed something rather than only forgetting it.
func (m *MemStore) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.values)
}
