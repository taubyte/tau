package distributed

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/samyfodil/mushql/crdt"
	"github.com/taubyte/tau/pkg/raft"
)

// These tests are about the allocator itself rather than the sequence it hands
// out: who holds it, and what it does when it cannot read what it is
// allocating from. Both are shapes raft.MockCluster cannot produce on its own,
// so each wraps one — the mock stays the replicated state, and the wrapper is
// the timing or the failure the mock has no way to express.

const sharedPrefix = "projects/p/databases/shared"

// interleaved holds the first reader of the head until a second one arrives,
// which is the interleaving two allocators have to be caught in for either to
// allocate over the other. It gives up after a moment, so that an allocator
// which serializes its proposers finishes rather than deadlocking against
// itself.
type interleaved struct {
	*raft.MockCluster

	key     string
	mu      sync.Mutex
	count   int
	arrived chan struct{}
}

func holdingTheFirstReader(key string) *interleaved {
	return &interleaved{
		MockCluster: raft.NewMockCluster(),
		key:         key,
		arrived:     make(chan struct{}),
	}
}

func (c *interleaved) rendezvous(key string) {
	if key != c.key {
		return
	}

	c.mu.Lock()
	c.count++
	if c.count == 2 {
		close(c.arrived)
	}
	c.mu.Unlock()

	select {
	case <-c.arrived:
	case <-time.After(250 * time.Millisecond):
	}
}

func (c *interleaved) Get(key string) ([]byte, bool) {
	c.rendezvous(key)
	return c.MockCluster.Get(key)
}

func (c *interleaved) TryGet(key string) ([]byte, bool, error) {
	c.rendezvous(key)
	return c.MockCluster.TryGet(key)
}

