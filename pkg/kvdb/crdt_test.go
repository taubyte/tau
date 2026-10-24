package kvdb

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ipfs/boxo/ipld/merkledag"
	mdutils "github.com/ipfs/boxo/ipld/merkledag/test"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	dssync "github.com/ipfs/go-datastore/sync"
	dstest "github.com/ipfs/go-datastore/test"
	pebbleds "github.com/ipfs/go-ds-pebble"
	ipld "github.com/ipfs/go-ipld-format"
	log "github.com/ipfs/go-log/v2"
)

var (
	numReplicas = 15
	debug       = false
)

const (
	mapStore = iota
	pebbleStore
)

var store int = mapStore

func init() {
	dstest.ElemCount = 10
}

type testLogger struct {
	name string
	l    log.StandardLogger
}

func (tl *testLogger) Debug(args ...any) {
	args = append([]any{tl.name}, args...)
	tl.l.Debug(args...)
}

func (tl *testLogger) Debugf(format string, args ...any) {
	args = append([]any{tl.name}, args...)
	tl.l.Debugf("%s "+format, args...)
}

func (tl *testLogger) Error(args ...any) {
	args = append([]any{tl.name}, args...)
	tl.l.Error(args...)
}

func (tl *testLogger) Errorf(format string, args ...any) {
	args = append([]any{tl.name}, args...)
	tl.l.Errorf("%s "+format, args...)
}

func (tl *testLogger) Fatal(args ...any) {
	args = append([]any{tl.name}, args...)
	tl.l.Fatal(args...)
}

func (tl *testLogger) Fatalf(format string, args ...any) {
	args = append([]any{tl.name}, args...)
	tl.l.Fatalf("%s "+format, args...)
}

func (tl *testLogger) Info(args ...any) {
	args = append([]any{tl.name}, args...)
	tl.l.Info(args...)
}

func (tl *testLogger) Infof(format string, args ...any) {
	args = append([]any{tl.name}, args...)
	tl.l.Infof("%s "+format, args...)
}

func (tl *testLogger) Panic(args ...any) {
	args = append([]any{tl.name}, args...)
	tl.l.Panic(args...)
}

func (tl *testLogger) Panicf(format string, args ...any) {
	args = append([]any{tl.name}, args...)
	tl.l.Panicf("%s "+format, args...)
}

func (tl *testLogger) Warn(args ...any) {
	args = append([]any{tl.name}, args...)
	tl.l.Warn(args...)
}

func (tl *testLogger) Warnf(format string, args ...any) {
	args = append([]any{tl.name}, args...)
	tl.l.Warnf("%s "+format, args...)
}

type mockBroadcaster struct {
	ctx      context.Context
	chans    []chan []byte
	myChan   chan []byte
	dropProb *atomic.Int64 // probability of dropping a message instead of broadcasting it
	t        testing.TB
}

func newBroadcasters(t testing.TB, n int) ([]Broadcaster, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	broadcasters := make([]Broadcaster, n)
	chans := make([]chan []byte, n)
	for i := range chans {
		dropP := &atomic.Int64{}
		chans[i] = make(chan []byte, 300)
		broadcasters[i] = &mockBroadcaster{
			ctx:      ctx,
			chans:    chans,
			myChan:   chans[i],
			dropProb: dropP,
			t:        t,
		}
	}
	return broadcasters, cancel
}

func (mb *mockBroadcaster) Broadcast(ctx context.Context, data []byte) error {
	var wg sync.WaitGroup

	randg := rand.New(rand.NewSource(time.Now().UnixNano()))

	for i, ch := range mb.chans {
		n := randg.Int63n(100)
		if n < mb.dropProb.Load() {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			randg := rand.New(rand.NewSource(int64(i)))
			// randomize when we send a little bit
			if randg.Intn(100) < 30 {
				// Sleep for a very small time that will
				// effectively be pretty random
				time.Sleep(time.Nanosecond)
			}
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()

			select {
			case ch <- data:
			case <-timer.C:
				mb.t.Errorf("broadcasting to %d timed out", i)
			}
		}(i)
		wg.Wait()
	}
	return nil
}

