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
	out    *outbox
	offset time.Duration
	st     *simTime
	opts   *Options
}

// outbox is a replica's side of the simulated network: what it broadcasts
// waits here until the test delivers it, so the test decides who hears what
// and when.
type outbox struct {
	mu   sync.Mutex
	msgs [][]byte
}

func (o *outbox) Broadcast(_ context.Context, data []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.msgs = append(o.msgs, append([]byte(nil), data...))
	return nil
}

func (o *outbox) Next(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ErrNoMoreBroadcast
}

func (o *outbox) take() [][]byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := o.msgs
	o.msgs = nil
	return out
}

func (s *simTime) replica(t testing.TB, name string, offset time.Duration) *simReplica {
	t.Helper()
	sr := &simReplica{name: name, store: dssync.MutexWrap(ds.NewMapDatastore()), dag: newFetchThrough(), out: &outbox{}, offset: offset, st: s}
	sr.opts = s.options(offset)
	sr.open(t)
	return sr
}

func (sr *simReplica) open(t testing.TB) {
	t.Helper()
	o := replicaOptions(sr.opts, sr.name+": ")
	o.DAGSyncerTimeout = time.Second
	r, err := NewDatastore(sr.store, ds.NewKey("crdttest"), sr.dag, sr.out, o)
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

// beat is one rebroadcast of sr: the gap check, then its heartbeat into its
// outbox.
func (sr *simReplica) beat() { sr.r.heartbeat(context.Background()) }

// deliver hands what src has broadcast so far to each of dsts, which fetch
// from src and from each other.
func (s *simTime) deliver(t testing.TB, src *simReplica, dsts ...*simReplica) {
	t.Helper()
	msgs := src.out.take()
	for _, dst := range dsts {
		dst.reach(append([]*simReplica{src}, dsts...)...)
		for _, data := range msgs {
			msg, err := dst.r.decodeBroadcast(data)
			if err != nil {
				t.Fatal(err)
			}
			dst.r.handleBroadcast(context.Background(), msg)
		}
	}
}

// exchange is a round among replicas in contact: each beats, and what each
// broadcasts, answers included, reaches all the others, until nobody has
// anything left to say.
func (s *simTime) exchange(t testing.TB, rs ...*simReplica) {
	t.Helper()
	for _, sr := range rs {
		sr.beat()
	}
	for range 8 {
		quiet := true
		for _, src := range rs {
			var others []*simReplica
			for _, o := range rs {
				if o != src {
					others = append(others, o)
				}
			}
			src.out.mu.Lock()
			pending := len(src.out.msgs) > 0
			src.out.mu.Unlock()
			if pending {
				quiet = false
				s.deliver(t, src, others...)
			}
		}
		if quiet {
			return
		}
	}
}

// mergeBlock merges one of src's blocks on sr, alone.
func (sr *simReplica) mergeBlock(src *simReplica, c cid.Cid) {
	sr.reach(src)
	tryMergeOne(sr.r, c)
}

func (sr *simReplica) String() string { return sr.name }
