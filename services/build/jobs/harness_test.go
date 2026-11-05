package jobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/build/joblog"
	"github.com/taubyte/tau/services/build/livelog"
	"github.com/taubyte/tau/services/build/run"
	"github.com/taubyte/tau/services/runner"
	"github.com/taubyte/tau/services/storage"
)

// fakeStore exists because raft.MockCluster's IsLeader is always true, its Barrier
// a no-op and its Batch always succeeds, leaving the leadership half untestable.
type fakeStore struct {
	*raft.MockCluster

	// barriers counts them: each is a replicated log entry (barrier_test.go).
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
	// Batcher and to nothing else — there is no accessor on one (jobs/row) —
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
// on one (jobs/row) — so this is where a test reads what one carried.
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

// fakeRecords is the kvdb a job's events live in, with the hooks a test needs to
// get between a listing and a read, or in front of a delta.
type fakeRecords struct {
	kvdb.Instance

	mu          sync.Mutex
	batchErr    error
	beforeBatch func()
	afterList   func()
	listPrefix  string
	phantom     []string
	// entries is every delta applied, each spelled as its rows: "set <key>",
	// "del <key>".
	entries [][]string
	// gate is a race test's hook on reads and writes (lostupdate_test.go).
	gate *readGate
}

func (r *fakeRecords) gated(g *readGate) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gate = g
}

func newRecords(t testing.TB) *fakeRecords {
	t.Helper()
	db, err := kvdbmock.New().New(nil, "build", 0)
	require.NoError(t, err)
	return &fakeRecords{Instance: db}
}

// haunt makes List report a key that Get will not find: a row deleted between a
// listing and the read of it.
func (r *fakeRecords) haunt(keys ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phantom = append(r.phantom, keys...)
}

// onList runs hook after the next listing of prefix.
func (r *fakeRecords) onList(prefix string, hook func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listPrefix, r.afterList = prefix, hook
}

