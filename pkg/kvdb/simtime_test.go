package kvdb

import (
	"context"
	"sync"
	"testing"
	"time"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	ipld "github.com/ipfs/go-ipld-format"
)

// The time-bound tests run on a virtual clock: one virtual now the test
// advances, read by each replica through an offset of its own, and a virtual
// uptime that is the time elapsed. Nothing time drives runs on its own
// (Options.manual); the test calls maintain, and hears, itself.
const (
	simD         = 2 * time.Second
	simS         = time.Second
	simRI        = time.Second
	simMargin    = 3*simD + 2*simS + 2*simRI // 10s
	simRetention = retentionFactor * simMargin
)

type simTime struct {
	mu    sync.Mutex
	now   time.Time
	start time.Time
}

func newSimTime() *simTime {
	t0 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	return &simTime{now: t0, start: t0}
}

func (s *simTime) advance(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = s.now.Add(d)
}

func (s *simTime) at(offset time.Duration) func() time.Time {
	return func() time.Time {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.now.Add(offset)
	}
}

func (s *simTime) uptime() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now.Sub(s.start)
}

// ms is the virtual now in milliseconds, as stamps read it.
func (s *simTime) ms() uint64 { return millis(s.at(0)()) }

func (s *simTime) options(offset time.Duration) *Options {
	o := noAutoCompact()
	o.RebroadcastInterval = simRI
	o.DAGSyncerTimeout = time.Second
	o.DeliveryBound = simD
	o.ClockSkew = simS
	o.Retention = simRetention
	o.Clock = s.at(offset)
	o.Uptime = s.uptime
	o.manual = true
	return o
}

// simReplica is one replica on the virtual clock, with what it needs to be
// reopened, copied or restored.
type simReplica struct {
	name   string
	r      *Datastore
	store  ds.Datastore
	dag    *fetchThroughDAGSvc
	offset time.Duration
	st     *simTime
	opts   *Options
}

func (s *simTime) replica(t testing.TB, name string, offset time.Duration) *simReplica {
	t.Helper()
	sr := &simReplica{name: name, store: dssync.MutexWrap(ds.NewMapDatastore()), dag: newFetchThrough(), offset: offset, st: s}
	sr.opts = s.options(offset)
	sr.open(t)
	return sr
}

func (sr *simReplica) open(t testing.TB) {
	t.Helper()
	o := replicaOptions(sr.opts, sr.name+": ")
	o.DAGSyncerTimeout = time.Second
	r, err := NewDatastore(sr.store, ds.NewKey("crdttest"), sr.dag, &nullBroadcaster{}, o)
	if err != nil {
		t.Fatal(err)
	}
	sr.r = r
	t.Cleanup(func() { sr.r.Close() })
}

// reopen closes the replica cleanly and opens it again in this process.
func (sr *simReplica) reopen(t testing.TB) {
	t.Helper()
	closeStore(t, sr.r)
	sr.open(t)
}

func (sr *simReplica) maintain(t testing.TB) {
	t.Helper()
	if err := sr.r.maintain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// reach points sr's fetches at the given replicas' blocks.
func (sr *simReplica) reach(others ...*simReplica) {
	var rs []ipld.DAGService
	for _, o := range others {
		rs = append(rs, o.dag.DAGService)
	}
	sr.dag.setRemotes(rs...)
}

// pull walks, on sr, every head of src.
func (sr *simReplica) pull(t testing.TB, src *simReplica) {
	t.Helper()
	sr.reach(src)
	syncHeadsQuiet(sr.r, src.r)
}

func (sr *simReplica) value(t testing.TB, k string) string {
	t.Helper()
	return valueOf(t, sr.r, ds.NewKey(k))
}

func (sr *simReplica) wantValue(t testing.TB, k, v string) {
	t.Helper()
	if got := sr.value(t, k); got != v {
		t.Fatalf("replica %s: %s = %s, want %s", sr.name, k, got, v)
	}
}

func (sr *simReplica) knows(replica string) bool {
	sr.r.mu.Lock()
	defer sr.r.mu.Unlock()
	return sr.r.cc.knows(replica)
}

func (sr *simReplica) horizon() uint64 {
	sr.r.mu.Lock()
	defer sr.r.mu.Unlock()
	return sr.r.horizon
}

// heartbeat is what the replicas in contact hear from each other every
// rebroadcast: the fleet's time.
func (s *simTime) heartbeat(t testing.TB, rs ...*simReplica) {
	t.Helper()
	for _, sr := range rs {
		if err := sr.r.hearFleet(context.Background(), s.ms()); err != nil {
			t.Fatal(err)
		}
	}
}

// mergeBlock merges one of src's blocks on sr, alone.
func (sr *simReplica) mergeBlock(src *simReplica, c cid.Cid) {
	sr.reach(src)
	tryMergeOne(sr.r, c)
}

func (sr *simReplica) String() string { return sr.name }