// TestTwoOpenersOfOneDatabaseNeverShareAnIndex. The resident table opens
// outside its own lock and closes the loser of the race asynchronously, and a
// redeployment serves from a new connection before the old one has closed, so
// two openers for one database is that code's ordinary path. Both are on the
// same leader; being on the same leader is exactly what does not save them.
func TestTwoOpenersOfOneDatabaseNeverShareAnIndex(t *testing.T) {
	ctx := context.Background()
	cluster := holdingTheFirstReader(sharedPrefix + "/head")

	first, err := NewRaftAuthority(cluster, sharedPrefix)
	if err != nil {
		t.Fatalf("the first opener: %v", err)
	}
	second, err := NewRaftAuthority(cluster, sharedPrefix)
	if err != nil {
		t.Fatalf("the second opener: %v", err)
	}

	var wg sync.WaitGroup
	seq := make(chan uint64, 2)
	for i, authority := range []Authority{first, second} {
		wg.Add(1)
		go func(i int, authority Authority) {
			defer wg.Done()

			stamped, err := authority.Admit(ctx, []crdt.Op{pendingOp("e", int64(i))})
			if err != nil {
				t.Errorf("opener %d admitting: %v", i, err)
				return
			}
			seq <- stamped[0].Seq
		}(i, authority)
	}
	wg.Wait()
	close(seq)

	taken := map[uint64]bool{}
	for index := range seq {
		if taken[index] {
			t.Fatalf("index %d was handed out to both openers", index)
		}
		taken[index] = true
	}
	if !taken[1] || !taken[2] {
		t.Fatalf("the two openers took %v; the sequence must be dense", taken)
	}

	ops, err := first.Since(ctx, 0, 16)
	if err != nil {
		t.Fatalf("replaying the log: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("the log replays %d of 2 ops; one allocation wrote over the other", len(ops))
	}
}

// TestOneDatabaseOnTwoClustersKeepsTwoAuthorities. The other side of sharing:
// a prefix names a database, and tau's dream runs a cloud's nodes in one
// process, so one prefix there is one database PER NODE. Sharing on the prefix
// alone would hand a node another node's cluster.
func TestOneDatabaseOnTwoClustersKeepsTwoAuthorities(t *testing.T) {
	ctx := context.Background()

	one, err := NewRaftAuthority(raft.NewMockCluster(), sharedPrefix)
	if err != nil {
		t.Fatalf("the first node's authority: %v", err)
	}
	other, err := NewRaftAuthority(raft.NewMockCluster(), sharedPrefix)
	if err != nil {
		t.Fatalf("the second node's authority: %v", err)
	}

	if _, err := one.Admit(ctx, []crdt.Op{pendingOp("e1", 1)}); err != nil {
		t.Fatalf("admitting on the first node: %v", err)
	}

	head, err := other.Head(ctx)
	if err != nil {
		t.Fatalf("reading the second node's head: %v", err)
	}
	if head != 0 {
		t.Fatalf("the second node's head is %d; the two nodes are sharing one authority", head)
	}
}

// TestTwoDatabasesOnOneClusterKeepAnAuthorityEach. One cluster carries every
// database on the node, and a prefix is what separates their logs.
func TestTwoDatabasesOnOneClusterKeepAnAuthorityEach(t *testing.T) {
	ctx := context.Background()
	cluster := raft.NewMockCluster()

	ledger, err := NewRaftAuthority(cluster, "projects/p/databases/ledger")
	if err != nil {
		t.Fatalf("the ledger's authority: %v", err)
	}
	audit, err := NewRaftAuthority(cluster, "projects/p/databases/audit")
	if err != nil {
		t.Fatalf("the audit log's authority: %v", err)
	}

	if _, err := ledger.Admit(ctx, []crdt.Op{pendingOp("e1", 1)}); err != nil {
		t.Fatalf("admitting to the ledger: %v", err)
	}

	head, err := audit.Head(ctx)
	if err != nil {
		t.Fatalf("reading the audit log's head: %v", err)
	}
	if head != 0 {
		t.Fatalf("the audit log's head is %d; two databases are sharing one sequence", head)
	}
}

// unreadable is a cluster whose head answers an error rather than a value or
// an absence, which is what a store that has lost its disk or been closed
// under a live node answers.
type unreadable struct {
	*raft.MockCluster

	key string

	mu  sync.Mutex
	err error
}

func (c *unreadable) failing(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
}

func (c *unreadable) fault(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if key != c.key {
		return nil
	}
	return c.err
}

func (c *unreadable) Get(key string) ([]byte, bool) {
	if c.fault(key) != nil {
		return nil, false
	}
	return c.MockCluster.Get(key)
}

func (c *unreadable) TryGet(key string) ([]byte, bool, error) {
	if err := c.fault(key); err != nil {
		return nil, false, err
	}
	return c.MockCluster.TryGet(key)
}

// TestAnAuthorityThatCannotReadItsHeadRefusesRatherThanAllocating. A refusal
// burns nothing, which is what Admit promises. Reading a failed head as "no
// head yet" allocates from one on a database that is at five thousand, and
// that overwrites admitted ops and republishes a head below them — a hole
// every later reader reports forever.
func TestAnAuthorityThatCannotReadItsHeadRefusesRatherThanAllocating(t *testing.T) {
	ctx := context.Background()
	cluster := &unreadable{MockCluster: raft.NewMockCluster(), key: sharedPrefix + "/head"}

	authority, err := NewRaftAuthority(cluster, sharedPrefix)
	if err != nil {
		t.Fatalf("building the authority: %v", err)
	}

	admitted, err := authority.Admit(ctx, []crdt.Op{pendingOp("e1", 1), pendingOp("e2", 2), pendingOp("e3", 3)})
	if err != nil {
		t.Fatalf("admitting while the head still reads: %v", err)
	}
	if admitted[0].Seq != 1 {
		t.Fatalf("the first op took index %d", admitted[0].Seq)
	}

	disk := errors.New("pebble error during get: input/output error")
	cluster.failing(disk)

	if _, err := authority.Admit(ctx, []crdt.Op{pendingOp("e4", 4)}); !errors.Is(err, disk) {
		t.Fatalf("an authority that could not read its head answered %v; it allocated over what it could not see", err)
	}

	cluster.failing(nil)

	head, err := authority.Head(ctx)
	if err != nil {
		t.Fatalf("reading the head back: %v", err)
	}
	if head != 3 {
		t.Fatalf("the head is %d after a refused admission; a refusal must burn nothing", head)
	}

	ops, err := authority.Since(ctx, 0, 16)
	if err != nil {
		t.Fatalf("replaying the log: %v", err)
	}
	if len(ops) != 3 {
		t.Fatalf("the log replays %d of 3 ops", len(ops))
	}
	if string(ops[0].PK) != string(crdt.EncodePK("e1")) {
		t.Fatalf("index 1 now holds %q; the refused admission wrote over it", ops[0].PK)
	}
}

// blocked is a cluster that holds a proposal inside Batch until the test lets
// it go. It is the only way to catch a second proposer waiting its turn: the
// mock answers every call immediately, and a queue nobody is in measures
// nothing.
type blocked struct {
	*raft.MockCluster

	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *blocked) Batch(ops []raft.BatchOp, timeout time.Duration) error {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return c.MockCluster.Batch(ops, timeout)
}

// TestACallerWhoseDeadlineHasPassedStopsWaitingForTheProposerAhead. One
// authority per database is also one queue per database, and what is ahead of
// a caller in it is two round trips of up to proposeTimeout each — so the wait
// is longer than the budget the caller was given, and the wait is the part of
// an Admit that nothing else bounds. A guest's INSERT sits there for the whole
// of it.
func TestACallerWhoseDeadlineHasPassedStopsWaitingForTheProposerAhead(t *testing.T) {
	cluster := &blocked{
		MockCluster: raft.NewMockCluster(),
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	release := sync.OnceFunc(func() { close(cluster.release) })
	defer release()

	authority, err := NewRaftAuthority(cluster, sharedPrefix)
	if err != nil {
		t.Fatalf("building the authority: %v", err)
	}

	holding := make(chan error, 1)
	go func() {
		_, err := authority.Admit(context.Background(), []crdt.Op{pendingOp("e1", 1)})
		holding <- err
	}()
	<-cluster.entered

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	waiting := make(chan error, 1)
	go func() {
		_, err := authority.Admit(ctx, []crdt.Op{pendingOp("e2", 2)})
		waiting <- err
	}()

	select {
	case err := <-waiting:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("the caller behind the proposer answered %v; a deadline that has passed is an answer", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("a caller whose deadline passed two seconds ago is still queued behind the proposer ahead of it")
	}

	release()
	if err := <-holding; err != nil {
		t.Fatalf("the proposer that held the gate: %v", err)
	}

	head, err := authority.Head(context.Background())
	if err != nil {
		t.Fatalf("reading the head back: %v", err)
	}
	if head != 1 {
		t.Fatalf("the head is %d; a caller that gave up before it proposed must burn nothing", head)
	}
}

// heldAuthorities is how many databases the registry is holding an allocator
// for right now.
func heldAuthorities() int {
	authorities.mu.Lock()
	defer authorities.mu.Unlock()
	return len(authorities.held)
}

// TestTheRegistryForgetsADatabaseNobodyIsServing. An entry is keyed on the
// cluster, and a cluster is one per node per boot — node/ledger caches one per
// namespace and drops the lot when the groups go — so a node that restarts
// fifty times is fifty clusters under one prefix. Held strongly, every dead
// entry pinned its cluster, that cluster's state machine and its store handle
// until the process exited, and nothing anywhere could evict one.
func TestTheRegistryForgetsADatabaseNobodyIsServing(t *testing.T) {
	const reboots = 50

	before := heldAuthorities()
	for i := range reboots {
		authority, err := NewRaftAuthority(raft.NewMockCluster(), sharedPrefix)
		if err != nil {
			t.Fatalf("boot %d: %v", i, err)
		}
		if _, err := authority.Admit(context.Background(), []crdt.Op{pendingOp("e1", 1)}); err != nil {
			t.Fatalf("boot %d admitting: %v", i, err)
		}
	}

	// A cleanup runs after the collection that found the authority unreachable
	// and not during it, so this waits for the registry to shrink rather than
	// measuring it once. The one entry allowed through is the last boot, which
	// is still reachable from this frame.
	deadline := time.Now().Add(5 * time.Second)
	for {
		runtime.GC()
		if held := heldAuthorities(); held <= before+1 {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("fifty boots of one database left %d entries in a registry that held %d before them; each pins a cluster nothing will close", held-before, before)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
