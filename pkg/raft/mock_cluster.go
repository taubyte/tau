package raft

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	"github.com/libp2p/go-libp2p/core/peer"
)

// MockCluster is an in-memory Cluster implementation for unit tests (e.g. non-dreaming patrick/monkey tests).
// It is a no-op leader: Set/Delete/Batch succeed; Get/Keys read from local map.
func NewMockCluster() *MockCluster {
	return &MockCluster{data: make(map[string][]byte)}
}

type MockCluster struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (m *MockCluster) Set(key string, value []byte, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(value))
	copy(cp, value)
	m.data[key] = cp
	return nil
}

func (m *MockCluster) Get(key string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	return v, ok
}

func (m *MockCluster) Delete(key string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *MockCluster) Keys(prefix string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for k := range m.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys
}

func (m *MockCluster) Batch(ops []BatchOp, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, op := range ops {
		switch {
		case op.Set != nil:
			cp := make([]byte, len(op.Set.Value))
			copy(cp, op.Set.Value)
			m.data[op.Set.Key] = cp
		case op.Delete != nil:
			delete(m.data, op.Delete.Key)
		}
	}
	return nil
}

func (m *MockCluster) Apply([]byte, time.Duration) (FSMResponse, error) { return FSMResponse{}, nil }
func (m *MockCluster) Close() error                                     { return nil }
func (m *MockCluster) Namespace() string                                { return "test" }
func (m *MockCluster) Barrier(time.Duration) error                      { return nil }
func (m *MockCluster) IsLeader() bool                                   { return true }
func (m *MockCluster) Leader() (peer.ID, error)                         { return "", nil }
func (m *MockCluster) State() raft.RaftState                            { return raft.Leader }
func (m *MockCluster) WaitForLeader(context.Context) error              { return nil }
func (m *MockCluster) Members() ([]Member, error)                       { return nil, nil }
func (m *MockCluster) AddVoter(peer.ID, time.Duration) error            { return nil }
func (m *MockCluster) RemoveServer(peer.ID, time.Duration) error        { return nil }
func (m *MockCluster) TransferLeadership() error                        { return nil }
