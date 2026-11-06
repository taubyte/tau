package kvdb

import (
	"context"
	"sync"
	"testing"
	"time"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
)

// The time-bound tests run on a virtual clock the test advances, read by each
// replica through an offset of its own, with uptime the time elapsed. Nothing
// time drives runs on its own: the test maintains and delivers.
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

// What a replica broadcasts waits in its outbox until the test delivers it.
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
	sr := &simReplica{name: name, store: newSortedStore(), dag: newFetchThrough(), out: &outbox{}, offset: offset, st: s}
	sr.opts = s.options(offset)
	sr.opts.ReplicaID = []byte(name)
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
	t.Cleanup(sr.release)
}

// release closes the replica if it is open and lets go of its store. A test's
// cleanups are held until its parent ends, so a seed run in parallel with a hundred
// others would otherwise keep every one of their stores alive.
func (sr *simReplica) release() {
	if sr.r != nil {
		sr.r.Close()
	}
	sr.r, sr.store, sr.dag, sr.out = nil, nil, nil, nil
}

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

func (sr *simReplica) reach(others ...*simReplica) {
	var rs []ipld.DAGService
	for _, o := range others {
		rs = append(rs, o.dag.DAGService)
	}
	sr.dag.setRemotes(rs...)
}

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

func (s *simTime) heartbeat(t testing.TB, rs ...*simReplica) {
	t.Helper()
	for _, sr := range rs {
		if err := sr.r.hearFleet(context.Background(), s.ms()); err != nil {
			t.Fatal(err)
		}
	}
}

func (sr *simReplica) beat() { sr.r.heartbeat(context.Background()) }

// The dsts fetch from src and from each other.
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

// exchange repeats rounds until nobody has anything left to say.
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

func (sr *simReplica) mergeBlock(src *simReplica, c cid.Cid) {
	sr.reach(src)
	tryMergeOne(sr.r, c)
}

func (sr *simReplica) String() string { return sr.name }