func (mb *mockBroadcaster) Next(ctx context.Context) ([]byte, error) {
	select {
	case data := <-mb.myChan:
		return data, nil
	case <-ctx.Done():
		return nil, ErrNoMoreBroadcast
	case <-mb.ctx.Done():
		return nil, ErrNoMoreBroadcast
	}
}

func storeFolder(i int) string {
	return fmt.Sprintf("test-pebble-%d", i)
}

func makeStore(t testing.TB, i int) ds.Datastore {
	t.Helper()

	switch store {
	case mapStore:
		return dssync.MutexWrap(ds.NewMapDatastore())
	case pebbleStore:
		folder := storeFolder(i)
		err := os.MkdirAll(folder, 0o700)
		if err != nil {
			t.Fatal(err)
		}

		dstore, err := pebbleds.NewDatastore(folder)
		if err != nil {
			t.Fatal(err)
		}
		return dstore
	default:
		t.Fatal("bad store type selected for tests")
		return nil

	}
}

func makeNReplicas(t testing.TB, n int, opts *Options) ([]*Datastore, func()) {
	bcasts, cancel := newBroadcasters(t, n)
	return makeNReplicasWithBroadcasters(t, n, opts, bcasts, cancel)
}

func makeNReplicasWithBroadcasters(t testing.TB, n int, opts *Options, bcasts []Broadcaster, cancelBcasts context.CancelFunc) ([]*Datastore, func()) {
	dagsyncs := make([]*fetchThroughDAGSvc, n)
	for i := range dagsyncs {
		dagsyncs[i] = newFetchThrough()
	}
	for i, d := range dagsyncs {
		var peers []ipld.DAGService
		for j, o := range dagsyncs {
			if j != i {
				peers = append(peers, o.DAGService)
			}
		}
		d.setRemotes(peers...)
	}

	replicas := make([]*Datastore, n)
	for i := range replicas {
		o := replicaOptions(opts, fmt.Sprintf("r#%d: ", i))
		o.RebroadcastInterval = time.Second * 5
		o.NumWorkers = 5
		var err error
		replicas[i], err = NewDatastore(makeStore(t, i), ds.NewKey("crdttest"), dagsyncs[i], bcasts[i], o)
		if err != nil {
			t.Fatal(err)
		}
	}
	if debug {
		log.SetLogLevel("crdt", "debug")
	}

	closeReplicas := func() {
		cancelBcasts()
		for i, r := range replicas {
			if err := r.Close(); err != nil {
				t.Error(err)
			}
			os.RemoveAll(storeFolder(i))
		}
	}
	return replicas, closeReplicas
}

func makeReplicas(t testing.TB, opts *Options) ([]*Datastore, func()) {
	return makeNReplicas(t, numReplicas, opts)
}

func TestCRDT(t *testing.T) {
	ctx := context.Background()
	replicas, closeReplicas := makeReplicas(t, nil)
	defer closeReplicas()
	k := ds.NewKey("hi")
	err := replicas[0].Put(ctx, k, []byte("hola"))
	if err != nil {
		t.Fatal(err)
	}

	waitCovered(t, replicas, lastDot(replicas[0]))

	for _, r := range replicas {
		v, err := r.Get(ctx, k)
		if err != nil {
			t.Error(err)
		}
		if string(v) != "hola" {
			t.Error("bad content: ", string(v))
		}
	}
}

