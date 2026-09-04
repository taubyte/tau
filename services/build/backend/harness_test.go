package backend

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"os"
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
	// entries is every batch applied, in order. An entry hands its rows to a
	// Batcher and to nothing else — there is no accessor on one (backend/row) —
	// so this is where a test reads what one actually carried.
	entries [][]raft.BatchOp
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

// stepUp is the other half of a flap, for a test that has to keep reading — or
// keep running the node — after the write it was measuring was refused.
func (s *fakeStore) stepUp() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.follower = false
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

	s.mu.Lock()
	s.entries = append(s.entries, ops)
	s.mu.Unlock()

	return s.MockCluster.Batch(ops, timeout)
}

// entriesNaming is every applied batch carrying a row at key, in order. An
// entry hands its rows to a Batcher and to nothing else — there is no accessor
// on one (backend/row) — so this is where a test reads what one carried.
func (s *fakeStore) entriesNaming(key string) [][]raft.BatchOp {
	s.mu.Lock()
	defer s.mu.Unlock()

	var found [][]raft.BatchOp
	for _, entry := range s.entries {
		for _, op := range entry {
			if (op.Set != nil && op.Set.Key == key) || (op.Delete != nil && op.Delete.Key == key) {
				found = append(found, entry)
				break
			}
		}
	}
	return found
}

// appliedEntries is how many batches have reached the store, which is what a
// test that says "and nothing was written" counts.
func (s *fakeStore) appliedEntries() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// keysIn spells one applied entry as the rows it is: "set <key>", "del <key>".
func keysIn(entry []raft.BatchOp) []string {
	var out []string
	for _, op := range entry {
		switch {
		case op.Set != nil && op.Delete == nil:
			out = append(out, "set "+op.Set.Key)
		case op.Delete != nil && op.Set == nil:
			out = append(out, "del "+op.Delete.Key)
		default:
			out = append(out, "neither a set nor a delete")
		}
	}
	return out
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

// add registers another repository, for a test that needs a push this node
// will accept for something other than the job it is running.
func (f *fakeRepositories) add(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects[key] = "project-of-" + key
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

	run func(ctx context.Context, w Work, out Output) error
}

func (f *fakeRunner) Run(ctx context.Context, w Work, out Output) error {
	f.mu.Lock()
	f.work = append(f.work, w)
	run := f.run
	f.mu.Unlock()

	if run == nil {
		return nil
	}
	return run(ctx, w, out)
}

// fakeAssets is the byte store a build's output goes to — v2's storage service,
// which does not exist yet (config.go's Assets says what a real one owes this
// seam).
//
// IT IS CONTENT-ADDRESSED, like the thing it stands in for, because that is
// what makes the assertions below about cids mean anything: the same bytes are
// the same id, so a test names what it expects by naming the content.
type fakeAssets struct {
	mu     sync.Mutex
	stored map[string][]byte
	puts   int
	err    error
	// emptyCID answers with no id at all, which is a store that broke its own
	// contract rather than one that failed.
	emptyCID bool
	// block holds a Put until a test releases it, which is how the publish
	// deadline is measured.
	block chan struct{}
	// onPut fires on ENTRY to Put, before the block below, so a test can tell
	// when a publish has actually reached the store rather than guessing at it
	// with a sleep.
	onPut func()
	// onStored fires once the object is KEPT and before the cid is handed back,
	// which is the one instant at which a store has the bytes and no record
	// names them. It is where a store that answers and then trips a leadership
	// flap is built.
	onStored func()
}

func newAssets() *fakeAssets { return &fakeAssets{stored: map[string][]byte{}} }

func (f *fakeAssets) Put(ctx context.Context, content io.Reader) (string, error) {
	f.mu.Lock()
	block, err := f.block, f.err
	onPut := f.onPut
	f.puts++
	f.mu.Unlock()

	if onPut != nil {
		onPut()
	}

	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if err != nil {
		return "", err
	}

	raw, readErr := io.ReadAll(content)
	if readErr != nil {
		return "", readErr
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.emptyCID {
		return "", nil
	}
	id := assetCID(raw)
	f.stored[id] = raw
	if f.onStored != nil {
		f.onStored()
	}
	return id, nil
}

// assetCID is what fakeAssets calls a piece of content, so a test can say "the
// cid of these bytes" without having stored them first.
func assetCID(content []byte) string {
	sum := sha256.Sum256(content)
	return "cid-" + base64.RawURLEncoding.EncodeToString(sum[:])[:16]
}

func (f *fakeAssets) content(t testing.TB, cid string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, ok := f.stored[cid]
	require.True(t, ok, "nothing was stored at %q", cid)
	return string(raw)
}

func (f *fakeAssets) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeAssets) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.puts
}

// objects is how many distinct pieces of content the store actually KEPT, which
// is not the same question as count(): a Put that was attempted and failed —
// because the reader it was handed failed — is one put and no object.
func (f *fakeAssets) objects() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stored)
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
	assets *fakeAssets
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
	assets := newAssets()
	clock := newClock()
	h := newHost(t, newMocknet(t))

	opts = append([]Option{
		WithClock(clock.now),
		WithRunner(runner),
		WithAssets(assets),
	}, opts...)

	// THE LOG DIRECTORY IS THIS TEST'S OWN. A run writes a real file, and the
	// default is a scratch directory shared by every process on the box — fine
	// for a node, wrong for a test that must not see another test's leftovers.
	s, err := New(h, over, repos, Config{LogDir: t.TempDir()}, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	return &harness{
		Service: s, store: store, repos: repos,
		runner: runner, assets: assets, clock: clock, host: h,
	}
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
func (h *harness) finishNow(t testing.TB, id string, status buildv1.JobStatus, reason string) error {
	t.Helper()
	return h.withJob(id, func(j *held) error {
		require.True(t, j.found(), "job %s must exist", id)
		return j.finish(status, reason)
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

// logFiles is what is left in the log directory. A run's file is scratch: it is
// removed when the log has been handed to the asset store, so a test that
// finishes a build and finds one has found a leak.
func logFiles(t testing.TB, h *harness) []string {
	t.Helper()
	entries, err := os.ReadDir(h.cfg.LogDir)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)

	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