func (r *fakeRecords) List(ctx context.Context, prefix string) ([]string, error) {
	keys, err := r.Instance.List(ctx, prefix)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	for _, k := range r.phantom {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	hook := r.afterList
	if hook != nil && prefix == r.listPrefix {
		r.afterList = nil
	} else {
		hook = nil
	}
	gate := r.gate
	r.mu.Unlock()

	if hook != nil {
		hook()
	}
	if gate != nil {
		gate.read(prefix)
	}
	return keys, nil
}

func (r *fakeRecords) failBatches(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batchErr = err
}

// onBatch runs hook before the next delta commits.
func (r *fakeRecords) onBatch(hook func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.beforeBatch = hook
}

func (r *fakeRecords) Batch(context.Context) (kvdb.Batch, error) {
	return &recordsBatch{r: r}, nil
}

// recordsBatch holds a delta's rows until Commit, which applies all of them or
// none.
type recordsBatch struct {
	r    *fakeRecords
	rows []recordsRow
}

type recordsRow struct {
	key    string
	value  []byte
	delete bool
}

func (b *recordsBatch) Put(key string, value []byte) error {
	b.rows = append(b.rows, recordsRow{key: key, value: value})
	return nil
}

func (b *recordsBatch) Delete(key string) error {
	b.rows = append(b.rows, recordsRow{key: key, delete: true})
	return nil
}

func (b *recordsBatch) Commit() error {
	b.r.mu.Lock()
	hook, err := b.r.beforeBatch, b.r.batchErr
	b.r.beforeBatch = nil
	b.r.mu.Unlock()

	if err != nil {
		return err
	}
	if hook != nil {
		hook()
	}

	spelled := make([]string, 0, len(b.rows))
	inner, err := b.r.Instance.Batch(context.Background())
	if err != nil {
		return err
	}
	for _, row := range b.rows {
		if row.delete {
			spelled = append(spelled, "del "+row.key)
			err = inner.Delete(row.key)
		} else {
			spelled = append(spelled, "set "+row.key)
			err = inner.Put(row.key, row.value)
		}
		if err != nil {
			return err
		}
	}
	if err := inner.Commit(); err != nil {
		return err
	}

	b.r.mu.Lock()
	b.r.entries = append(b.r.entries, spelled)
	gate := b.r.gate
	b.r.mu.Unlock()

	if gate != nil {
		gate.wrote(spelled)
	}
	return nil
}

// entriesNaming is every applied delta carrying a row at key, in order.
func (r *fakeRecords) entriesNaming(key string) [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var found [][]string
	for _, entry := range r.entries {
		for _, row := range entry {
			if strings.HasSuffix(row, " "+key) {
				found = append(found, entry)
				break
			}
		}
	}
	return found
}

// entriesAbout is every applied delta carrying one of a job's events.
func (r *fakeRecords) entriesAbout(id string) [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var found [][]string
	for _, entry := range r.entries {
		for _, row := range entry {
			if strings.HasPrefix(row, "set "+eventPrefix(id)) {
				found = append(found, entry)
				break
			}
		}
	}
	return found
}

// appliedEntries is how many deltas have reached the store.
func (r *fakeRecords) appliedEntries() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// keys is every key under prefix, in byte order.
func (r *fakeRecords) keys(t testing.TB, prefix string) []string {
	t.Helper()
	keys, err := r.Instance.List(context.Background(), prefix)
	require.NoError(t, err)
	slices.Sort(keys)
	return keys
}

// put writes straight into the store, to plant a row this package did not write.
func (r *fakeRecords) put(t testing.TB, key string, value []byte) {
	t.Helper()
	require.NoError(t, r.Instance.Put(context.Background(), key, value))
}

func (r *fakeRecords) drop(t testing.TB, key string) {
	t.Helper()
	require.NoError(t, r.Instance.Delete(context.Background(), key))
}

// garble plants an event of id that does not decode.
func (r *fakeRecords) garble(t testing.TB, id string) {
	t.Helper()
	r.put(t, eventKey(id, 1<<62, "garbled"), []byte("not an event"))
}

// erase removes every event of a job, as retention would.
func (r *fakeRecords) erase(t testing.TB, id string) {
	t.Helper()
	for _, key := range r.keys(t, eventPrefix(id)) {
		r.drop(t, key)
	}
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
	mu      sync.Mutex
	work    []run.Work
	planned []run.Work

	run func(ctx context.Context, w run.Work, out run.Output) error
	// plan is which environments a push feeds; nil is testEnvironment alone.
	plan func(ctx context.Context, w run.Work, out io.Writer) ([]string, error)
}

// testEnvironment is the one environment a push feeds unless a test plans others.
const testEnvironment = "prod"

func (f *fakeRunner) Plan(ctx context.Context, w run.Work, out io.Writer) ([]string, error) {
	f.mu.Lock()
	f.planned = append(f.planned, w)
	plan := f.plan
	f.mu.Unlock()

	if plan == nil {
		return []string{testEnvironment}, nil
	}
	return plan(ctx, w, out)
}

func (f *fakeRunner) Run(ctx context.Context, w run.Work, out run.Output) error {
	f.mu.Lock()
	f.work = append(f.work, w)
	run := f.run
	f.mu.Unlock()

	if run == nil {
		return nil
	}
	return run(ctx, w, out)
}

// fakeAssets is a content-addressed byte store, so a test names an expected cid by
// its content.
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
	// encodings is what each kept object said it was encoded as.
	encodings map[string]string
	// logs is the events of each log kept. A log's content, and so its cid, is the
	// text of its messages, so a test names an expected log by what the run said.
	logs map[string][]*buildv2.Job_Event
}

func newAssets() *fakeAssets {
	return &fakeAssets{stored: map[string][]byte{}, encodings: map[string]string{}, logs: map[string][]*buildv2.Job_Event{}}
}

func (f *fakeAssets) Put(ctx context.Context, at storage.Address, name string, o storage.Object, content io.Reader) (storage.Version, error) {
	id, err := f.put(ctx, content, o)
	if err != nil {
		return storage.Version{}, err
	}
	return storage.Version{Name: name, Record: storage.VersionRecord{RootCID: id, ContentType: o.ContentType, Metadata: o.Metadata}}, nil
}

