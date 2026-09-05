package secrets

import (
	"context"
	"strings"
	"sync"
)

// MemStore is a Store over a map: a single-node development vault, and what
// every test in this repository composes a worker against.
//
// IT IS EXPORTED AND IN THE NON-TEST BUILD ON PURPOSE. services/worker,
// node/dream and the CLI's tests all need a vault that holds something, and a
// second hand-rolled map in each of them would be three places where "a miss
// is not an empty value" could be got wrong.
//
// It is not a mode this service knows about — see the package doc. What it is
// not is durable, and a node that stores secrets in one loses them on restart.
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

// List returns WHOLE KEYS, the way a real kvdb does. Returning the suffix
// would make this store the one place the prefix is stripped, and Vault.List
// — which is what strips it — would then be untested against anything but
// itself.
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
