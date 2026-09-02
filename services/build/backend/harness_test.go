package backend

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/raft"
	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/pkg/v2/queue"
)

// THE FLIPPABLE STORE SHIPS WITH THIS PACKAGE, and it is not a convenience.
// raft.MockCluster's IsLeader() is hardcoded true, its Barrier does nothing and
// its Batch always succeeds, so the ENTIRE leadership half of this service —
// which is every RPC in it — is untestable with the tree's existing double.
// Anyone assuming otherwise would have green tests over an untested failure
// path. It is pkg/v2/queue/fake_test.go's fake, which exists for exactly the
// same reason.
type fakeStore struct {
	*raft.MockCluster

	// barriers counts them because a barrier is a replicated log entry and the
	// claim loop runs continuously, so "take one in every section" is a cost
	// and not only a correctness argument. barrier_test.go spends it.
	barriers atomic.Int64

	mu          sync.Mutex
	follower    bool
	batchErr    error
	barrierErr  error
	beforeBatch func()
	afterKeys   func()
	keysPrefix  string
	phantom     []string
}

// haunt makes Keys report a key that Get will not find — a row deleted between
// a listing and the read of it, which is an ordinary interleaving on a store
// several writers share.
func (s *fakeStore) haunt(keys ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phantom = append(s.phantom, keys...)
}

func (s *fakeStore) Keys(prefix string) []string {
	keys := s.MockCluster.Keys(prefix)

	s.mu.Lock()
	for _, k := range s.phantom {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	hook := s.afterKeys
	if hook != nil && prefix == s.keysPrefix {
		s.afterKeys = nil
	} else {
		hook = nil
	}
	s.mu.Unlock()

	if hook != nil {
		hook()
	}
	return keys
}

// onKeys runs hook after the next listing of prefix. reconcile takes its one
// barrier and then lists, so this is where a test gets between that barrier and
// the per-row sections underneath it.
func (s *fakeStore) onKeys(prefix string, hook func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keysPrefix, s.afterKeys = prefix, hook
}

func newStore() *fakeStore {
	return &fakeStore{MockCluster: raft.NewMockCluster()}
}

func (s *fakeStore) IsLeader() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.follower
}

// stepDown is a leadership flap: the node is still up, still reachable, and may
// no longer write.
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

func (s *fakeStore) failBarrier(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.barrierErr = err
}

// onBatch runs hook before the next Batch commits, which is how a test
// interleaves something with a write it cannot otherwise get between.
func (s *fakeStore) onBatch(hook func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beforeBatch = hook
}

func (s *fakeStore) Barrier(time.Duration) error {
	s.barriers.Add(1)

	s.mu.Lock()
	err := s.barrierErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if !s.IsLeader() {
		return raft.ErrNotLeader
	}
	return nil
}

func (s *fakeStore) Batch(ops []raft.BatchOp, timeout time.Duration) error {
	if !s.IsLeader() {
		return raft.ErrNotLeader
	}

	s.mu.Lock()
	hook, err := s.beforeBatch, s.batchErr
	s.beforeBatch = nil
	s.mu.Unlock()

	if err != nil {
		return err
	}
	if hook != nil {
		hook()
	}
	return s.MockCluster.Batch(ops, timeout)
}

// put writes straight into the underlying state, to plant a row this package
// did not write.
func (s *fakeStore) put(t testing.TB, key string, value []byte) {
	t.Helper()
	require.NoError(t, s.MockCluster.Set(key, value, time.Second))
}

type testClock struct {
	mu sync.Mutex
	at time.Time
}

func newClock() *testClock {
	return &testClock{at: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}
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

// fakeRepositories is auth, reduced to the one question this service asks it.
type fakeRepositories struct {
	mu       sync.Mutex
	projects map[string]string
	err      error
	asked    []string
}

func newRepositories(keys ...string) *fakeRepositories {
	f := &fakeRepositories{projects: map[string]string{}}
	for _, k := range keys {
		f.projects[k] = "project-of-" + k
	}
	return f
}

func (f *fakeRepositories) Project(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, key)
	if f.err != nil {
		return "", f.err
	}
	id, ok := f.projects[key]
	if !ok {
		return "", errNotFound("no repository %q", key)
	}
	return id, nil
}