func (f *fakeAssets) put(ctx context.Context, content io.Reader, o storage.Object) (string, error) {
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
	// A LOG IS KEPT COMPRESSED, and is its events; what a test reads back is what
	// was said.
	var logged []*buildv2.Job_Event
	if o.Metadata["encoding"] == livelog.Encoding {
		plain, err := io.ReadAll(livelog.Decompressed(io.NopCloser(bytes.NewReader(raw))))
		if err != nil {
			return "", err
		}
		events := joblog.Read(bytes.NewReader(plain), 0)
		for {
			ev, err := events.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return "", err
			}
			logged = append(logged, ev)
		}
		raw = []byte(messagesOf(logged))
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.emptyCID {
		return "", nil
	}
	id := assetCID(raw)
	f.stored[id] = raw
	f.encodings[id] = o.Metadata["encoding"]
	if logged != nil {
		f.logs[id] = logged
	}
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

// events is what a kept log recorded.
func (f *fakeAssets) events(t testing.TB, cid string) []*buildv2.Job_Event {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	evs, ok := f.logs[cid]
	require.True(t, ok, "no log was stored at %q", cid)
	return evs
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

func (f *fakeRunner) seen() []run.Work {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]run.Work(nil), f.work...)
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

// harness is a constructed (but not started) server and everything under it.
// Every dependency is a fake: a mocknet host, a map behind a raft mock, and two
// interfaces. Nothing binds a port and nothing needs a universe.
type harness struct {
	*server
	store   *fakeStore
	records *fakeRecords
	repos   *fakeRepositories
	runner  *fakeRunner
	// onRunner is the runner's runs the jobs go to, executed by runner and keeping
	// what they made in assets, at runnerID.
	onRunner *runner.Runs
	runnerID peer.ID
	assets   *fakeAssets
	logDir   string
	clock    *testClock
	host     host.Host
}

// oneRunner is the one runner a harness hands runs to.
type oneRunner struct {
	id   peer.ID
	runs run.Service
}

func (o oneRunner) Peers() []peer.ID { return []peer.ID{o.id} }

func (o oneRunner) At(id peer.ID) (run.Service, error) {
	if id != o.id {
		return nil, errors.New("no such runner")
	}
	return o.runs, nil
}

// stamped is a test clock as the runner reads one.
type stamped struct{ c *testClock }

func (s stamped) Now() time.Time { return s.c.now() }

// step is one claim cycle, returned once what it handed out has run: a run is on
// its own goroutine, and a test reads what it did.
func (h *harness) step(ctx context.Context) (bool, error) {
	worked, err := h.server.step(ctx)
	h.dispatched.Wait()
	return worked, err
}

// runNext claims the next job and runs it here on the harness's runner, its error
// the test's to read.
func (h *harness) runNext(t testing.TB) error {
	t.Helper()
	d, err := h.work.Claim()
	require.NoError(t, err)
	r, err := h.runners.At(h.runnerID)
	require.NoError(t, err)
	return h.run(t.Context(), d, h.runnerID, r)
}

const (
	testRepo   = "github.com/acme/pay"
	testRef    = "refs/heads/main"
	testCommit = "0123456789abcdef0123456789abcdef01234567"
)

// withoutRunners is a test-only option: a node that is a voter in the consistency
// domain without dispatching, which withRunners cannot express because it refuses
// nil — correctly, since in production no runners is a mistake rather than a
// deployment choice.
func withoutRunners() option {
	return func(o *options) error { o.runners = nil; return nil }
}

func newHarness(t testing.TB, opts ...option) *harness {
	return newHarnessCfg(t, nil, opts...)
}

// newHarnessKeeping is newHarness with the runner keeping what it made in keep, or,
// nil, nowhere.
func newHarnessKeeping(t testing.TB, keep runner.Assets, opts ...option) *harness {
	t.Helper()
	store := newStore()
	return newHarnessRigged(t, store, store, nil, keep, opts...)
}

// newHarnessCfg is newHarness plus a tweak to the node config. It exists
// because ClaimInterval and BuildTimeout are OPERATOR settings, not injected
// collaborators, so they are not reachable through an option.
func newHarnessCfg(t testing.TB, adjust func(*Config), opts ...option) *harness {
	t.Helper()
	store := newStore()
	return newHarnessOverCfg(t, store, store, adjust, opts...)
}

func newHarnessOverCfg(t testing.TB, over Store, store *fakeStore, adjust func(*Config), opts ...option) *harness {
	t.Helper()
	return newHarnessRigged(t, over, store, adjust, newAssets(), opts...)
}

// newHarnessOver builds the service over `over`, which may wrap `store`. The queue
// is opened over whatever New was given, so a wrapper must be passed here to sit
// under both.
func newHarnessOver(t testing.TB, over Store, store *fakeStore, opts ...option) *harness {
	return newHarnessOverCfg(t, over, store, nil, opts...)
}

// rig is what a harness's jobs run on: a fake build executed by a runner's runs
// at their own peer id, keeping what they make in keep.
type rig struct {
	build  *fakeRunner
	runs   *runner.Runs
	at     peer.ID
	logDir string
}

func newRig(t testing.TB, mn mocknet.Mocknet, clock *testClock, keep runner.Assets) rig {
	t.Helper()
	build := &fakeRunner{}
	// THE LOG DIRECTORY IS THIS TEST'S OWN. A run writes a real file, and the
	// default is a scratch directory shared by every process on the box — fine
	// for a node, wrong for a test that must not see another test's leftovers.
	logDir := t.TempDir()
	runs, err := runner.NewRuns(build, nil, keep, stamped{clock}, runner.Logs{Dir: logDir})
	require.NoError(t, err)
	t.Cleanup(func() { runs.Close() })
	return rig{build: build, runs: runs, at: newHost(t, mn).ID(), logDir: logDir}
}

func (r rig) runners() option { return withRunners(oneRunner{id: r.at, runs: r.runs}) }

func newHarnessRigged(t testing.TB, over Store, store *fakeStore, adjust func(*Config), keep runner.Assets, opts ...option) *harness {
	t.Helper()

	repos := newRepositories(testRepo)
	clock := newClock()
	mn := newMocknet(t)
	h := newHost(t, mn)
	on := newRig(t, mn, clock, keep)

	opts = append([]option{withClock(clock.now), on.runners()}, opts...)

	var cfg Config
	if adjust != nil {
		adjust(&cfg)
	}
	records := newRecords(t)
	s, err := newServer(cfg, append([]option{withHost(h), withStore(over), withRecords(records), withRepositories(repos)}, opts...)...)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	assets, _ := keep.(*fakeAssets)
	return &harness{
		server: s, store: store, records: records, repos: repos,
		runner: on.build, onRunner: on.runs, runnerID: on.at, assets: assets, logDir: on.logDir, clock: clock, host: h,
	}
}

func (h *harness) push(t testing.TB, ref, commit string) string {
	t.Helper()
	id, err := h.enqueue(t.Context(), &buildv2.PushEvent{
		RepositoryKey: testRepo,
		Ref:           ref,
		CommitSha:     commit,
	})
	require.NoError(t, err)
	return id
}

// planned is a push whose plan has run: the id of the one environment job it
// enqueued, ready to be claimed.
func (h *harness) planned(t testing.TB, ref, commit string) string {
	t.Helper()
	plan := h.push(t, ref, commit)
	require.True(t, h.mustStep(t), "the plan ran")
	children := h.mustRecord(t, plan).Children
	require.Len(t, children, 1, "a push feeding one environment is one job")
	return children[0]
}

// plannedEach is several pushes whose plans have all run, each an environment job
// queued in push order. The plans are pushed first and the clock moved past them, so
// every plan is claimed before any job it enqueues.
func (h *harness) plannedEach(t testing.TB, refs ...string) []string {
	t.Helper()
	plans := make([]string, 0, len(refs))
	for _, ref := range refs {
		plans = append(plans, h.push(t, ref, testCommit))
	}
	h.clock.advance(time.Second)
	for range plans {
		require.True(t, h.mustStep(t), "a plan ran")
	}
	out := make([]string, 0, len(plans))
	for _, plan := range plans {
		children := h.mustRecord(t, plan).Children
		require.Len(t, children, 1, "a push feeding one environment is one job")
		out = append(out, children[0])
	}
	return out
}

// terminal is the retention rows of one job: its plan has rows of its own.
func (h *harness) terminal(t testing.TB, id string) []string {
	t.Helper()
	var out []string
	for _, key := range h.records.keys(t, terminalRows()) {
		if strings.HasSuffix(key, "/"+seg(id)) {
			out = append(out, key)
		}
	}
	return out
}

// rows is every key this service keeps for its jobs.
func (h *harness) rows(t testing.TB) []string {
	t.Helper()
	return h.records.keys(t, "/"+rootSegment+"/")
}

// open is the jobs a repository has outstanding, each once however many open rows
// it has.
func (h *harness) open(t testing.TB, repositoryKey string) []string {
	t.Helper()
	var out []string
	for _, key := range h.records.keys(t, openRows(repositoryKey)) {
		_, id, err := parseOpenRow(key)
		require.NoError(t, err)
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// plant writes an open row this package did not leave: beside the job's first
// event, a row a drop of it missed; for a job with no events, a row from long ago.
func (h *harness) plant(t testing.TB, repositoryKey, id string) {
	t.Helper()
	if events := h.records.keys(t, eventPrefix(id)); len(events) > 0 {
		h.records.put(t, openRowsOf(repositoryKey, id)+strings.TrimPrefix(events[0], eventPrefix(id)), nil)
		return
	}
	h.records.put(t, openRow(repositoryKey, id, 1, "plant"), nil)
}

func (h *harness) mustRecord(t testing.TB, id string) jobRecord {
	t.Helper()
	rec, found, err := h.readRecord(id)
	require.NoError(t, err)
	require.True(t, found, "job %s must exist", id)
	return rec
}

// finishNow writes a terminal record the way a transition does: one section, one
// read, one write.
func (h *harness) finishNow(t testing.TB, id string, status buildv2.JobStatus, reason string) error {
	t.Helper()
	return h.withJob(id, func(j *held) error {
		require.True(t, j.found(), "job %s must exist", id)
		return j.finish(status, reason)
	})
}

func (h *harness) status(t testing.TB, id string) buildv2.JobStatus {
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
	entries, err := os.ReadDir(h.logDir)
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

// plantRecord replaces a job's events with ones that fold to rec, as if another
// writer had taken it there. The events are at this node's own next instants, so
// what the test does afterwards is ordered after them.
func (h *harness) plantRecord(t testing.TB, rec jobRecord) {
	t.Helper()
	h.records.erase(t, rec.ID)

	evs := []*buildv2.JobEvent{{Kind: &buildv2.JobEvent_Created{Created: &buildv2.JobCreated{
		RepositoryKey: rec.RepositoryKey, Ref: rec.Ref, CommitSha: rec.CommitSHA,
		ProjectId: rec.ProjectID, Environment: rec.Environment, Prelude: rec.Prelude,
		CreatedAt: rec.CreatedAt,
	}}}}
	claim := func(attempt uint32) *buildv2.JobEvent {
		return &buildv2.JobEvent{Kind: &buildv2.JobEvent_Claimed{Claimed: &buildv2.JobClaimed{
			Attempt: attempt, Holder: rec.Holder, ClaimedUntil: rec.ClaimedUntil, Runner: rec.Runner,
		}}}
	}
	settle := func(status buildv2.JobStatus, reason string) *buildv2.JobEvent {
		return &buildv2.JobEvent{Kind: &buildv2.JobEvent_Settled{Settled: &buildv2.JobSettled{
			Attempt: rec.Attempt, Status: status, Reason: reason,
			Assets: rec.Assets, Children: rec.Children,
		}}}
	}
	switch status := rec.status(); {
	case status == buildv2.JobStatus_JobStatusClaimed:
		require.NotZero(t, rec.Attempt, "a claimed job is at an attempt")
		require.Empty(t, rec.Assets, "a claimed job has built nothing yet")
		evs = append(evs, claim(rec.Attempt))
	case status == buildv2.JobStatus_JobStatusCancelled:
		require.Empty(t, rec.Assets, "a cancelled job built nothing that counts")
		if rec.Attempt > 0 {
			evs = append(evs, claim(rec.Attempt))
		}
		evs = append(evs, &buildv2.JobEvent{Kind: &buildv2.JobEvent_Cancelled{Cancelled: &buildv2.JobCancelled{Reason: rec.Reason}}})
	case rec.terminal():
		require.NotZero(t, rec.Attempt, "a settled job ended an attempt")
		evs = append(evs, claim(rec.Attempt), settle(status, rec.Reason))
	case rec.Attempt > 0 || len(rec.Assets) > 0:
		// AN OPEN JOB THAT HAS RUN BEFORE is one whose last attempt ended and was
		// retried.
		require.NotZero(t, rec.Attempt, "what an open job built, an attempt built")
		evs = append(evs, claim(rec.Attempt), settle(buildv2.JobStatus_JobStatusFailed, ""),
			&buildv2.JobEvent{Kind: &buildv2.JobEvent_Retried{Retried: &buildv2.JobRetried{}}})
	}
	for attempt, cid := range rec.Logs {
		n, err := strconv.ParseUint(attempt, 10, 32)
		require.NoError(t, err)
		evs = append(evs, &buildv2.JobEvent{Kind: &buildv2.JobEvent_Logged{Logged: &buildv2.JobLogged{Attempt: uint32(n), Log: cid}}})
	}
	for _, ev := range evs {
		ev.At, ev.Writer = h.journal.at(), "plant"
		raw, err := ev.MarshalVT()
		require.NoError(t, err)
		h.records.put(t, eventKey(rec.ID, ev.At, ev.Writer), raw)
	}

	planted := h.mustRecord(t, rec.ID)
	require.Equal(t, rec.status(), planted.status(), "the planted events fold to the record asked for")
	require.Equal(t, rec.Attempt, planted.Attempt)
}