func TestCRDTReplication(t *testing.T) {
	ctx := context.Background()
	nItems := 50
	randGen := rand.New(rand.NewSource(time.Now().UnixNano()))

	replicas, closeReplicas := makeReplicas(t, nil)
	defer closeReplicas()

	// Add nItems choosing the replica randomly
	var written []dot
	for i := range nItems {
		k := ds.RandomKey()
		v := fmt.Appendf(nil, "%d", i)
		n := randGen.Intn(len(replicas))
		err := replicas[n].Put(ctx, k, v)
		if err != nil {
			t.Fatal(err)
		}
		written = append(written, lastDot(replicas[n]))
	}

	waitCovered(t, replicas, written...)

	// Query all items
	q := query.Query{
		KeysOnly: true,
	}
	results, err := replicas[0].Query(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	// nolint:errcheck
	defer results.Close()
	rest, err := results.Rest()
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != nItems {
		t.Fatalf("expected %d elements", nItems)
	}

	// make sure each item has arrived to every replica
	for _, res := range rest {
		for _, r := range replicas {
			ok, err := r.Has(ctx, ds.NewKey(res.Key))
			if err != nil {
				t.Error(err)
			}
			if !ok {
				t.Error("replica should have key")
			}
		}
	}

	// give a new value for each item
	written = written[:0]
	for _, r := range rest {
		n := randGen.Intn(len(replicas))
		err := replicas[n].Put(ctx, ds.NewKey(r.Key), []byte("hola"))
		if err != nil {
			t.Error(err)
		}
		written = append(written, lastDot(replicas[n]))
	}

	waitCovered(t, replicas, written...)

	// query everything again
	results, err = replicas[0].Query(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	// nolint:errcheck
	defer results.Close()

	total := 0
	for r := range results.Next() {
		total++
		if r.Error != nil {
			t.Error(err)
		}
		k := ds.NewKey(r.Key)
		for i, r := range replicas {
			v, err := r.Get(ctx, k)
			if err != nil {
				t.Error(err)
			}
			if string(v) != "hola" {
				t.Errorf("value should be hola for %s in replica %d", k, i)
			}
		}
	}
	if total != nItems {
		t.Fatalf("expected %d elements again", nItems)
	}

	for _, r := range replicas {
		list, _ := r.heads.List()
		t.Log(list)
	}
	// replicas[0].PrintDAG()
	// fmt.Println("==========================================================")
	// replicas[1].PrintDAG()
}

// TestCRDTPriority tests that given multiple concurrent updates from several
// replicas on the same key, the resulting values converge to the same key.
//
// It does this by launching one go routine for every replica, where it replica
// writes the value #replica-number repeteadly (nItems-times).
//
// Finally, it puts a final value for a single key in the first replica and
// checks that all replicas got it.
//
// If key priority rules are respected, the "last" update emitted for the key
// K (which could have come from any replica) should take place everywhere.
func TestCRDTPriority(t *testing.T) {
	ctx := context.Background()
	nItems := 50

	replicas, closeReplicas := makeReplicas(t, nil)
	defer closeReplicas()

	k := ds.NewKey("k")

	var wg sync.WaitGroup
	wg.Add(len(replicas))
	for i, r := range replicas {
		go func(r *Datastore, i int) {
			defer wg.Done()
			for range nItems {
				err := r.Put(ctx, k, fmt.Appendf(nil, "r#%d", i))
				if err != nil {
					t.Error(err)
				}
			}
		}(r, i)
	}
	wg.Wait()
	var written []dot
	for _, r := range replicas {
		last := lastDot(r)
		for c := uint64(1); c <= last.counter; c++ {
			written = append(written, dot{replica: last.replica, counter: c})
		}
	}
	waitCovered(t, replicas, written...)
	var v, lastv []byte
	var err error
	for i, r := range replicas {
		v, err = r.Get(ctx, k)
		if err != nil {
			t.Error(err)
		}
		t.Logf("Replica %d got value %s", i, string(v))
		if lastv != nil && string(v) != string(lastv) {
			t.Error("value was different between replicas, but should be the same")
		}
		lastv = v
	}

	err = replicas[0].Put(ctx, k, []byte("final value"))
	if err != nil {
		t.Fatal(err)
	}

	waitCovered(t, replicas, lastDot(replicas[0]))

	for i, r := range replicas {
		v, err := r.Get(ctx, k)
		if err != nil {
			t.Error(err)
		}
		if string(v) != "final value" {
			t.Errorf("replica %d has wrong final value: %s", i, string(v))
		}
	}

	// replicas[14].PrintDAG()
	// fmt.Println("=======================================================")
	// replicas[1].PrintDAG()
}

func TestCRDTCatchUp(t *testing.T) {
	ctx := context.Background()
	nItems := 50
	replicas, closeReplicas := makeReplicas(t, nil)
	defer closeReplicas()

	r := replicas[len(replicas)-1]
	br := r.broadcaster.(*mockBroadcaster)
	br.dropProb.Store(101)

	// this items will not get to anyone
	var written []dot
	for range nItems {
		k := ds.RandomKey()
		err := r.Put(ctx, k, nil)
		if err != nil {
			t.Fatal(err)
		}
		written = append(written, lastDot(r))
	}

	br.dropProb.Store(0)

	// this message will get to everyone, and its links bring the rest
	err := r.Put(ctx, ds.RandomKey(), nil)
	if err != nil {
		t.Fatal(err)
	}
	written = append(written, lastDot(r))

	waitCovered(t, replicas, written...)
	q := query.Query{KeysOnly: true}
	results, err := replicas[0].Query(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	// nolint:errcheck
	defer results.Close()
	rest, err := results.Rest()
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != nItems+1 {
		t.Fatal("replica 0 did not get all the things")
	}
}

func TestCRDTPrintDAG(t *testing.T) {
	ctx := context.Background()

	nItems := 5
	replicas, closeReplicas := makeReplicas(t, nil)
	defer closeReplicas()

	// this items will not get to anyone
	for range nItems {
		k := ds.RandomKey()
		err := replicas[0].Put(ctx, k, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	err := replicas[0].PrintDAG(ctx)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCRDTBatch(t *testing.T) {
	ctx := context.Background()

	opts := DefaultOptions()
	opts.MaxBatchDeltaSize = 500 // bytes

	replicas, closeReplicas := makeReplicas(t, opts)
	defer closeReplicas()

	btch, err := replicas[0].Batch(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// This should be batched
	k := ds.RandomKey()
	err = btch.Put(ctx, k, make([]byte, 200))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := replicas[0].Get(ctx, k); err != ds.ErrNotFound {
		t.Fatal("should not have commited the crdtBatch")
	}

	k2 := ds.RandomKey()
	err = btch.Put(ctx, k2, make([]byte, 400))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := replicas[0].Get(ctx, k2); err != nil {
		t.Fatal("should have commited the crdtBatch: delta size was over threshold")
	}

	err = btch.Delete(ctx, k)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := replicas[0].Get(ctx, k); err != nil {
		t.Fatal("should not have committed the crdtBatch")
	}

	err = btch.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var written []dot
	last := lastDot(replicas[0])
	for c := uint64(1); c <= last.counter; c++ {
		written = append(written, dot{replica: last.replica, counter: c})
	}
	waitCovered(t, replicas, written...)
	for _, r := range replicas {
		if _, err := r.Get(ctx, k); err != ds.ErrNotFound {
			t.Error("k should have been deleted everywhere")
		}
		if _, err := r.Get(ctx, k2); err != nil {
			t.Error("k2 should be everywhere")
		}
	}
}

func TestCRDTNamespaceClash(t *testing.T) {
	ctx := context.Background()

	opts := DefaultOptions()
	replicas, closeReplicas := makeReplicas(t, opts)
	defer closeReplicas()

	k := ds.NewKey("path/to/something")
	err := replicas[0].Put(ctx, k, nil)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	k = ds.NewKey("path")
	ok, _ := replicas[0].Has(ctx, k)
	if ok {
		t.Error("it should not have the key")
	}

	_, err = replicas[0].Get(ctx, k)
	if err != ds.ErrNotFound {
		t.Error("should return err not found")
	}

	err = replicas[0].Put(ctx, k, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}

	v, err := replicas[0].Get(ctx, k)
	if err != nil {
		t.Fatal(err)
	}
	if string(v) != "hello" {
		t.Error("wrong value read from database")
	}

	err = replicas[0].Delete(ctx, ds.NewKey("path/to/something"))
	if err != nil {
		t.Fatal(err)
	}

	v, err = replicas[0].Get(ctx, k)
	if err != nil {
		t.Fatal(err)
	}
	if string(v) != "hello" {
		t.Error("wrong value read from database")
	}
}

func BenchmarkQueryElements(b *testing.B) {
	ctx := context.Background()
	replicas, closeReplicas := makeNReplicas(b, 1, nil)
	defer closeReplicas()

	for i := 0; i < b.N; i++ {
		k := ds.RandomKey()
		err := replicas[0].Put(ctx, k, make([]byte, 2000))
		if err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()

	q := query.Query{
		KeysOnly: false,
	}
	results, err := replicas[0].Query(ctx, q)
	if err != nil {
		b.Fatal(err)
	}
	// nolint:errcheck
	defer results.Close()

	totalSize := 0
	for r := range results.Next() {
		if r.Error != nil {
			b.Error(r.Error)
		}
		totalSize += len(r.Value)
	}
	b.Log(totalSize)
}

func TestRandomizeInterval(t *testing.T) {
	prevR := 100 * time.Second
	for range 1000 {
		r := randomizeInterval(100 * time.Second)
		if r < 70*time.Second || r > 130*time.Second {
			t.Error("r was ", r)
		}
		if prevR == r {
			t.Log("r and prevR were equal")
		}
		prevR = r
	}
}

func TestCRDTPutPutDelete(t *testing.T) {
	replicas, closeReplicas := makeNReplicas(t, 2, nil)
	defer closeReplicas()

	ctx := context.Background()

	br0 := replicas[0].broadcaster.(*mockBroadcaster)
	br0.dropProb.Store(101)

	br1 := replicas[1].broadcaster.(*mockBroadcaster)
	br1.dropProb.Store(101)

	k := ds.NewKey("k1")

	// r0 - put put delete
	err := replicas[0].Put(ctx, k, []byte("r0-1"))
	if err != nil {
		t.Fatal(err)
	}
	err = replicas[0].Put(ctx, k, []byte("r0-2"))
	if err != nil {
		t.Fatal(err)
	}
	err = replicas[0].Delete(ctx, k)
	if err != nil {
		t.Fatal(err)
	}

	// r1 - put
	err = replicas[1].Put(ctx, k, []byte("r1-1"))
	if err != nil {
		t.Fatal(err)
	}

	br0.dropProb.Store(0)
	br1.dropProb.Store(0)

	// The heads were never heard: they arrive with the next rebroadcast.
	var written []dot
	for _, r := range replicas {
		last := lastDot(r)
		for c := uint64(1); c <= last.counter; c++ {
			written = append(written, dot{replica: last.replica, counter: c})
		}
	}
	waitCovered(t, replicas, written...)

	r0Res, err := replicas[0].Get(ctx, ds.NewKey("k1"))
	if err != nil {
		if !errors.Is(err, ds.ErrNotFound) {
			t.Fatal(err)
		}
	}

	r1Res, err := replicas[1].Get(ctx, ds.NewKey("k1"))
	if err != nil {
		t.Fatal(err)
	}
	closeReplicas()

	if string(r0Res) != string(r1Res) {
		fmt.Printf("r0Res: %s\nr1Res: %s\n", string(r0Res), string(r1Res))
		t.Log("r0 dag")
		// nolint:errcheck
		replicas[0].PrintDAG(ctx)

		t.Log("r1 dag")
		// nolint:errcheck
		replicas[1].PrintDAG(ctx)

		t.Fatal("r0 and r1 should have the same value")
	}
}

type memoryBroadcaster struct {
	Broadcaster
	last chan []byte
}

func (mb *memoryBroadcaster) Next(ctx context.Context) ([]byte, error) {
	b, err := mb.Broadcaster.Next(ctx)
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case mb.last <- b:
	}
	return b, err
}

func (mb *memoryBroadcaster) Last() []byte {
	last := <-mb.last
	return last
}

func TestCRDTBatchBroadcast(t *testing.T) {
	// makes DefaultOptions
	opts := DefaultOptions()
	// sets the BroadcastBatchDelay to 1 second
	opts.BroadcastBatchDelay = 250 * time.Millisecond

	bcaster, cancel := newBroadcasters(t, 1)
	mb := &memoryBroadcaster{
		Broadcaster: bcaster[0],
		last:        make(chan []byte),
	}

	// makes 1 replica
	replicas, closeReplicas := makeNReplicasWithBroadcasters(t, 1, opts, []Broadcaster{mb}, cancel)
	defer closeReplicas()
	replica := replicas[0]
	ctx := context.Background()

	// Three sequential writes inside one batch window: each new head
	// replaces the previous one, which became its parent.
	for i := range 3 {
		if err := replica.Put(ctx, ds.RandomKey(), fmt.Appendf(nil, "value%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	msg, err := replica.decodeBroadcast(mb.Last())
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.heads) != 1 {
		t.Errorf("Expected 1 head in broadcast, got %d: %s", len(msg.heads), msg.heads)
	}
}

// nullBroadcaster is a no-op broadcaster for tests that don't need replication.
// Its Next() blocks until ctx is cancelled, preventing background goroutines
// from interfering with operations that require exclusive access (e.g. PurgeDAG).
type nullBroadcaster struct{}

func (nb *nullBroadcaster) Broadcast(_ context.Context, _ []byte) error { return nil }
func (nb *nullBroadcaster) Next(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func makeNReplicasNoBcast(t testing.TB, n int, opts *Options) ([]*Datastore, func()) {
	bcasts := make([]Broadcaster, n)
	for i := range bcasts {
		bcasts[i] = &nullBroadcaster{}
	}
	return makeNReplicasWithBroadcasters(t, n, opts, bcasts, func() {})
}

// openTestStore opens a Datastore on the given underlying ds/dagservice with
// sensible defaults for the bad-shutdown tests (null broadcaster, short
// repair interval when requested).
func openTestStore(t testing.TB, name string, mapDs ds.Datastore, dagServ ipld.DAGService, repair time.Duration) *Datastore {
	t.Helper()
	opts := DefaultOptions()
	opts.Logger = &testLogger{name: name + ": ", l: DefaultOptions().Logger}
	opts.RepairInterval = repair
	opts.RebroadcastInterval = time.Hour // disable rebroadcast noise
	opts.DeliveryBound = opts.DAGSyncerTimeout + opts.RebroadcastInterval
	d, err := NewDatastore(mapDs, ds.NewKey("crdttest"), dagServ, &nullBroadcaster{}, opts)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestBadShutdownMark_CleanCloseNoDirty verifies that on a clean
// New/Close/New cycle, the store is never marked dirty. The bad-shutdown
// key is present while a Datastore is open and removed by Close.
func TestBadShutdownMark_CleanCloseNoDirty(t *testing.T) {
	ctx := t.Context()
	mapDs := dssync.MutexWrap(ds.NewMapDatastore())
	dagServ := merkledag.NewDAGService(mdutils.Bserv())
	bsKey := ds.NewKey("crdttest").ChildString(badShutdownKey)

	d1 := openTestStore(t, "r1", mapDs, dagServ, time.Hour)
	has, err := mapDs.Has(ctx, bsKey)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("bad-shutdown key should be present after New")
	}
	if d1.IsDirty(ctx) {
		t.Fatal("store should not be dirty on a fresh open")
	}
	if err := d1.Close(); err != nil {
		t.Fatal(err)
	}
	has, err = mapDs.Has(ctx, bsKey)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("bad-shutdown key should be cleared by a clean Close")
	}

	d2 := openTestStore(t, "r2", mapDs, dagServ, time.Hour)
	if d2.IsDirty(ctx) {
		t.Fatal("store should not be dirty after a clean prior Close")
	}
	if err := d2.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestBadShutdownMark_DetectedOnStartup verifies that if the bad-shutdown
// key is present at startup (simulating a previous run that crashed before
// Close), the store is marked dirty and the key is re-armed for the next
// run.
func TestBadShutdownMark_DetectedOnStartup(t *testing.T) {
	ctx := t.Context()
	mapDs := dssync.MutexWrap(ds.NewMapDatastore())
	dagServ := merkledag.NewDAGService(mdutils.Bserv())
	bsKey := ds.NewKey("crdttest").ChildString(badShutdownKey)

	// Simulate state left behind by a crashed prior run: bs key present,
	// no clean Close ever ran.
	if err := mapDs.Put(ctx, bsKey, nil); err != nil {
		t.Fatal(err)
	}

	d := openTestStore(t, "recover", mapDs, dagServ, time.Hour)
	defer d.Close()

	if !d.IsDirty(ctx) {
		t.Fatal("store should be dirty after detecting a prior bad shutdown")
	}
	has, err := mapDs.Has(ctx, bsKey)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("bad-shutdown key should still be present (re-armed for next run)")
	}
}

// TestBadShutdownMark_PartialBranchRecovery: a replica that died after
// merging a head and before walking to its parent is marked dirty on reopen,
// and the repair walks the parent it recorded as missing.
func TestBadShutdownMark_PartialBranchRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		writerDs := dssync.MutexWrap(ds.NewMapDatastore())
		shared := newFetchThrough()
		w := openTestStore(t, "w", writerDs, shared, time.Hour)
		// Every store is closed on the way out, failing or not: a store left
		// open leaves goroutines in the bubble, and synctest turns that
		// into a panic that ends the whole test binary.
		t.Cleanup(func() { w.Close() })
		if err := w.Put(ctx, ds.NewKey("/k1"), []byte("v1")); err != nil {
			t.Fatal(err)
		}
		if err := w.Put(ctx, ds.NewKey("/k2"), []byte("v2")); err != nil {
			t.Fatal(err)
		}
		hs, _ := w.heads.List()
		if len(hs) != 1 {
			t.Fatalf("expected exactly 1 head after two sequential Puts, got %d", len(hs))
		}

		mapDs := dssync.MutexWrap(ds.NewMapDatastore())
		local := newFetchThrough()
		local.setRemotes(shared.DAGService)
		d1 := openTestStore(t, "r1", mapDs, local, time.Hour)
		t.Cleanup(func() { d1.Close() })
		mergeOne(t, d1, hs[0].Cid)
		if valueOf(t, d1, ds.NewKey("/k1")) != "<deleted>" {
			t.Fatal("the parent should not be merged yet")
		}
		if err := d1.Close(); err != nil {
			t.Fatal(err)
		}
		if err := mapDs.Put(ctx, ds.NewKey("crdttest").ChildString(badShutdownKey), nil); err != nil {
			t.Fatal(err)
		}

		d2 := openTestStore(t, "r2", mapDs, local, time.Hour)
		t.Cleanup(func() { d2.Close() })
		synctest.Wait()

		if got := valueOf(t, d2, ds.NewKey("/k1")); got != "v1" {
			t.Fatalf("repair should have merged the missing parent, /k1 = %s", got)
		}
		if d2.IsDirty(ctx) {
			t.Fatal("store should be marked clean after successful repair")
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