func (f *fakeRepositories) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// fakeRunner is a build. It records what it was given and does whatever the
// test set it to do.
type fakeRunner struct {
	mu   sync.Mutex
	work []Work

	run func(ctx context.Context, w Work, sink Sink) error
}

func (f *fakeRunner) Run(ctx context.Context, w Work, sink Sink) error {
	f.mu.Lock()
	f.work = append(f.work, w)
	run := f.run
	f.mu.Unlock()

	if run == nil {
		return nil
	}
	return run(ctx, w, sink)
}

func (f *fakeRunner) seen() []Work {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Work(nil), f.work...)
}

func newMocknet(t testing.TB) mocknet.Mocknet {
	t.Helper()
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	return mn
}

func newHost(t testing.TB, mn mocknet.Mocknet) host.Host {
	t.Helper()
	h, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	return h
}

// harness is a constructed (but not started) Service and everything under it.
// Every dependency is a fake: a mocknet host, a map behind a raft mock, and two
// interfaces. Nothing binds a port and nothing needs a universe.
type harness struct {
	*Service
	store  *fakeStore
	repos  *fakeRepositories
	runner *fakeRunner
	clock  *testClock
	host   host.Host
}

const (
	testRepo   = "github.com/acme/pay"
	testRef    = "refs/heads/main"
	testCommit = "0123456789abcdef0123456789abcdef01234567"
)

func newHarness(t testing.TB, opts ...Option) *harness {
	t.Helper()
	store := newStore()
	return newHarnessOver(t, store, store, opts...)
}

// newHarnessOver builds the service over `over`, which may be a wrapper around
// `store` rather than the store itself.
//
// THE QUEUE IS OPENED OVER WHATEVER New WAS GIVEN, so a wrapper installed by
// assigning Service.store afterwards is under the service and NOT under the
// queue — which is invisible until a test tries to get between a record read
// and the queue write it decided, and then simply never fires. Passing it here
// is the only way both sides are behind one hook.
func newHarnessOver(t testing.TB, over Store, store *fakeStore, opts ...Option) *harness {
	t.Helper()

	repos := newRepositories(testRepo)
	runner := &fakeRunner{}
	clock := newClock()
	h := newHost(t, newMocknet(t))

	opts = append([]Option{
		WithClock(clock.now),
		WithRunner(runner),
	}, opts...)

	s, err := New(h, over, repos, Config{}, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	return &harness{Service: s, store: store, repos: repos, runner: runner, clock: clock, host: h}
}

func (h *harness) push(t testing.TB, ref, commit string) string {
	t.Helper()
	id, err := h.enqueue(t.Context(), &buildv1.PushEvent{
		RepositoryKey: testRepo,
		Ref:           ref,
		CommitSha:     commit,
	})
	require.NoError(t, err)
	return id
}

func (h *harness) mustRecord(t testing.TB, id string) jobRecord {
	t.Helper()
	rec, found, err := h.readRecord(id)
	require.NoError(t, err)
	require.True(t, found, "job %s must exist", id)
	return rec
}

// finishNow is a terminal write the way a transition makes one: one section,
// one read, and the write that read decided. A fixture cannot write it any
// other way — finish is a method on the handle withJob hands its body, and
// there is no handle anywhere else — so the fixture does what the code does.
func (h *harness) finishNow(t testing.TB, id string, status buildv1.JobStatus, reason string, extra ...raft.BatchOp) error {
	t.Helper()
	return h.withJob(id, func(j *held) error {
		require.True(t, j.found(), "job %s must exist", id)
		return j.finish(status, reason, extra...)
	})
}

func (h *harness) status(t testing.TB, id string) buildv1.JobStatus {
	t.Helper()
	return h.mustRecord(t, id).status()
}

// mustStep runs one cycle and fails on anything but a clean one or an empty
// queue.
func (h *harness) mustStep(t testing.TB) bool {
	t.Helper()
	worked, err := h.step(t.Context())
	require.NoError(t, err)
	return worked
}

func (h *harness) deadLetter(t testing.TB) *queue.Queue {
	t.Helper()
	q, err := h.group.Open(queue.Config{Name: h.cfg.Queue.DeadLetter})
	require.NoError(t, err)
	return q
}
