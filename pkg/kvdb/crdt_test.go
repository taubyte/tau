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

	blockstore "github.com/ipfs/boxo/blockstore"
	"github.com/ipfs/boxo/ipld/merkledag"
	mdutils "github.com/ipfs/boxo/ipld/merkledag/test"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	dssync "github.com/ipfs/go-datastore/sync"
	dstest "github.com/ipfs/go-datastore/test"
	pebbleds "github.com/ipfs/go-ds-pebble"
	ipld "github.com/ipfs/go-ipld-format"
	log "github.com/ipfs/go-log/v2"
	"github.com/multiformats/go-multihash"
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

type mockDAGSvc struct {
	ipld.DAGService
	bs blockstore.Blockstore
}

func (mds *mockDAGSvc) Add(ctx context.Context, n ipld.Node) error {
	return mds.DAGService.Add(ctx, n)
}

func (mds *mockDAGSvc) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	nd, err := mds.DAGService.Get(ctx, c)
	if err != nil {
		return nd, err
	}
	return nd, nil
}

func (mds *mockDAGSvc) GetMany(ctx context.Context, cids []cid.Cid) <-chan *ipld.NodeOption {
	return mds.DAGService.GetMany(ctx, cids)
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
	bs := mdutils.Bserv()
	dagserv := merkledag.NewDAGService(bs)

	replicaOpts := make([]*Options, n)
	for i := range replicaOpts {
		if opts == nil {
			replicaOpts[i] = DefaultOptions()
		} else {
			copy := *opts
			replicaOpts[i] = &copy
		}

		replicaOpts[i].Logger = &testLogger{
			name: fmt.Sprintf("r#%d: ", i),
			l:    DefaultOptions().Logger,
		}
		replicaOpts[i].RebroadcastInterval = time.Second * 5
		replicaOpts[i].NumWorkers = 5
		replicaOpts[i].DAGSyncerTimeout = time.Second
	}

	replicas := make([]*Datastore, n)
	for i := range replicas {
		dagsync := &mockDAGSvc{
			DAGService: dagserv,
			bs:         bs.Blockstore(),
		}

		var err error
		replicas[i], err = NewDatastore(
			makeStore(t, i),
			// ds.NewLogDatastore(
			// 	makeStore(t, i),
			// 	fmt.Sprintf("crdt-test-%d", i),
			// ),
			ds.NewKey("crdttest"),
			dagsync,
			bcasts[i],
			replicaOpts[i],
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	if debug {
		// nolint:errcheck
		log.SetLogLevel("crdt", "debug")
	}

	closeReplicas := func() {
		cancelBcasts()
		for i, r := range replicas {
			err := r.Close()
			if err != nil {
				t.Error(err)
			}
			// nolint:errcheck
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

	time.Sleep(time.Second)

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
	for i := range nItems {
		k := ds.RandomKey()
		v := fmt.Appendf(nil, "%d", i)
		n := randGen.Intn(len(replicas))
		err := replicas[n].Put(ctx, k, v)
		if err != nil {
			t.Fatal(err)
		}
	}

	time.Sleep(500 * time.Millisecond)

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
	for _, r := range rest {
		n := randGen.Intn(len(replicas))
		err := replicas[n].Put(ctx, ds.NewKey(r.Key), []byte("hola"))
		if err != nil {
			t.Error(err)
		}
	}

	time.Sleep(200 * time.Millisecond)

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
		list, _, err := r.heads.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
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
	time.Sleep(5000 * time.Millisecond)
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

	time.Sleep(1000 * time.Millisecond)

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
	for range nItems {
		k := ds.RandomKey()
		err := r.Put(ctx, k, nil)
		if err != nil {
			t.Fatal(err)
		}
	}

	time.Sleep(100 * time.Millisecond)
	br.dropProb.Store(0)

	// this message will get to everyone
	err := r.Put(ctx, ds.RandomKey(), nil)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(500 * time.Millisecond)
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

func TestCRDTHooks(t *testing.T) {
	ctx := context.Background()

	var put int64
	var deleted int64

	opts := DefaultOptions()
	opts.PutHook = func(k ds.Key, v []byte) {
		atomic.AddInt64(&put, 1)
	}
	opts.DeleteHook = func(k ds.Key) {
		atomic.AddInt64(&deleted, 1)
	}

	replicas, closeReplicas := makeReplicas(t, opts)
	defer closeReplicas()

	k := ds.RandomKey()
	err := replicas[0].Put(ctx, k, nil)
	if err != nil {
		t.Fatal(err)
	}

	err = replicas[0].Delete(ctx, k)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if atomic.LoadInt64(&put) != int64(len(replicas)) {
		t.Error("all replicas should have notified Put", put)
	}
	if atomic.LoadInt64(&deleted) != int64(len(replicas)) {
		t.Error("all replicas should have notified Remove", deleted)
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

	time.Sleep(100 * time.Millisecond)
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

var _ ds.Datastore = (*syncedTrackDs)(nil)

type syncedTrackDs struct {
	ds.Datastore
	syncs map[ds.Key]struct{}
	set   *set
}

func (st *syncedTrackDs) Sync(ctx context.Context, k ds.Key) error {
	st.syncs[k] = struct{}{}
	return st.Datastore.Sync(ctx, k)
}

func (st *syncedTrackDs) isSynced(k ds.Key) bool {
	prefixStr := k.String()
	mustBeSynced := []ds.Key{
		st.set.elemsPrefix(prefixStr),
		st.set.tombsPrefix(prefixStr),
		st.set.keyPrefix(keysNs).Child(k),
	}

	for k := range st.syncs {
		synced := false
		for _, t := range mustBeSynced {
			if k == t || k.IsAncestorOf(t) {
				synced = true
				break
			}
		}
		if !synced {
			return false
		}
	}
	return true
}

func TestCRDTSync(t *testing.T) {
	ctx := context.Background()

	opts := DefaultOptions()
	replicas, closeReplicas := makeReplicas(t, opts)
	defer closeReplicas()

	syncedDs := &syncedTrackDs{
		Datastore: replicas[0].set.store,
		syncs:     make(map[ds.Key]struct{}),
		set:       replicas[0].set,
	}

	replicas[0].set.store = syncedDs
	k1 := ds.NewKey("/hello/bye")
	k2 := ds.NewKey("/hello")
	k3 := ds.NewKey("/hell")

	err := replicas[0].Put(ctx, k1, []byte("value1"))
	if err != nil {
		t.Fatal(err)
	}

	err = replicas[0].Put(ctx, k2, []byte("value2"))
	if err != nil {
		t.Fatal(err)
	}

	err = replicas[0].Put(ctx, k3, []byte("value3"))
	if err != nil {
		t.Fatal(err)
	}

	err = replicas[0].Sync(ctx, ds.NewKey("/hello"))
	if err != nil {
		t.Fatal(err)
	}

	if !syncedDs.isSynced(k1) {
		t.Error("k1 should have been synced")
	}

	if !syncedDs.isSynced(k2) {
		t.Error("k2 should have been synced")
	}

	if syncedDs.isSynced(k3) {
		t.Error("k3 should have not been synced")
	}
}

func TestCRDTBroadcastBackwardsCompat(t *testing.T) {
	ctx := context.Background()
	mh, err := multihash.Sum([]byte("emacs is best"), multihash.SHA2_256, -1)
	if err != nil {
		t.Fatal(err)
	}
	cidV0 := cid.NewCidV0(mh)

	opts := DefaultOptions()
	replicas, closeReplicas := makeReplicas(t, opts)
	defer closeReplicas()

	headsMap, err := replicas[0].decodeBroadcast(ctx, cidV0.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	if len(headsMap[""]) != 1 || !headsMap[""][0].Cid.Equals(cidV0) {
		t.Error("should have returned a single cidV0", headsMap[""])
	}

	data, err := replicas[0].encodeBroadcast(ctx, headsMap[""])
	if err != nil {
		t.Fatal(err)
	}

	headsMap2, err := replicas[0].decodeBroadcast(ctx, data)
	if err != nil {
		t.Fatal(err)
	}

	if len(headsMap2[""]) != 1 || !headsMap2[""][0].Cid.Equals(cidV0) {
		t.Error("should have reparsed cid0", headsMap2[""])
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

	time.Sleep(15 * time.Second)

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

func TestMigration0to1(t *testing.T) {
	replicas, closeReplicas := makeNReplicas(t, 1, nil)
	defer closeReplicas()
	replica := replicas[0]
	ctx := context.Background()

	nItems := 200
	var keys []ds.Key
	// Add nItems
	for i := range nItems {
		k := ds.RandomKey()
		keys = append(keys, k)
		v := fmt.Appendf(nil, "%d", i)
		err := replica.Put(ctx, k, v)
		if err != nil {
			t.Fatal(err)
		}

	}

	// Overwrite n/2 items 5 times to have multiple tombstones per key
	// later...
	for range 5 {
		for i := 0; i < nItems/2; i++ {
			v := fmt.Appendf(nil, "%d", i)
			err := replica.Put(ctx, keys[i], v)
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	// delete keys
	for i := 0; i < nItems/2; i++ {
		err := replica.Delete(ctx, keys[i])
		if err != nil {
			t.Fatal(err)
		}
	}

	// And write them again
	for i := 0; i < nItems/2; i++ {
		err := replica.Put(ctx, keys[i], []byte("final value"))
		if err != nil {
			t.Fatal(err)
		}
	}

	// And now we manually put the wrong value
	for i := 0; i < nItems/2; i++ {
		valueK := replica.set.valueKey(keys[i].String())
		err := replica.set.store.Put(ctx, valueK, []byte("wrong value"))
		if err != nil {
			t.Fatal(err)
		}
		err = replica.set.setPriority(ctx, replica.set.store, keys[i].String(), 1)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := replica.migrate0to1(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < nItems/2; i++ {
		v, err := replica.Get(ctx, keys[i])
		if err != nil {
			t.Fatal(err)
		}
		if string(v) != "final value" {
			t.Fatalf("value for elem %d should be final value: %s", i, string(v))
		}
	}
}

func TestCRDTDagNames(t *testing.T) {
	// make 2 replicas
	replicas, closeReplicas := makeNReplicas(t, 2, nil)
	defer closeReplicas()

	ctx := context.Background()

	nItems := 50

	// create delta manually for each item with store.set.Add()
	// delta.SetDagName() alternatively to "dag1" and "dag2"
	for i := range nItems {
		k := ds.RandomKey()
		v := fmt.Appendf(nil, "value-%d", i)

		// Create delta manually
		delta, err := replicas[0].set.Add(ctx, k.String(), v)
		if err != nil {
			t.Fatal(err)
		}

		// Set different DAG names for alternating items
		if i%2 == 0 {
			delta.SetDagName("dag1")
		} else {
			delta.SetDagName("dag2")
		}

		// Commit the delta
		_, err = replicas[0].publish(ctx, delta)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Wait for propagation
	time.Sleep(100 * time.Millisecond)

	// verify there are 2 distinct heads (.List())
	heads, _, err := replicas[0].heads.List(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// replicas[0].PrintDAG(ctx)

	if len(heads) != 2 {
		t.Fatalf("Expected 2 heads, got %d", len(heads))
	}

	// verify one head has dagName set to "dag1" and the other to "dag2"
	headDagNames := make(map[string]bool)
	for _, head := range heads {
		headDagNames[head.DAGName] = true
	}

	if !headDagNames["dag1"] {
		t.Error("Expected a head with DAGName 'dag1'")
	}

	if !headDagNames["dag2"] {
		t.Error("Expected a head with DAGName 'dag2'")
	}

	var expectedDagName string
	visit := func(n ipld.Node) error {
		dbytes, err := extractDelta(n)
		if err != nil {
			t.Fatal(err)
		}
		d := replicas[0].opts.crdtOpts.DeltaFactory()
		d.Unmarshal(dbytes) // nolint:errcheck

		if d.GetDagName() != expectedDagName {
			return fmt.Errorf("wrong dagName in subtree: got %s, expected %s", d.GetDagName(), expectedDagName)
		}
		return nil
	}

	mcrdt := MerkleCRDT{replicas[0]}
	for _, h := range heads {
		expectedDagName = h.DAGName
		err := mcrdt.Traverse(ctx, []cid.Cid{h.Cid}, visit)
		if err != nil {
			t.Error(err)
		}
	}
}

func TestCRDTHeadsSaveLoad(t *testing.T) {
	// make 1 replica
	replicas, closeReplicas := makeNReplicas(t, 1, nil)
	defer closeReplicas()
	replica := replicas[0]
	ctx := context.Background()

	// for dagname = range ["", "dag1", "dag2"]:
	//  generate a random cid
	//  add a head to the replica heads.
	dagNames := []string{"", "dag1", "dag2"}
	for _, dagName := range dagNames {
		// Generate a random cid
		k := ds.RandomKey()
		pref := cid.Prefix{
			Version: 1,
		}

		c, err := pref.Sum([]byte(k.String()))
		if err != nil {
			t.Fatal(err)
		}

		// Add a head to the replica heads
		head := Head{
			Cid: c,
			HeadValue: HeadValue{
				Height:  1,
				DAGName: dagName,
			},
		}
		err = replica.heads.Add(ctx, head)
		if err != nil {
			t.Fatal(err)
		}
	}

	heads, err := newHeads(ctx, replica.store, replica.heads.namespace, replica.heads.namespaceDags, replica.heads.logger)
	if err != nil {
		t.Fatal(err)
	}

	// Verify there are 3 heads and they are the ones written before
	newheads, _, err := heads.List(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(newheads) != 3 {
		t.Fatalf("Expected 3 heads, got %d", len(newheads))
	}

	// Verify the heads have the correct DAG names
	headDagNames := make(map[string]bool)
	for _, head := range newheads {
		headDagNames[head.DAGName] = true
	}

	if !headDagNames[""] {
		t.Error("Expected a head with DAGName ''")
	}

	if !headDagNames["dag1"] {
		t.Error("Expected a head with DAGName 'dag1'")
	}

	if !headDagNames["dag2"] {
		t.Error("Expected a head with DAGName 'dag2'")
	}
}

func TestCRDTIsProcessed(t *testing.T) {
	replicas, closeReplicas := makeNReplicas(t, 1, nil)
	defer closeReplicas()
	mcrdt := MerkleCRDT{replicas[0]}
	ctx := context.Background()

	// A CID that was never added should not be processed.
	mh, err := multihash.Sum([]byte("never-added"), multihash.SHA2_256, -1)
	if err != nil {
		t.Fatal(err)
	}
	unknownCID := cid.NewCidV1(cid.DagProtobuf, mh)
	processed, err := mcrdt.IsProcessed(ctx, unknownCID)
	if err != nil {
		t.Fatal(err)
	}
	if processed {
		t.Error("unknown CID should not be processed")
	}

	// Put a value — this synchronously creates and processes a DAG block.
	err = mcrdt.Put(ctx, ds.NewKey("/testkey"), []byte("testval"))
	if err != nil {
		t.Fatal(err)
	}

	// All head CIDs should be marked as processed now.
	heads, _, err := mcrdt.Heads().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(heads) == 0 {
		t.Fatal("expected at least one head after Put")
	}
	for _, head := range heads {
		processed, err := mcrdt.IsProcessed(ctx, head.Cid)
		if err != nil {
			t.Fatal(err)
		}
		if !processed {
			t.Errorf("head %s should be processed after Put", head.Cid)
		}
	}
}

func TestCRDTLenDag(t *testing.T) {
	// make a replica
	replicas, closeReplicas := makeNReplicas(t, 1, nil)
	defer closeReplicas()
	replica := replicas[0]
	ctx := context.Background()

	// publish one delta for dag1
	k1 := ds.RandomKey()
	v1 := []byte("value1")
	delta1, err := replica.set.Add(ctx, k1.String(), v1)
	if err != nil {
		t.Fatal(err)
	}
	delta1.SetDagName("dag1")
	_, err = replica.publish(ctx, delta1)
	if err != nil {
		t.Fatal(err)
	}

	// publish 2 deltas for dag2
	k2 := ds.RandomKey()
	v2 := []byte("value2")
	delta2, err := replica.set.Add(ctx, k2.String(), v2)
	if err != nil {
		t.Fatal(err)
	}
	delta2.SetDagName("dag2")
	_, err = replica.publish(ctx, delta2)
	if err != nil {
		t.Fatal(err)
	}

	k3 := ds.RandomKey()
	v3 := []byte("value3")
	delta3, err := replica.set.Add(ctx, k3.String(), v3)
	if err != nil {
		t.Fatal(err)
	}
	delta3.SetDagName("dag2")
	_, err = replica.publish(ctx, delta3)
	if err != nil {
		t.Fatal(err)
	}

	lenDag1, err := replica.heads.LenDAG(ctx, "dag1")
	if err != nil {
		t.Fatal(err)
	}
	if lenDag1 != 1 {
		t.Errorf("Expected 1 head for dag1, got %d", lenDag1)
	}

	lenDag2, err := replica.heads.LenDAG(ctx, "dag2")
	if err != nil {
		t.Fatal(err)
	}
	if lenDag2 != 1 {
		t.Errorf("Expected 1 heads for dag2, got %d", lenDag2)
	}

	_, max, _ := replica.heads.ListDAG(ctx, "dag2")
	if max != 2 {
		t.Errorf("Expected height = 2 for dag2")
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

	// publish one delta for dag1
	k1 := ds.RandomKey()
	v1 := []byte("value1")
	delta1, err := replica.set.Add(ctx, k1.String(), v1)
	if err != nil {
		t.Fatal(err)
	}
	delta1.SetDagName("dag1")
	_, err = replica.publish(ctx, delta1)
	if err != nil {
		t.Fatal(err)
	}

	// publish 2 deltas for dag2
	k2 := ds.RandomKey()
	v2 := []byte("value2")
	delta2, err := replica.set.Add(ctx, k2.String(), v2)
	if err != nil {
		t.Fatal(err)
	}
	delta2.SetDagName("dag2")
	_, err = replica.publish(ctx, delta2)
	if err != nil {
		t.Fatal(err)
	}

	k3 := ds.RandomKey()
	v3 := []byte("value3")
	delta3, err := replica.set.Add(ctx, k3.String(), v3)
	if err != nil {
		t.Fatal(err)
	}
	delta3.SetDagName("dag2")
	_, err = replica.publish(ctx, delta3)
	if err != nil {
		t.Fatal(err)
	}

	// wait for Next() to be called
	data := mb.Last()

	// calls decodeBroadcast on the response and verifies that there are 3 heads in the response
	heads, err := replica.decodeBroadcast(ctx, data)
	if err != nil {
		t.Fatal(err)
	}

	// Should have one DAG with 2 heads since the last update in
	// "dag2" replaced the previous head as it became a child.
	var totalHeads int
	for _, headList := range heads {
		totalHeads += len(headList)
	}

	if totalHeads != 2 {
		t.Errorf("Expected 2 heads in broadcast, got %d: %s", totalHeads, heads)
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

func TestPurgeDAG(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, nil)
	t.Cleanup(closeReplicas)
	replica := replicas[0]
	mcrdt := MerkleCRDT{replica}
	ctx := t.Context()

	// Publish one delta under dag1 and two under dag2.
	k1 := ds.NewKey("key1")
	delta1, err := replica.set.Add(ctx, k1.String(), []byte("val1"))
	if err != nil {
		t.Fatal(err)
	}
	delta1.SetDagName("dag1")
	if _, err := replica.publish(ctx, delta1); err != nil {
		t.Fatal(err)
	}

	k2 := ds.NewKey("key2")
	delta2, err := replica.set.Add(ctx, k2.String(), []byte("val2"))
	if err != nil {
		t.Fatal(err)
	}
	delta2.SetDagName("dag2")
	if _, err := replica.publish(ctx, delta2); err != nil {
		t.Fatal(err)
	}

	k3 := ds.NewKey("key3")
	delta3, err := replica.set.Add(ctx, k3.String(), []byte("val3"))
	if err != nil {
		t.Fatal(err)
	}
	delta3.SetDagName("dag2")
	if _, err := replica.publish(ctx, delta3); err != nil {
		t.Fatal(err)
	}

	// Collect dag1 head CIDs before purge so we can check IsProcessed later.
	dag1Heads, _, err := replica.heads.ListDAG(ctx, "dag1")
	if err != nil {
		t.Fatal(err)
	}

	// Purge dag1.
	n, err := mcrdt.PurgeDAG(ctx, "dag1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("expected non-zero blocks removed for dag1")
	}

	// dag1 heads are gone.
	dag1HeadsAfter, _, err := replica.heads.ListDAG(ctx, "dag1")
	if err != nil {
		t.Fatal(err)
	}
	if len(dag1HeadsAfter) != 0 {
		t.Errorf("expected 0 dag1 heads after purge, got %d", len(dag1HeadsAfter))
	}

	// dag2 heads remain.
	dag2Heads, _, err := replica.heads.ListDAG(ctx, "dag2")
	if err != nil {
		t.Fatal(err)
	}
	if len(dag2Heads) != 1 {
		t.Error("dag2 heads should survive purge of dag1")
	}

	// Set entry for k1 (dag1 key) is gone.
	_, err = replica.set.Element(ctx, k1.String())
	if !errors.Is(err, ds.ErrNotFound) {
		t.Errorf("expected ErrNotFound for key1 after purge, got %v", err)
	}

	// Set entries for dag2 keys remain.
	if _, err := replica.set.Element(ctx, k2.String()); err != nil {
		t.Errorf("key2 should survive purge of dag1: %v", err)
	}
	if _, err := replica.set.Element(ctx, k3.String()); err != nil {
		t.Errorf("key3 should survive purge of dag1: %v", err)
	}

	// Processed block markers for dag1 blocks are gone.
	for _, h := range dag1Heads {
		processed, err := mcrdt.IsProcessed(ctx, h.Cid)
		if err != nil {
			t.Fatal(err)
		}
		if processed {
			t.Errorf("dag1 CID %s should not be processed after purge", h.Cid)
		}
	}
}

func TestPurgeDAGIdempotent(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, nil)
	t.Cleanup(closeReplicas)
	mcrdt := MerkleCRDT{replicas[0]}
	ctx := t.Context()

	n, err := mcrdt.PurgeDAG(ctx, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected 0 blocks removed for unknown dagName, got %d", n)
	}
}

func TestPurgeDAGMixedKey(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, nil)
	t.Cleanup(closeReplicas)
	replica := replicas[0]
	mcrdt := MerkleCRDT{replica}
	ctx := t.Context()

	// Both dag1 and dag2 write to the same key with different values.
	key := ds.NewKey("shared").String()

	delta1, err := replica.set.Add(ctx, key, []byte("from-dag1"))
	if err != nil {
		t.Fatal(err)
	}
	delta1.SetDagName("dag1")
	if _, err := replica.publish(ctx, delta1); err != nil {
		t.Fatal(err)
	}

	delta2, err := replica.set.Add(ctx, key, []byte("from-dag2"))
	if err != nil {
		t.Fatal(err)
	}
	delta2.SetDagName("dag2")
	if _, err := replica.publish(ctx, delta2); err != nil {
		t.Fatal(err)
	}

	// Purge dag2.
	if _, err := mcrdt.PurgeDAG(ctx, "dag2"); err != nil {
		t.Fatal(err)
	}

	// The key should still exist with dag1's value.
	val, err := replica.set.Element(ctx, key)
	if err != nil {
		t.Fatalf("key should survive after purging dag2: %v", err)
	}
	if string(val) != "from-dag1" {
		t.Errorf("expected 'from-dag1', got %q", string(val))
	}
}

func TestPurgeDAGCleanStore(t *testing.T) {
	ctx := t.Context()
	mapDs := dssync.MutexWrap(ds.NewMapDatastore())
	dagserv := merkledag.NewDAGService(mdutils.Bserv())
	dagsync := &mockDAGSvc{
		DAGService: dagserv,
		bs:         mdutils.Bserv().Blockstore(),
	}

	opts := DefaultOptions()
	opts.Logger = &testLogger{
		name: "purge-clean: ",
		l:    DefaultOptions().Logger,
	}

	namespace := ds.NewKey("crdttest")
	replica, err := NewDatastore(mapDs, namespace, dagsync, &nullBroadcaster{}, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { replica.Close() })

	mcrdt := MerkleCRDT{replica}

	// Publish two keys under dag1.
	delta1, err := replica.set.Add(ctx, "key1", []byte("val1"))
	if err != nil {
		t.Fatal(err)
	}
	delta1.SetDagName("dag1")
	if _, err := replica.publish(ctx, delta1); err != nil {
		t.Fatal(err)
	}
	delta2, err := replica.set.Add(ctx, "key2", []byte("val2"))
	if err != nil {
		t.Fatal(err)
	}
	delta2.SetDagName("dag1")
	if _, err := replica.publish(ctx, delta2); err != nil {
		t.Fatal(err)
	}

	// Purge dag1.
	n, err := mcrdt.PurgeDAG(ctx, "dag1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatal("expected non-zero blocks removed")
	}

	// Query all keys remaining in the store.
	results, err := mapDs.Query(ctx, query.Query{KeysOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { results.Close() })

	var remaining []string
	for r := range results.Next() {
		if r.Error != nil {
			t.Fatal(r.Error)
		}
		remaining = append(remaining, r.Key)
	}

	// The only keys that should survive are datastore-level metadata keys
	// not associated with any dagName: the version key and the
	// bad-shutdown key (written by New and cleared by Close).
	allowed := map[string]bool{
		namespace.ChildString(versionKey).String():     true,
		namespace.ChildString(badShutdownKey).String(): true,
	}
	for _, key := range remaining {
		if !allowed[key] {
			t.Errorf("unexpected key remaining after purge: %s", key)
		}
	}
	if len(remaining) != len(allowed) {
		t.Errorf("expected exactly %d datastore-level keys remaining, got %d: %v", len(allowed), len(remaining), remaining)
	}
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
	defer func() { _ = d.Close() }()

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

// TestBadShutdownMark_PartialBranchRecovery builds a 2-node chain, fetches
// the head's child, corrupts the datastore by deleting the child's processed
// marker, and asserts that on reopen the bad-shutdown mark triggers a repair
// that restores the missing state.
func TestBadShutdownMark_PartialBranchRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		mapDs := dssync.MutexWrap(ds.NewMapDatastore())
		dagServ := merkledag.NewDAGService(mdutils.Bserv())

		d1 := openTestStore(t, "r1", mapDs, dagServ, time.Hour)
		if err := d1.Put(ctx, ds.NewKey("/k1"), []byte("v1")); err != nil {
			t.Fatal(err)
		}
		if err := d1.Put(ctx, ds.NewKey("/k2"), []byte("v2")); err != nil {
			t.Fatal(err)
		}

		headsList, _, err := d1.heads.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(headsList) != 1 {
			t.Fatalf("expected exactly 1 head after two sequential Puts, got %d", len(headsList))
		}
		headCid := headsList[0].Cid

		headNode, err := dagServ.Get(ctx, headCid)
		if err != nil {
			t.Fatal(err)
		}
		links := headNode.Links()
		if len(links) == 0 {
			t.Fatal("head node should link to at least one prior block")
		}
		childCid := links[0].Cid
		childProcessedKey := d1.processedBlockKey(childCid)

		for _, c := range []cid.Cid{headCid, childCid} {
			ok, err := d1.isProcessed(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatalf("block %s should be processed before corruption", c)
			}
		}
		if err := d1.Close(); err != nil {
			t.Fatal(err)
		}

		// Corrupt the datastore: drop the child's processed marker and
		// re-arm the bad-shutdown key so reopen detects an unclean prior
		// run.
		if err := mapDs.Delete(ctx, childProcessedKey); err != nil {
			t.Fatal(err)
		}
		if err := mapDs.Put(ctx, ds.NewKey("crdttest").ChildString(badShutdownKey), nil); err != nil {
			t.Fatal(err)
		}

		d2 := openTestStore(t, "r2", mapDs, dagServ, time.Hour)
		t.Cleanup(func() { _ = d2.Close() })

		if !d2.IsDirty(ctx) {
			t.Fatal("store should be dirty after detecting a prior bad shutdown")
		}

		synctest.Wait()

		ok, err := d2.isProcessed(ctx, childCid)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatal("child should be processed after repair")
		}
		if d2.IsDirty(ctx) {
			t.Fatal("store should be marked clean after successful repair")
		}
	})
}
