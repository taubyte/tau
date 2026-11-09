package queue

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/taubyte/tau/pkg/raft"
)

// fakeStore wraps raft.MockCluster, whose leader is a no-op, with a follower, a
// failing write and barrier ordering. The state still lives in the mock.
type fakeStore struct {
	*raft.MockCluster

	mu          sync.Mutex
	follower    bool
	calls       []string
	beforeBatch func()
	batchErr    error
	keysErr     error
}

func newStore() *fakeStore {
	return &fakeStore{MockCluster: raft.NewMockCluster()}
}

func (s *fakeStore) IsLeader() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.follower
}

func (s *fakeStore) stepDown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.follower = true
}

func (s *fakeStore) failBatches(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batchErr = err
}

func (s *fakeStore) onBatch(hook func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beforeBatch = hook
}

func (s *fakeStore) called(op string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, op)
}

// trace returns the operations seen since the last reset.
func (s *fakeStore) trace() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

func (s *fakeStore) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = nil
}

func (s *fakeStore) Get(key string) ([]byte, bool) {
	s.called("get")
	return s.MockCluster.Get(key)
}

func (s *fakeStore) Keys(prefix string) ([]string, error) {
	s.called("keys")
	s.mu.Lock()
	err := s.keysErr
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.MockCluster.Keys(prefix)
}

func (s *fakeStore) Barrier(timeout time.Duration) error {
	s.called("barrier")
	if !s.IsLeader() {
		return raft.ErrNotLeader
	}
	return nil
}

func (s *fakeStore) Batch(ops []raft.BatchOp, timeout time.Duration) error {
	s.called("batch")
	if !s.IsLeader() {
		return raft.ErrNotLeader
	}

	s.mu.Lock()
	hook, err := s.beforeBatch, s.batchErr
	s.mu.Unlock()

	if err != nil {
		return err
	}
	if hook != nil {
		hook()
	}
	return s.MockCluster.Batch(ops, timeout)
}

// put writes straight into the underlying state, to plant something this
// package did not write.
func (s *fakeStore) put(key string, value []byte) {
	if err := s.MockCluster.Set(key, value, time.Second); err != nil {
		panic(err)
	}
}

type testClock struct {
	mu sync.Mutex
	at time.Time
}

func newClock() *testClock {
	return &testClock{at: time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// harness is a queue, the group and store under it and the clock it reads.
type harness struct {
	*Queue
	group *Group
	store *fakeStore
	clock *testClock
}

func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	store := newStore()
	clock := newClock()
	if cfg.Name == "" {
		cfg.Name = "build"
	}
	group, err := NewGroup(store, WithClock(clock.now))
	if err != nil {
		t.Fatalf("NewGroup: %v", err)
	}
	q, err := group.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return &harness{Queue: q, group: group, store: store, clock: clock}
}

// depthReader is a queue, as a test reads its depth.
type depthReader interface {
	Depth() (ready, claimed int, err error)
}

// depthOf is q's depth. A depth it cannot read fails t and reads as -1, -1, which
// no assertion takes for a depth; t.Errorf is safe from a condition Eventually polls.
func depthOf(t testing.TB, q depthReader) (ready, claimed int) {
	t.Helper()
	ready, claimed, err := q.Depth()
	if err != nil {
		t.Errorf("Depth: %v", err)
		return -1, -1
	}
	return ready, claimed
}

// deadLetter opens the queue exhausted items move to, from the same group.
func (h *harness) deadLetter(t *testing.T) *Queue {
	t.Helper()
	q, err := h.group.Open(Config{Name: h.Config().DeadLetter})
	if err != nil {
		t.Fatalf("Open dead letter: %v", err)
	}
	return q
}

func (h *harness) mustPush(t *testing.T, id, data string) {
	t.Helper()
	if err := h.Push(id, []byte(data)); err != nil {
		t.Fatalf("Push(%q): %v", id, err)
	}
}

func (h *harness) mustClaim(t *testing.T) Delivery {
	t.Helper()
	d, err := h.Claim()
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	return d
}

func (h *harness) mustNotClaim(t *testing.T) {
	t.Helper()
	d, err := h.Claim()
	if err != ErrNoItem {
		t.Fatalf("Claim: got (%+v, %v), want ErrNoItem", d, err)
	}
}
