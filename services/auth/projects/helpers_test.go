package projects

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/proto/codec"
)

var (
	ctx        = context.Background()
	errFailing = errors.New("store unreachable")
)

// memStore is a store in a map, which a test merges with another the way CRDT
// replication would.
type memStore struct {
	mu sync.RWMutex
	m  map[string][]byte
}

func newMemStore() *memStore { return &memStore{m: map[string][]byte{}} }

func (s *memStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[key]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), v...), true, nil
}

func (s *memStore) List(_ context.Context, prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for k := range s.m {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}

func (s *memStore) Put(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = append([]byte(nil), value...)
	return nil
}

// Keys is every key held, sorted.
func (s *memStore) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.m))
	for k := range s.m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// failing fails every operation of a kind.
type failing struct {
	*memStore
	get, put, list bool
}

func (f *failing) Get(c context.Context, key string) ([]byte, bool, error) {
	if f.get {
		return nil, false, errFailing
	}
	return f.memStore.Get(c, key)
}

func (f *failing) Put(c context.Context, key string, v []byte) error {
	if f.put {
		return errFailing
	}
	return f.memStore.Put(c, key, v)
}

func (f *failing) List(c context.Context, prefix string) ([]string, error) {
	if f.list {
		return nil, errFailing
	}
	return f.memStore.List(c, prefix)
}

// row is r as the table stores it.
func row(t *testing.T, r codec.Record) []byte {
	t.Helper()
	b, err := r.MarshalVT()
	require.NoError(t, err)
	return b
}
