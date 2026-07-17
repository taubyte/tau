// Package crdt provides a replicated go-datastore (key-value store)
// implementation using Merkle-CRDTs built with IPLD nodes.
//
// This Datastore is agnostic to how new MerkleDAG roots are broadcasted to
// the rest of replicas (`Broadcaster` component) and to how the IPLD nodes
// are made discoverable and retrievable to by other replicas (`DAGSyncer`
// component).
//
// The implementation is based on the "Merkle-CRDTs: Merkle-DAGs meet CRDTs"
// paper by Héctor Sanjuán, Samuli Pöyhtäri and Pedro Teixeira.
//
// Note that, in the absence of compaction, a crdt.Datastore will only grow
// in size even when keys are deleted: deleting a key adds a tombstone
// without removing the history that preceded it. Compact (or, for outright
// deletion of a whole named DAG's history, PurgeDAG) can be called manually
// to fold a named DAG's live state into a small "snapshot" generation and
// discard the DAG history it replaces. Compact is coordination-free, like
// every other operation this package exposes: it needs no quiescence or
// single-writer guarantee from the caller. See the Compact doc comment for
// the full algorithm and the (non-mandatory) notes on replica upgrade
// ordering.
//
// The time to be fully synced for new Datastore replicas will depend on how
// fast they can retrieve the DAGs announced by the other replicas, but newer
// values will be available before older ones.
package kvdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	dshelp "github.com/ipfs/boxo/datastore/dshelp"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"google.golang.org/protobuf/proto"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	ipld "github.com/ipfs/go-ipld-format"
	logging "github.com/ipfs/go-log/v2"
)

var (
	_ ds.Datastore = (*Datastore)(nil)
	_ ds.Batching  = (*Datastore)(nil)
)

// datastore namespace keys. Short keys save space and memory.
const (
	headsNs           = "h"  // heads
	dagHeadsNs        = "a"  // dagHeads - heads for named dags.
	setNs             = "s"  // set
	processedBlocksNs = "b"  // blocks
	dirtyBitKey       = "d"  // dirty
	badShutdownKey    = "bs" // bad-shutdown: set on New, cleared on clean Close
	versionKey        = "crdt_version"
	reclaimNs         = "rc" // reclaim - receiver-side reclamation bookkeeping
)

// Common errors.
var (
	ErrNoMoreBroadcast = errors.New("receiving blocks aborted since no new blocks will be broadcasted")
)

// A Broadcaster provides a way to send (notify) an opaque payload to
// all replicas and to retrieve payloads broadcasted.
type Broadcaster interface {
	// Send payload to other replicas.
	Broadcast(context.Context, []byte) error
	// Obtain the next payload received from the network.
	Next(context.Context) ([]byte, error)
}

// A SessionDAGService is a Sessions-enabled DAGService. This type of DAG-Service
// provides an optimized NodeGetter to make multiple related requests. The
// same session-enabled NodeGetter is used to download DAG branches when
// the DAGSyncer supports it.
type SessionDAGService interface {
	ipld.DAGService
	Session(context.Context) ipld.NodeGetter
}

// Options holds configurable values for Datastore.
type Options struct {
	Logger logging.StandardLogger
	// RebroadcastInterval specifies how often the system
	// re-publishes its latest heads so that new replicas can
	// learn about them. It only broadcasts heads that have not
	// been already broadcasted by other replicas in the
	// interval. Default: 1m.
	RebroadcastInterval time.Duration
	// The PutHook function is triggered whenever an element
	// is successfully added to the datastore (either by a local
	// or remote update), and only when that addition is considered the
	// prevalent value. Default: nil.
	PutHook func(k ds.Key, v []byte)
	// The DeleteHook function is triggered whenever a version of an
	// element is successfully removed from the datastore (either by a
	// local or remote update). Unordered and concurrent updates may
	// result in the DeleteHook being triggered even though the element is
	// still present in the datastore because it was re-added or not fully
	// tombstoned. If that is relevant, use Has() to check if the removed
	// element is still part of the datastore. Default: nil.
	DeleteHook func(k ds.Key)
	// NumWorkers specifies the number of workers ready to
	// retrieve and merge deltas while walking the DAGs. Default:
	// 5.
	NumWorkers int
	// DAGSyncerTimeout specifies how long to wait for a DAGSyncer
	// to, for example, receive a delta from a different replica.
	// Set to 0 to disable. Default: 5m.
	DAGSyncerTimeout time.Duration
	// MaxBatchDeltaSize will automatically commit any batches whose
	// delta size gets too big. This helps keep DAG nodes small
	// enough that they will be transferred by the network. Default: 1MiB.
	MaxBatchDeltaSize int
	// RepairInterval specifies how often to walk the full DAG until
	// the root(s) if it has been marked dirty. 0 to disable. Default: 1h.
	RepairInterval time.Duration
	// MultiHeadProcessing lets several new heads to be processed
	// in parallel. This results in more branching in
	// general. More branching is not necessarily a bad thing and
	// may improve throughput, but everything depends on
	// usage. Default: false.
	MultiHeadProcessing bool
	// BroadcastBatchDelay batches the new Head broadcasts when
	// publishing new deltas into a single message. As a result,
	// broadcast traffic is reduced on systems with high
	// utilization. On the other side, the delay introduces
	// latency as a change will not be published until it is
	// included in the crdtBatch when the delay expires. 0 to
	// disable. Default: 0.
	BroadcastBatchDelay time.Duration
	// ReclaimOnSnapshot enables receiver-side reclamation of compacted
	// history: when a replica merges a snapshot delta produced by another
	// replica's Compact() call and has now merged every sibling of that
	// compaction generation, it purges its own local copy of the DAG
	// history that snapshot generation covers (blocks and set entries),
	// exactly as Compact does on the compacting replica. This is a
	// best-effort, soft-failure feature: a failed or missed reclaim never
	// fails the triggering merge and never corrupts state, it just leaves
	// local history unreclaimed until the next opportunity. Use
	// Datastore.ReclaimCompacted for manual/recovery reclamation (crash
	// windows, legacy pre-metadata snapshots, or when this option is
	// disabled). Default: true.
	ReclaimOnSnapshot bool
	// advanced options
	crdtOpts MerkleCRDTOptions
}

func (opts *Options) verify() error {
	if opts == nil {
		return errors.New("options cannot be nil")
	}

	if opts.RebroadcastInterval <= 0 {
		return errors.New("invalid RebroadcastInterval")
	}

	if opts.Logger == nil {
		return errors.New("the Logger is undefined")
	}

	if opts.NumWorkers <= 0 {
		return errors.New("bad number of NumWorkers")
	}

	if opts.DAGSyncerTimeout < 0 {
		return errors.New("invalid DAGSyncerTimeout")
	}

	if opts.MaxBatchDeltaSize <= 0 {
		return errors.New("invalid MaxBatchDeltaSize")
	}

	if opts.RepairInterval < 0 {
		return errors.New("invalid RepairInterval")
	}

	if opts.BroadcastBatchDelay < 0 {
		return errors.New("invalid BroadcastBatchDelay")
	}

	if opts.crdtOpts.DeltaFactory == nil {
		panic("deltaFactory is unset, and this should never happen")
	}

	switch {
	case opts.crdtOpts.Namespaces.Heads == "",
		opts.crdtOpts.Namespaces.Set == "",
		opts.crdtOpts.Namespaces.ProcessedBlocks == "",
		opts.crdtOpts.Namespaces.DirtyBitKey == "",
		opts.crdtOpts.Namespaces.BadShutdownKey == "",
		opts.crdtOpts.Namespaces.VersionKey == "",
		opts.crdtOpts.Namespaces.Reclaim == "":
		panic("one or several InternalNamespaces are unset, and this should never happen")
	}

	return nil
}

// DefaultOptions initializes an Options object with sensible defaults.
func DefaultOptions() *Options {
	return &Options{
		Logger:              logging.Logger("crdt"),
		RebroadcastInterval: time.Minute,
		PutHook:             nil,
		DeleteHook:          nil,
		NumWorkers:          5,
		DAGSyncerTimeout:    5 * time.Minute,
		// always keeping
		// https://github.com/libp2p/go-libp2p-core/blob/master/network/network.go#L23
		// in sight
		MaxBatchDeltaSize:   1 * 1024 * 1024, // 1MB,
		RepairInterval:      time.Hour,
		MultiHeadProcessing: false,
		BroadcastBatchDelay: 0,
		ReclaimOnSnapshot:   true,

		crdtOpts: MerkleCRDTOptions{
			DeltaFactory: func() Delta { return &pbDelta{Delta: &pb.Delta{}} },
			Namespaces: InternalNamespaces{
				Heads:           headsNs,
				DAGHeads:        dagHeadsNs,
				Set:             setNs,
				ProcessedBlocks: processedBlocksNs,
				DirtyBitKey:     dirtyBitKey,
				BadShutdownKey:  badShutdownKey,
				VersionKey:      versionKey,
				Reclaim:         reclaimNs,
			},
		},
	}
}

// Datastore makes a go-datastore a distributed Key-Value store using
// Merkle-CRDTs and IPLD.
type Datastore struct {
	ctx    context.Context
	cancel context.CancelFunc

	opts   *Options
	logger logging.StandardLogger

	// permanent storage
	store     ds.Datastore
	namespace ds.Key
	set       *set
	heads     *heads

	dagService       ipld.DAGService
	broadcaster      Broadcaster
	broadcastBatchCh chan broadcastBatchHead

	seenHeadsMux sync.RWMutex
	seenHeads    map[cid.Cid]struct{}

	curDeltaMux sync.Mutex
	curDelta    Delta // current, unpublished delta

	// compactMux serializes Compact() runs against local publishes
	// (addDAGNode): Compact holds it for its entire run so that a local
	// Put/Delete/Batch.Commit cannot add a new DAG node referencing heads
	// that Compact is in the middle of collapsing/purging.
	compactMux sync.Mutex

	// reclaimMux guards the read-modify-write of the per-generation
	// sibling counter used by receiver-side reclamation (see
	// processNode's allowReclaim handling and reclaimCovered).
	reclaimMux sync.Mutex

	wg sync.WaitGroup

	jobQueue chan *dagJob
	sendJobs chan *dagJob
	// keep track of children to be fetched so only one job does every
	// child
	queuedChildren *cidSafeSet
}

type dagJob struct {
	ctx        context.Context // A job context for tracing
	session    *sync.WaitGroup // A waitgroup to wait for all related jobs to conclude
	nodeGetter *crdtNodeGetter // a node getter to use
	root       Head            // the root of the branch we are walking down
	delta      Delta           // the current delta
	node       ipld.Node       // the current ipld Node
}

type broadcastBatchHead struct {
	head     Head
	children []Head
}

// New returns a Merkle-CRDT-based Datastore using the given one to persist
// all the necessary data under the given namespace. It needs a DAG-Service
// component for IPLD nodes and a Broadcaster component to distribute and
// receive information to and from the rest of replicas. Actual implementation
// of these must be provided by the user, but it normally means using
// ipfs-lite (https://github.com/hsanjuan/ipfs-lite) as a DAG Service and the
// included libp2p PubSubBroadcaster as a Broadcaster.
//
// The given Datastore is used to back all CRDT-datastore contents and
// accounting information. When using an asynchronous datastore, the user is
// in charge of calling Sync() regularly. Sync() will persist paths related to
// the given prefix, but note that if other replicas are modifying the
// datastore, the prefixes that will need syncing are not only those modified
// by the local replica. Therefore the user should consider calling Sync("/"),
// with an empty prefix, in that case, or use a synchronous underlying
// datastore that persists things directly on write.
//
// The CRDT-Datastore should call Close() before the given store is closed.
func NewDatastore(
	store ds.Datastore,
	namespace ds.Key,
	dagSyncer ipld.DAGService,
	bcast Broadcaster,
	opts *Options,
) (*Datastore, error) {
	if opts == nil {
		opts = DefaultOptions()
	}

	if err := opts.verify(); err != nil {
		return nil, err
	}

	// <namespace>/set
	fullSetNs := namespace.ChildString(opts.crdtOpts.Namespaces.Set)
	// <namespace>/heads
	fullHeadsNs := namespace.ChildString(opts.crdtOpts.Namespaces.Heads)

	// <namespace>/heads
	fullDagHeadsNs := namespace.ChildString(opts.crdtOpts.Namespaces.DAGHeads)

	setPutHook := func(k string, v []byte) {
		if opts.PutHook == nil {
			return
		}
		dsk := ds.NewKey(k)
		opts.PutHook(dsk, v)
	}

	setDeleteHook := func(k string) {
		if opts.DeleteHook == nil {
			return
		}
		dsk := ds.NewKey(k)
		opts.DeleteHook(dsk)
	}

	ctx, cancel := context.WithCancel(context.Background())
	set, err := newCRDTSet(ctx, store, fullSetNs, dagSyncer, opts.Logger, setPutHook, setDeleteHook, opts.crdtOpts.DeltaFactory, opts.DAGSyncerTimeout)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("error setting up crdt set: %w", err)
	}
	heads, err := newHeads(ctx, store, fullHeadsNs, fullDagHeadsNs, opts.Logger)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("error building heads: %w", err)
	}

	dstore := &Datastore{
		ctx:              ctx,
		cancel:           cancel,
		opts:             opts,
		logger:           opts.Logger,
		store:            store,
		namespace:        namespace,
		set:              set,
		heads:            heads,
		dagService:       dagSyncer,
		broadcaster:      bcast,
		broadcastBatchCh: make(chan broadcastBatchHead, 32000),
		seenHeads:        make(map[cid.Cid]struct{}),
		jobQueue:         make(chan *dagJob, opts.NumWorkers),
		sendJobs:         make(chan *dagJob),
		queuedChildren:   newCidSafeSet(),
	}

	err = dstore.applyMigrations(ctx)
	if err != nil {
		cancel()
		return nil, err
	}

	// Detect whether the previous run ended with a clean Close(). If the
	// bad-shutdown key is present at startup, the process died without clearing
	// it (crash, kill, OOM, power loss); mark the store dirty so the repair loop
	// walks the DAG and recovers any partially-processed branches.
	hadBadShutdown, err := dstore.store.Has(ctx, dstore.badShutdownKey())
	if err != nil {
		cancel()
		return nil, fmt.Errorf("error checking bad-shutdown key: %w", err)
	}
	if hadBadShutdown {
		dstore.logger.Warn("previous shutdown was not clean; marking datastore as dirty to trigger repair")
		dstore.MarkDirty(ctx)
	}
	// Set the bad-shutdown key so that if we crash before the next clean
	// Close(), we will know on the next startup and trigger a repair. Sync it to
	// disk immediately, otherwise a crash before the next flush would leave the
	// marker only in memory and the crash would go undetected on the next
	// startup.
	if err := dstore.store.Put(ctx, dstore.badShutdownKey(), nil); err != nil {
		cancel()
		return nil, fmt.Errorf("error writing bad-shutdown key: %w", err)
	}
	if err := dstore.store.Sync(ctx, dstore.badShutdownKey()); err != nil {
		cancel()
		return nil, fmt.Errorf("error syncing bad-shutdown key: %w", err)
	}

	headList, maxHeight, err := dstore.heads.List(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	dstore.logger.Infof(
		"crdt Datastore created. Number of heads: %d. Current max-height: %d. Dirty: %t",
		len(headList),
		maxHeight,
		dstore.IsDirty(ctx),
	)

	// sendJobWorker + NumWorkers
	dstore.wg.Add(1 + dstore.opts.NumWorkers)
	go func() {
		defer dstore.wg.Done()
		dstore.sendJobWorker(ctx)
	}()
	for i := 0; i < dstore.opts.NumWorkers; i++ {
		go func() {
			defer dstore.wg.Done()
			dstore.dagWorker()
		}()
	}
	dstore.wg.Add(5)
	go func() {
		defer dstore.wg.Done()
		dstore.handleNext(ctx)
	}()
	go func() {
		defer dstore.wg.Done()
		dstore.rebroadcast(ctx)
	}()
	go func() {
		defer dstore.wg.Done()
		dstore.broadcastBatchWorker(ctx)
	}()

	go func() {
		defer dstore.wg.Done()
		dstore.repair(ctx)
	}()

	go func() {
		defer dstore.wg.Done()
		dstore.logStats(ctx)
	}()

	return dstore, nil
}

// handleNext will loop on broadcaster.Next().  In the general case
// (MultiheadProcessing = false), for each received head it will wait
// until the branch it points to has been processed before returning.
// Different DAGNames are however processed in parallel. Multiple
// heads from the same DAGName are processed sequentially. We wait for
// all DAGname-branches to be finished before continuing to process
// the next broadcast.
//
// When MultiheadProcessing = true, we do not wait for a branch to
// have been processed to return and launch goroutines instead so that
// the processing happens in the background, while we read the next
// updates.
//
// MultiheadProcessing may cause goroutines to queue as the jobQueue
// has as much space as numWorkers.
func (store *Datastore) handleNext(ctx context.Context) {
	if store.broadcaster == nil { // offline
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		data, err := store.broadcaster.Next(ctx)
		if err != nil {
			if err == ErrNoMoreBroadcast || ctx.Err() != nil {
				return
			}
			store.logger.Error(err)
			continue
		}

		receivedHeads, err := store.decodeBroadcast(ctx, data)
		if err != nil {
			store.logger.Error(err)
			continue
		}

		processHead := func(ctx context.Context, h Head) {
			err := store.handleBlock(ctx, h) // handleBlock blocks
			if err != nil {
				store.logger.Errorf("error processing new head: %s", err)
				// For posterity: do not mark the store as
				// Dirty if we could not handle a block. If an
				// error happens here, it means the node could
				// not be fetched, thus it could not be
				// processed, thus it did not leave a branch
				// half-processed and there's nothign to
				// recover.
				// disabled: store.MarkDirty()
			}
		}

		// markHeadsAsSeen asap so that we don't rebroadcast them.
		for _, heads := range receivedHeads {
			for _, head := range heads {
				store.seenHeadsMux.Lock()
				store.seenHeads[head.Cid] = struct{}{}
				store.seenHeadsMux.Unlock()
			}
		}

		var wg sync.WaitGroup
		wg.Add(len(receivedHeads))
		for dagName, heads := range receivedHeads {
			// Send heads for processing. Each dagName is
			// processed in parallel, and follows the
			// MultiHeadProcessing configuration.  If
			// MultiHeadProcessing is false, this will
			// wait until all dagNames heads have been
			// processed.
			go func(dagName string, heads []Head) {
				defer wg.Done()

				// if we have no heads for the DAG name, make
				// seen-heads heads immediately.  On a fresh
				// start, this allows us to start building on
				// top of recent heads, even if we have not
				// fully synced rather than creating new
				// orphan branches.
				curHeadCount, err := store.heads.LenDAG(ctx, dagName)
				if err != nil {
					store.logger.Error(err)
					return
				}
				if curHeadCount == 0 {
					dg := &crdtNodeGetter{NodeGetter: store.dagService}
					for _, head := range heads {
						// getPriority fetches the delta.
						cctx, cancel := context.WithTimeout(ctx, store.opts.DAGSyncerTimeout)
						prio, err := store.getPriority(cctx, dg, head.Cid)
						cancel()
						if err != nil {
							store.logger.Error(err)
							continue
						}
						head.Height = prio
						err = store.heads.Add(ctx, head)
						if err != nil {
							store.logger.Error(err)
						}
					}
				}

				for _, head := range heads {
					// A thing to try here would be to process heads in
					// the same broadcast in parallel, but do not process
					// heads from multiple broadcasts in parallel.
					if store.opts.MultiHeadProcessing {
						go processHead(ctx, head)
					} else {
						processHead(ctx, head)
					}
					store.seenHeadsMux.Lock()
					store.seenHeads[head.Cid] = struct{}{}
					store.seenHeadsMux.Unlock()
				}
			}(dagName, heads)
		}

		// wait for all heads in all dagNames to be processed.
		// If MultiHeadProcessing is enabled this will not wait long as goroutines above
		// will return quickly.
		wg.Wait()

		// --> continue loop for next heads broadcast

		// TODO: We should store trusted-peer signatures associated to
		// each head in a timecache. When we broadcast, attach the
		// signatures (along with our own) to the broadcast.
		// Other peers can use the signatures to verify that the
		// received CIDs have been issued by a trusted peer.
	}
}

// decodeBroadcast parses a CRDTBroadcast received via pubsub and
// returns the heads classified by DAGName.
func (store *Datastore) decodeBroadcast(ctx context.Context, data []byte) (map[string][]Head, error) {
	// Make a list of heads we received
	bcastData := pb.CRDTBroadcast{}
	err := proto.Unmarshal(data, &bcastData)
	if err != nil {
		return nil, err
	}

	// Compatibility: before we were publishing CIDs directly
	msgReflect := bcastData.ProtoReflect()
	if len(msgReflect.GetUnknown()) > 0 {
		// Backwards compatibility
		c, err := cid.Cast(msgReflect.GetUnknown())
		if err != nil {
			return nil, err
		}
		store.logger.Debugf("a legacy CID broadcast was received for: %s", c)
		return map[string][]Head{
			"": {{Cid: c}},
		}, nil
	}

	bCastHeads := make(map[string][]Head, len(bcastData.Heads))
	for _, protoHead := range bcastData.Heads {
		c, err := cid.Cast(protoHead.Cid)
		if err != nil {
			return bCastHeads, err
		}
		// The broadcast does not include the height of the
		// head as this is obtained from the delta, once it is fetched.
		h := Head{Cid: c}
		h.DAGName = protoHead.GetDagName()
		bCastHeads[h.DAGName] = append(bCastHeads[h.DAGName], h)
	}
	return bCastHeads, nil
}

func (store *Datastore) encodeBroadcast(ctx context.Context, heads []Head) ([]byte, error) {
	bcastData := pb.CRDTBroadcast{}
	for _, h := range heads {
		if h.Cid == cid.Undef {
			continue
		}
		bcastData.Heads = append(bcastData.Heads, &pb.Head{
			Cid:     h.Cid.Bytes(),
			DagName: h.DAGName,
		})
	}

	return proto.Marshal(&bcastData)
}

func randomizeInterval(d time.Duration) time.Duration {
	// 30% of the configured interval
	leeway := (d * 30 / 100)
	// A random number between -leeway|+leeway.
	randGen := rand.New(rand.NewSource(time.Now().UnixNano()))
	randomInterval := time.Duration(randGen.Int63n(int64(leeway*2))) - leeway
	return d + randomInterval
}

func (store *Datastore) rebroadcast(ctx context.Context) {
	delay := randomizeInterval(store.opts.RebroadcastInterval)
	timer := time.NewTimer(delay)

	for {
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
			store.rebroadcastHeads(ctx)
			delay := randomizeInterval(store.opts.RebroadcastInterval)
			timer.Reset(delay)
		}
	}
}

func (store *Datastore) broadcastBatchWorker(ctx context.Context) {
	if store.opts.BroadcastBatchDelay == 0 {
		return
	}

	t := time.NewTimer(store.opts.BroadcastBatchDelay)
	var heads []Head
	for {
		select {
		case <-ctx.Done():
			return
		case batchHead := <-store.broadcastBatchCh:
			children := batchHead.children
			head := batchHead.head

			removed := false
			// Remove children from heads
			for _, child := range children {
				for i, curHead := range heads {
					if curHead.Cid.Equals(child.Cid) {
						heads[i].Cid = cid.Undef
						removed = true
					}
				}
			}

			// Sweep to remove Cid.Undef-heads in-place.
			// They would also be ignored by broadcastHeads
			// so this is mostly cosmetic.
			if removed { // skip unless necessary.
				toI := 0
				for fromI := 0; fromI < len(heads); fromI++ {
					if heads[fromI].Cid != cid.Undef {
						if toI != fromI {
							heads[toI] = heads[fromI]
						}
						toI++
					}
				}
				heads = heads[:toI]
			}

			// append the new head
			heads = append(heads, head)
		case <-t.C:
			err := store.broadcastHeads(ctx, heads)
			if err != nil {
				store.logger.Errorf("error broadcasting heads crdtBatch %s: %s", heads, err)
			}
			heads = nil
			t.Reset(store.opts.BroadcastBatchDelay)
		}
	}
}

func (store *Datastore) repair(ctx context.Context) {
	if store.opts.RepairInterval == 0 {
		return
	}
	timer := time.NewTimer(0) // fire immediately on start
	for {
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
			if !store.IsDirty(ctx) {
				store.logger.Info("store is marked clean. No need to repair")
			} else {
				store.logger.Warn("store is marked dirty. Starting DAG repair operation")
				err := store.repairDAG(ctx)
				if err != nil {
					store.logger.Error(err)
				}
			}
			timer.Reset(store.opts.RepairInterval)
		}
	}
}

// regularly send out a list of heads that we have not recently seen.
// If we have seen heads during our randomized rebroadcastInterval, it
// means someone else has broadcasted them. Otherwise, the others
// might not have these heads we we need to broadcast them.
func (store *Datastore) rebroadcastHeads(ctx context.Context) {
	// Get our current list of heads
	heads, _, err := store.heads.List(ctx)
	if err != nil {
		store.logger.Error(err)
		return
	}

	var headsToBroadcast []Head
	store.seenHeadsMux.RLock()
	{
		headsToBroadcast = make([]Head, 0, len(store.seenHeads))
		for _, h := range heads {
			if _, ok := store.seenHeads[h.Cid]; !ok {
				headsToBroadcast = append(headsToBroadcast, h)
			}
		}
	}
	store.seenHeadsMux.RUnlock()

	// Send them out
	err = store.broadcastHeads(ctx, headsToBroadcast)
	if err != nil {
		store.logger.Warnf("broadcast failed: %v", err)
	}

	// Reset the map
	store.seenHeadsMux.Lock()
	clear(store.seenHeads)
	store.seenHeadsMux.Unlock()
}

// Log some stats every 5 minutes.
func (store *Datastore) logStats(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	for {
		select {
		case <-ticker.C:
			heads, height, err := store.heads.List(ctx)
			if err != nil {
				store.logger.Errorf("error listing heads: %s", err)
			}

			store.logger.Infof(
				"Number of heads: %d. Current max height: %d. Queued jobs: %d. Dirty: %t",
				len(heads),
				height,
				len(store.jobQueue),
				store.IsDirty(ctx),
			)
		case <-ctx.Done():
			ticker.Stop()
			return
		}
	}
}

// handleBlock takes care of vetting, retrieving and applying
// CRDT blocks to the Datastore.
func (store *Datastore) handleBlock(ctx context.Context, h Head) error {
	// Ignore already processed blocks.
	// This includes the case when the block is a current
	// head.
	c := h.Cid
	isProcessed, err := store.isProcessed(ctx, c)
	if err != nil {
		return fmt.Errorf("error checking for known block %s: %w", c, err)
	}
	if isProcessed {
		store.logger.Debugf("%s is known. Skip walking tree", c)
		return nil
	}

	return store.handleBranch(ctx, h, c)
}

// send job starting at the given CID in a branch headed by a given head.
// this can be used to continue branch processing from a certain point.
func (store *Datastore) handleBranch(ctx context.Context, head Head, c cid.Cid) error {
	// Walk down from this block
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	dg := &crdtNodeGetter{NodeGetter: store.dagService}
	if sessionMaker, ok := store.dagService.(SessionDAGService); ok {
		dg = &crdtNodeGetter{NodeGetter: sessionMaker.Session(cctx)}
	}

	var session sync.WaitGroup
	err := store.sendNewJobs(ctx, &session, dg, head, []cid.Cid{c})
	session.Wait()
	return err
}

// dagWorker should run in its own goroutine. Workers are launched during
// initialization in NewDatastore().
func (store *Datastore) dagWorker() {
	for job := range store.jobQueue {
		ctx := job.ctx
		select {
		case <-ctx.Done():
			// drain jobs from queue when we are done
			job.session.Done()
			continue
		default:
		}

		children, err := store.processNode(
			ctx,
			job.nodeGetter,
			job.root,
			job.delta,
			job.node,
			true, // dagWorker processes remote/walked blocks: reclaim may trigger.
		)
		if err != nil {
			store.logger.Error(err)
			store.MarkDirty(ctx)
			job.session.Done()
			continue
		}
		go func(j *dagJob) {
			err := store.sendNewJobs(ctx, j.session, j.nodeGetter, j.root, children)
			if err != nil {
				store.logger.Error(err)
				store.MarkDirty(ctx)
			}
			j.session.Done()
		}(job)
	}
}

// sendNewJobs calls getDeltas (GetMany) on the crdtNodeGetter with the given
// children and sends each response to the workers. It will block until all
// jobs have been queued.
func (store *Datastore) sendNewJobs(ctx context.Context, session *sync.WaitGroup, ng *crdtNodeGetter, root Head, children []cid.Cid) error {
	if len(children) == 0 {
		return nil
	}

	cctx, cancel := context.WithTimeout(ctx, store.opts.DAGSyncerTimeout)
	defer cancel()

	// Special case for root
	if root.Height == 0 {
		store.logger.Debugf("getting priority for head %s [%s] - timeout: %s", root.Cid, root.DAGName, store.opts.DAGSyncerTimeout)
		prio, err := store.getPriority(cctx, ng, children[0])
		if err != nil {
			return fmt.Errorf("error getting root delta priority: %s %w", children[0], err)
		}
		root.Height = prio
	}

	goodDeltas := make(map[cid.Cid]struct{})

	var err error
loop:
	for deltaOpt := range ng.GetDeltas(cctx, children) {
		// we abort whenever we a delta comes back in error.
		if deltaOpt.err != nil {
			err = fmt.Errorf("error getting delta: %w", deltaOpt.err)
			break
		}
		goodDeltas[deltaOpt.node.Cid()] = struct{}{}

		delta := store.newDelta()
		err = delta.Unmarshal(deltaOpt.delta)
		if err != nil {
			store.logger.Warn("error unmarshaling children's delta: %s", err)
			continue
		}

		session.Add(1)
		job := &dagJob{
			ctx:        ctx,
			session:    session,
			nodeGetter: ng,
			root:       root,
			delta:      delta,
			node:       deltaOpt.node,
		}
		select {
		case store.sendJobs <- job:
		case <-ctx.Done():
			// the job was never sent, so it cannot complete.
			session.Done()
			// We are in the middle of sending jobs, thus we left
			// something unprocessed.
			err = ctx.Err()
			break loop
		}
	}

	// This is a safe-guard in case GetDeltas() returns less deltas than
	// asked for. It clears up any children that could not be fetched from
	// the queue. The rest will remove themselves in processNode().
	// Hector: as far as I know, this should not execute unless errors
	// happened.
	for _, child := range children {
		if _, ok := goodDeltas[child]; !ok {
			store.logger.Warn("GetDeltas did not include all children")
			store.queuedChildren.Remove(child)
		}
	}
	return err
}

// the only purpose of this worker is to be able to orderly shut-down job
// workers without races by becoming the only sender for the store.jobQueue
// channel.
func (store *Datastore) sendJobWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			if len(store.sendJobs) > 0 {
				// we left something in the queue
				store.MarkDirty(ctx)
			}
			close(store.jobQueue)
			return
		case j := <-store.sendJobs:
			store.jobQueue <- j
		}
	}
}

func (store *Datastore) processedBlockKey(c cid.Cid) ds.Key {
	return store.namespace.ChildString(store.opts.crdtOpts.Namespaces.ProcessedBlocks).ChildString(dshelp.MultihashToDsKey(c.Hash()).String())
}

func (store *Datastore) isProcessed(ctx context.Context, c cid.Cid) (bool, error) {
	return store.store.Has(ctx, store.processedBlockKey(c))
}

func (store *Datastore) markProcessed(ctx context.Context, c cid.Cid) error {
	return store.store.Put(ctx, store.processedBlockKey(c), nil)
}

func (store *Datastore) dirtyKey() ds.Key {
	return store.namespace.ChildString(store.opts.crdtOpts.Namespaces.DirtyBitKey)
}

// badShutdownKey is written on New and removed on a clean Close. Its presence
// at startup indicates the previous run did not Close() cleanly, so the store
// should be treated as dirty and repaired.
func (store *Datastore) badShutdownKey() ds.Key {
	return store.namespace.ChildString(store.opts.crdtOpts.Namespaces.BadShutdownKey)
}

// MarkDirty marks the Datastore as dirty.
func (store *Datastore) MarkDirty(ctx context.Context) {
	store.logger.Warn("marking datastore as dirty")
	err := store.store.Put(ctx, store.dirtyKey(), nil)
	if err != nil {
		store.logger.Errorf("error setting dirty bit: %s", err)
	}
}

// IsDirty returns whether the datastore is marked dirty.
func (store *Datastore) IsDirty(ctx context.Context) bool {
	ok, err := store.store.Has(ctx, store.dirtyKey())
	if err != nil {
		store.logger.Errorf("error checking dirty bit: %s", err)
	}
	return ok
}

// MarkClean removes the dirty mark from the datastore.
func (store *Datastore) MarkClean(ctx context.Context) {
	store.logger.Info("marking datastore as clean")
	err := store.store.Delete(ctx, store.dirtyKey())
	if err != nil {
		store.logger.Errorf("error clearing dirty bit: %s", err)
	}
}

// processNode merges the delta in a node and has the logic about what to do
// then.
//
// allowReclaim controls whether this call may trigger receiver-side
// reclamation (R4) of a snapshot delta's covered history: it must be true
// only for nodes that arrived from elsewhere and are being merged for the
// first time via the normal DAG walk (dagWorker), and false for locally
// authored nodes (addDAGNode -- a local publish is never a snapshot) and
// for Compact's own processing of the snapshot nodes it just created
// (Compact purges that history itself; reclaiming it again here would be
// redundant and would race Compact's own bookkeeping).
func (store *Datastore) processNode(ctx context.Context, ng *crdtNodeGetter, root Head, delta Delta, node ipld.Node, allowReclaim bool) ([]cid.Cid, error) {
	// First,  merge the delta in this node.
	current := node.Cid()

	// Remove from the set that has the children which are queued for
	// processing, whether we succeed or fail below. If we returned early
	// on failure without doing this, the CID would stay reserved
	// forever: every later broadcast of this branch would see
	// !queuedChildren.Visit(child) and assume someone else owns
	// processing it, stalling the branch until the next repair.
	// Doing this via defer (rather than only after markProcessed
	// succeeds) is safe: between markProcessed and this Remove running,
	// another worker either sees isProcessed=true or sees the
	// reservation still held -- both are correct outcomes.
	defer store.queuedChildren.Remove(current)

	blockKey := dshelp.MultihashToDsKey(current.Hash()).String()
	err := store.set.Merge(ctx, delta, blockKey)
	if err != nil {
		// node was not processed properly, we do not need to
		// mark datastore as dirty, as this may result from
		// custom delta errors that prevent applying this delta.
		return nil, fmt.Errorf("error merging delta from %s: %w", current, err)
	}

	// Record that we have processed the node so that any other worker
	// can skip it.
	err = store.markProcessed(ctx, current)
	if err != nil {
		// marking as dirty here will not help, as we have not made this block a head, so we will not re-traverse it when fixing the datastore.
		return nil, fmt.Errorf("error recording %s as processed: %w", current, err)
	}

	// Some informative logging
	if prio := delta.GetPriority(); prio%50 == 0 {
		store.logger.Infof("merged delta from node %s (priority: %d)", current, prio)
	} else {
		store.logger.Debugf("merged delta from node %s (priority: %d)", current, prio)
	}

	links := node.Links()
	children := []cid.Cid{}

	// We reached the bottom. Our head must become a new head.
	if len(links) == 0 {
		err := store.heads.Add(ctx, root)
		if err != nil {
			return nil, fmt.Errorf("error adding head %s: %w", root, err)
		}
	}

	// Return children that:
	// a) Are not processed
	// b) Are not going to be processed by someone else.
	//
	// For every other child, add our node as Head.

	// Snapshot deltas (see Compact) never get descended into: their links
	// are "covered heads" bookkeeping only, pointing at (possibly
	// already-purged) history. Forcing isProcessed=true for every child
	// below reuses the exact same head-replacement/addition logic as a
	// normal fully-processed child, while guaranteeing we never call
	// queuedChildren.Visit nor append to children for them.
	isSnapshot := delta.IsSnapshot()

	addedAsHead := false // small optimization to avoid adding as head multiple times.
	for _, l := range links {
		child := l.Cid

		oldHead, isHead := store.heads.Get(ctx, child)

		var isProcessed bool
		if isSnapshot {
			isProcessed = true
		} else {
			var err error
			isProcessed, err = store.isProcessed(ctx, child)
			if err != nil {
				return nil, fmt.Errorf("error checking for known block %s: %w", child, err)
			}
		}

		if isHead {
			// reached one of the current heads. Replace it with
			// the tip of this branch
			err := store.heads.Replace(ctx, oldHead, root)
			if err != nil {
				return nil, fmt.Errorf("error replacing head: %s->%s: %w", child, root, err)
			}
			addedAsHead = true

			// If this head was already processed, continue this
			// protects the case when something is a head but was
			// not processed (potentially could happen during
			// first sync when heads are set before processing, a
			// both a node and its child are heads - which I'm not
			// sure if it can happen at all, but good to safeguard
			// for it).
			if isProcessed {
				continue
			}
		}

		// If the child has already been processed or someone else has
		// reserved it for processing, then we can make ourselves a
		// head right away because we are not meant to replace an
		// existing head. Otherwise, mark it for processing and
		// keep going down this branch.
		if isProcessed || !store.queuedChildren.Visit(child) {
			if !addedAsHead {
				err = store.heads.Add(ctx, root)
				if err != nil {
					// Don't let this failure prevent us
					// from processing the other links.
					store.logger.Error(fmt.Errorf("error adding head %s: %w", root, err))
				}
			}
			addedAsHead = true
			continue
		}

		// We can return this child because it is not processed and we
		// reserved it in the queue.
		children = append(children, child)

	}

	if allowReclaim && isSnapshot && store.opts.ReclaimOnSnapshot {
		store.maybeReclaimOnSnapshot(ctx, delta, node)
	}

	return children, nil
}

// RepairDAG is used to walk down the chain until a non-processed node is
// found and at that moment, queues it for processing.
func (store *Datastore) repairDAG(ctx context.Context) error {
	start := time.Now()
	defer func() {
		store.logger.Infof("DAG repair finished. Took %s", time.Since(start).Truncate(time.Second))
	}()

	getter := &crdtNodeGetter{store.dagService}

	heads, _, err := store.heads.List(ctx)
	if err != nil {
		return fmt.Errorf("error listing heads: %w", err)
	}

	type nodeHead struct {
		head Head
		node cid.Cid
	}

	var nodes []nodeHead
	queued := cid.NewSet()
	for _, h := range heads {
		nodes = append(nodes, nodeHead{head: h, node: h.Cid})
		queued.Add(h.Cid)
	}

	// For logging
	var visitedNodes uint64
	var lastPriority uint64
	var queuedNodes uint64

	exitLogging := make(chan struct{})
	defer close(exitLogging)
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		for {
			select {
			case <-exitLogging:
				ticker.Stop()
				return
			case <-ticker.C:
				store.logger.Infof(
					"DAG repair in progress. Visited nodes: %d. Last priority: %d. Queued nodes: %d",
					atomic.LoadUint64(&visitedNodes),
					atomic.LoadUint64(&lastPriority),
					atomic.LoadUint64(&queuedNodes),
				)
			}
		}
	}()

	for {
		// GetDelta does not seem to respond well to context
		// cancellations (probably this goes down to the Blockstore
		// still working with a cancelled context). So we need to put
		// this here.
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if len(nodes) == 0 {
			break
		}
		nh := nodes[0]
		nodes = nodes[1:]
		cur := nh.node
		head := nh.head

		cctx, cancel := context.WithTimeout(ctx, store.opts.DAGSyncerTimeout)
		n, deltaBytes, err := getter.GetDelta(cctx, cur)
		if err != nil {
			cancel()
			return fmt.Errorf("error getting node for reprocessing %s: %w", cur, err)
		}
		cancel()

		delta := store.newDelta()
		err = delta.Unmarshal(deltaBytes)
		if err != nil {
			return err
		}

		isProcessed, err := store.isProcessed(ctx, cur)
		if err != nil {
			return fmt.Errorf("error checking for reprocessed block %s: %w", cur, err)
		}
		if !isProcessed {
			store.logger.Debugf("reprocessing %s / %d", cur, delta.GetPriority())
			// start syncing from here.
			// do not add children to our queue.
			err = store.handleBranch(ctx, head, cur)
			if err != nil {
				return fmt.Errorf("error reprocessing block %s: %w", cur, err)
			}
		}

		// A snapshot node's links are covered-heads bookkeeping only:
		// the history they point at may have been purged by Compact,
		// so trying to fetch it would fail forever. Do not queue them.
		if delta.IsSnapshot() {
			atomic.StoreUint64(&queuedNodes, uint64(len(nodes)))
			atomic.AddUint64(&visitedNodes, 1)
			atomic.StoreUint64(&lastPriority, delta.GetPriority())
			continue
		}

		links := n.Links()
		for _, l := range links {
			if queued.Visit(l.Cid) {
				nodes = append(nodes, (nodeHead{head: head, node: l.Cid}))
			}
		}

		atomic.StoreUint64(&queuedNodes, uint64(len(nodes)))
		atomic.AddUint64(&visitedNodes, 1)
		atomic.StoreUint64(&lastPriority, delta.GetPriority())
	}

	// If we are here we have successfully reprocessed the chain until the
	// bottom.
	store.MarkClean(ctx)
	return nil
}

// Repair triggers a DAG-repair, which tries to re-walk the CRDT-DAG from the
// current heads until the roots, processing currently unprocessed branches.
//
// Calling Repair will walk the full DAG even if the dirty bit is unset, but
// will mark the store as clean unpon successful completion.
func (store *Datastore) Repair(ctx context.Context) error {
	return store.repairDAG(ctx)
}

// Get retrieves the object `value` named by `key`.
// Get will return ErrNotFound if the key is not mapped to a value.
func (store *Datastore) Get(ctx context.Context, key ds.Key) (value []byte, err error) {
	return store.set.Element(ctx, key.String())
}

// Has returns whether the `key` is mapped to a `value`.
// In some contexts, it may be much cheaper only to check for existence of
// a value, rather than retrieving the value itself. (e.g. HTTP HEAD).
// The default implementation is found in `GetBackedHas`.
func (store *Datastore) Has(ctx context.Context, key ds.Key) (exists bool, err error) {
	return store.set.InSet(ctx, key.String())
}

// GetSize returns the size of the `value` named by `key`.
// In some contexts, it may be much cheaper to only get the size of the
// value rather than retrieving the value itself.
func (store *Datastore) GetSize(ctx context.Context, key ds.Key) (size int, err error) {
	return ds.GetBackedSize(ctx, store, key)
}

// Query searches the datastore and returns a query result. This function
// may return before the query actually runs. To wait for the query:
//
//	result, _ := ds.Query(q)
//
//	// use the channel interface; result may come in at different times
//	for entry := range result.Next() { ... }
//
//	// or wait for the query to be completely done
//	entries, _ := result.Rest()
//	for entry := range entries { ... }
func (store *Datastore) Query(ctx context.Context, q query.Query) (query.Results, error) {
	qr, err := store.set.Elements(ctx, q)
	if err != nil {
		return nil, err
	}
	return query.NaiveQueryApply(q, qr), nil
}

// Put stores the object `value` named by `key`.
func (store *Datastore) Put(ctx context.Context, key ds.Key, value []byte) error {
	delta, err := store.set.Add(ctx, key.String(), value)
	if err != nil {
		return err
	}
	_, err = store.publish(ctx, delta)
	return err
}

// Delete removes the value for given `key`.
func (store *Datastore) Delete(ctx context.Context, key ds.Key) error {
	delta, err := store.set.Rmv(ctx, key.String())
	if err != nil {
		return err
	}

	tombs, err := delta.GetTombstones()
	if err != nil {
		return err
	}

	if len(tombs) == 0 {
		return nil
	}
	_, err = store.publish(ctx, delta)
	return err
}

// Sync ensures that all the data under the given prefix is flushed to disk in
// the underlying datastore.
func (store *Datastore) Sync(ctx context.Context, prefix ds.Key) error {
	// This is a quick write up of the internals from the time when
	// I was thinking many underlying datastore entries are affected when
	// an add operation happens:
	//
	// When a key is added:
	// - a new delta is made
	// - Delta is marshalled and a DAG-node is created with the bytes,
	//   pointing to previous heads. DAG-node is added to DAGService.
	// - Heads are replaced with new CID.
	// - New CID is broadcasted to everyone
	// - The new CID is processed (up until now the delta had not
	//   taken effect). Implementation detail: it is processed before
	//   broadcast actually.
	// - processNode() starts processing that branch from that CID
	// - it calls set.Merge()
	// - that calls putElems() and putTombs()
	// - that may make a crdtBatch for all the elems which is later committed
	// - each element has a datastore entry /setNamespace/elemsNamespace/<key>/<block_id>
	// - each tomb has a datastore entry /setNamespace/tombsNamespace/<key>/<block_id>
	// - each value has a datastore entry /setNamespace/keysNamespace/<key>/valueSuffix
	// - each value has an additional priority entry /setNamespace/keysNamespace/<key>/prioritySuffix
	// - the last two are only written if the added entry has more priority than any the existing
	// - For a value to not be lost, those entries should be fully synced.
	// - In order to check if a value is in the set:
	//   - List all elements on /setNamespace/elemsNamespace/<key> (will return several block_ids)
	//   - If we find an element which is not tombstoned, then value is in the set
	// - In order to retrieve an element's value:
	//   - Check that it is in the set
	//   - Read the value entry from the /setNamespace/keysNamespace/<key>/valueSuffix path

	// Be safe and just sync everything in our namespace
	if prefix.String() == "/" {
		return store.store.Sync(ctx, store.namespace)
	}

	// attempt to be intelligent and sync only all heads and the
	// set entries related to the given prefix.
	err := store.set.datastoreSync(ctx, prefix)
	err2 := store.store.Sync(ctx, store.heads.namespace)
	return errors.Join(err, err2)
}

// Close shuts down the CRDT datastore. It should not be used afterwards.
func (store *Datastore) Close() error {
	closeCtx := context.Background()
	store.cancel()
	store.wg.Wait()
	if store.IsDirty(closeCtx) {
		store.logger.Warn("datastore is being closed marked as dirty")
	}
	// Clear the bad-shutdown key last, after all workers have exited, so
	// any dirty marks they still needed to write have had a chance to
	// land. Use a background context — store.ctx was cancelled above.
	if err := store.store.Delete(closeCtx, store.badShutdownKey()); err != nil {
		store.logger.Errorf("error clearing bad-shutdown key: %s", err)
	} else if err := store.store.Sync(closeCtx, store.badShutdownKey()); err != nil {
		store.logger.Errorf("error syncing bad-shutdown key deletion: %s", err)
	}
	return nil
}

// Batch implements batching for writes by accumulating
// Put and Delete in the same CRDT-delta and only applying it and
// broadcasting it on Commit().
func (store *Datastore) Batch(ctx context.Context) (ds.Batch, error) {
	return &crdtBatch{ctx: ctx, store: store}, nil
}

func (store *Datastore) deltaMerge(d1, d2 Delta) (Delta, error) {
	if d1 == nil {
		d1 = store.newDelta()
	}
	if d2 == nil {
		d2 = store.newDelta()
	}

	elems1, err := d1.GetElements()
	if err != nil {
		return nil, err
	}

	elems2, err := d2.GetElements()
	if err != nil {
		return nil, err
	}

	tombs1, err := d1.GetTombstones()
	if err != nil {
		return nil, err
	}

	tombs2, err := d2.GetTombstones()
	if err != nil {
		return nil, err
	}

	result := store.newDelta()
	result.SetElements(append(elems1, elems2...))
	result.SetTombstones(append(tombs1, tombs2...))
	p1 := d1.GetPriority()
	p2 := d2.GetPriority()
	if p2 > p1 {
		result.SetPriority(p2)
	} else {
		result.SetPriority(p1)
	}
	return result, nil
}

// returns delta size and error
func (store *Datastore) addToDelta(ctx context.Context, key string, value []byte) (int, error) {
	delta, err := store.set.Add(ctx, key, value)
	if err != nil {
		return 0, err
	}
	return store.updateDelta(delta)
}

// returns delta size and error
func (store *Datastore) rmvToDelta(ctx context.Context, key string) (int, error) {
	delta, err := store.set.Rmv(ctx, key)
	if err != nil {
		return 0, err
	}

	return store.updateDeltaWithRemove(key, delta)
}

// to satisfy datastore semantics, we need to remove elements from the current
// crdtBatch if they were added.
func (store *Datastore) updateDeltaWithRemove(key string, newDelta Delta) (int, error) {
	store.curDeltaMux.Lock()
	defer store.curDeltaMux.Unlock()

	if store.curDelta == nil {
		store.curDelta = newDelta
		return newDelta.Size(), nil
	}

	// Remove `key` from current elements in the delta.
	elems := make([]*pb.Element, 0)
	curElems, err := store.curDelta.GetElements()
	if err != nil {
		return 0, err
	}

	for _, e := range curElems {
		if e.GetKey() != key {
			elems = append(elems, e)
		}
	}

	curTombs, err := store.curDelta.GetTombstones()
	if err != nil {
		return 0, err
	}

	storeDelta := store.newDelta()
	storeDelta.SetElements(elems)
	storeDelta.SetTombstones(curTombs)
	storeDelta.SetPriority(store.curDelta.GetPriority())
	store.curDelta = storeDelta

	// we have deleted the removed element from Elements(). Now
	// merge normally.
	store.curDelta, err = store.deltaMerge(store.curDelta, newDelta)
	if err != nil {
		return 0, err
	}
	return store.curDelta.Size(), nil
}

func (store *Datastore) updateDelta(newDelta Delta) (int, error) {
	var size int
	var err error
	var merged Delta
	store.curDeltaMux.Lock()
	{
		merged, err = store.deltaMerge(store.curDelta, newDelta)
		if err == nil {
			store.curDelta = merged
			size = merged.Size()
		}
	}
	store.curDeltaMux.Unlock()
	return size, err
}

func (store *Datastore) publishDelta(ctx context.Context) error {
	store.curDeltaMux.Lock()
	defer store.curDeltaMux.Unlock()
	_, err := store.publish(ctx, store.curDelta)
	if err != nil {
		return err
	}
	store.curDelta = nil
	return nil
}

func (store *Datastore) putBlock(ctx context.Context, heads []Head, delta Delta) (ipld.Node, error) {
	node, err := makeNode(delta, heads)
	if err != nil {
		return nil, fmt.Errorf("error creating new block: %w", err)
	}

	cctx, cancel := context.WithTimeout(ctx, store.opts.DAGSyncerTimeout)
	defer cancel()
	err = store.dagService.Add(cctx, node)
	if err != nil {
		return nil, fmt.Errorf("error writing new block %s: %w", node.Cid(), err)
	}

	return node, nil
}

func (store *Datastore) publish(ctx context.Context, delta Delta) (Head, error) {
	// curDelta might be nil if nothing has been added to it
	if delta == nil || delta.Size() == 0 {
		return Head{}, nil
	}

	head, children, err := store.addDAGNode(ctx, delta)
	if err != nil {
		return Head{}, err
	}

	if err := store.broadcast(ctx, head, children); err != nil {
		return Head{}, err
	}
	return head, nil
}

// addDAGNode creates a block with the given delta and returns the new
// head and the heads it replaced.
//
// It holds compactMux for its duration so that local publishes serialize
// against a concurrent Compact() run on the same Datastore (see Compact's
// doc comment).
func (store *Datastore) addDAGNode(ctx context.Context, delta Delta) (Head, []Head, error) {
	store.compactMux.Lock()
	defer store.compactMux.Unlock()

	dagName := delta.GetDagName()
	heads, height, err := store.heads.ListDAG(ctx, dagName)
	if err != nil {
		return Head{}, nil, fmt.Errorf("error listing heads: %w", err)
	}
	height = height + 1 // This implies our minimum height is 1
	delta.SetPriority(height)

	// for _, e := range delta.GetElements() {
	// 	e.Value = append(e.GetValue(), []byte(fmt.Sprintf(" height: %d", height))...)
	// }

	nd, err := store.putBlock(ctx, heads, delta)
	if err != nil {
		return Head{}, nil, err
	}

	newHead := Head{Cid: nd.Cid()}
	newHead.DAGName = dagName
	newHead.Height = height

	// Process new block. This makes that every operation applied
	// to this store take effect (delta is merged) before
	// returning. Since our block references current heads, children
	// should be empty
	store.logger.Debugf("processing generated block %s", nd.Cid())
	children, err := store.processNode(
		ctx,
		&crdtNodeGetter{store.dagService},
		newHead,
		delta,
		nd,
		false, // local publishes are never snapshots: no reclaim.
	)
	if err != nil {
		// store.MarkDirty(ctx) // Keep disabled: Since we are
		// adding a new node that should become head any
		// processing failures are unlikely to be fixed by
		// reprocessing, unlike when processing nodes deep in
		// the DAG.  Additionally, process node may fail due
		// to custom delta errors on GetElement(). Those
		// should just abort merging, and not mark the whole
		// datastore dirty.
		return newHead, nil, fmt.Errorf("error processing new block: %w", err)
	}
	if len(children) != 0 {
		store.logger.Warnf("bug: created a block to unknown children")
	}

	return newHead, heads, nil
}

func (store *Datastore) broadcastHeads(ctx context.Context, heads []Head) error {
	if store.broadcaster == nil { // offline
		return nil
	}

	store.logger.Debugf("broadcasting %s", heads)

	if len(heads) == 0 { // nothing to rebroadcast
		return nil
	}

	bcastBytes, err := store.encodeBroadcast(ctx, heads)
	if err != nil {
		return err
	}

	err = store.broadcaster.Broadcast(ctx, bcastBytes)
	if err != nil {
		return fmt.Errorf("error broadcasting %s: %w", heads, err)
	}
	return nil
}

// broadcast calls broadcastHeads directly or batches the the Head when
// BroadcastBatchDelay is enabled.
func (store *Datastore) broadcast(ctx context.Context, head Head, children []Head) error {
	if store.broadcaster == nil { // offline
		return nil
	}

	if head.Cid == cid.Undef { // nothing to rebroadcast
		return nil
	}

	select {
	case <-ctx.Done():
		store.logger.Debugf("skipping broadcast: %s", ctx.Err())
		return ctx.Err()
	default:
	}

	if store.opts.BroadcastBatchDelay > 0 {
		select {
		case store.broadcastBatchCh <- broadcastBatchHead{head: head, children: children}:
		case <-ctx.Done():
			return ctx.Err()
		default:
			store.logger.Error("broadcastBatch channel is full! Batch broadcasting is too slow for number of heads received.")
		}
		return nil
	}

	return store.broadcastHeads(ctx, []Head{head})
}

type crdtBatch struct {
	ctx   context.Context
	store *Datastore
}

func (b *crdtBatch) Put(ctx context.Context, key ds.Key, value []byte) error {
	size, err := b.store.addToDelta(ctx, key.String(), value)
	if err != nil {
		return err
	}
	if size > b.store.opts.MaxBatchDeltaSize {
		b.store.logger.Warn("delta size over MaxBatchDeltaSize. Commiting.")
		return b.Commit(ctx)
	}
	return nil
}

func (b *crdtBatch) Delete(ctx context.Context, key ds.Key) error {
	size, err := b.store.rmvToDelta(ctx, key.String())
	if err != nil {
		return err
	}
	if size > b.store.opts.MaxBatchDeltaSize {
		b.store.logger.Warn("delta size over MaxBatchDeltaSize. Commiting.")
		return b.Commit(ctx)
	}
	return nil
}

// Commit writes the current delta as a new DAG node and publishes the new
// head. The publish step is skipped if the context is cancelled.
func (b *crdtBatch) Commit(ctx context.Context) error {
	return b.store.publishDelta(ctx)
}

// PrintDAG pretty prints the current Merkle-DAG to stdout in a pretty
// fashion. Only use for small DAGs. DotDAG is an alternative for larger DAGs.
func (store *Datastore) PrintDAG(ctx context.Context) error {
	heads, _, err := store.heads.List(ctx)
	if err != nil {
		return err
	}

	ng := &crdtNodeGetter{NodeGetter: store.dagService}

	set := cid.NewSet()

	for _, h := range heads {
		err := store.printDAGRec(ctx, h.Cid, 0, ng, set)
		if err != nil {
			return err
		}
	}
	return nil
}

func (store *Datastore) printDAGRec(ctx context.Context, from cid.Cid, depth uint64, ng *crdtNodeGetter, set *cid.Set) error {
	line := ""
	for range depth {
		line += " "
	}

	ok := set.Visit(from)
	if !ok {
		line += "..."
		fmt.Println(line)
		return nil
	}

	cctx, cancel := context.WithTimeout(ctx, store.opts.DAGSyncerTimeout)
	defer cancel()
	nd, deltaBytes, err := ng.GetDelta(cctx, from)
	if err != nil {
		// The block may have been purged by Compact: its parent was
		// a snapshot node whose links are covered-heads bookkeeping,
		// not guaranteed-fetchable history. Tolerate this so the
		// debug tools remain usable on compacted DAGs.
		line += fmt.Sprintf("(purged/unavailable: %s)", from)
		fmt.Println(line)
		return nil
	}
	delta := store.newDelta()
	err = delta.Unmarshal(deltaBytes)
	if err != nil {
		return err
	}

	cidStr := nd.Cid().String()
	dagName := delta.GetDagName()
	cidStr = cidStr[len(cidStr)-4:]
	line += fmt.Sprintf("- %d | %s [%s]: ", delta.GetPriority(), cidStr, dagName)
	line += "Add: {"
	elems, err := delta.GetElements()
	if err != nil {
		return err
	}
	for _, e := range elems {
		line += fmt.Sprintf("%s:%s,", e.GetKey(), e.GetValue())
	}

	tombs, err := delta.GetTombstones()
	if err != nil {
		return err
	}
	line += "}. Rmv: {"
	for _, e := range tombs {
		line += fmt.Sprintf("%s,", e.GetKey())
	}
	line += "}. Links: {"
	for _, l := range nd.Links() {
		cidStr := l.Cid.String()
		cidStr = cidStr[len(cidStr)-4:]
		line += fmt.Sprintf("%s,", cidStr)
	}
	line += "}"

	processed, err := store.isProcessed(ctx, nd.Cid())
	if err != nil {
		return err
	}

	if !processed {
		line += " Unprocessed!"
	}

	line += ":"

	fmt.Println(line)
	for _, l := range nd.Links() {
		// nolint:errcheck
		store.printDAGRec(ctx, l.Cid, depth+1, ng, set)
	}
	return nil
}

// DotDAG writes a dot-format representation of the CRDT DAG to the given
// writer. It can be converted to image format and visualized with graphviz
// tooling.
func (store *Datastore) DotDAG(ctx context.Context, w io.Writer) error {
	heads, _, err := store.heads.List(ctx)
	if err != nil {
		return err
	}

	// nolint:errcheck
	fmt.Fprintln(w, "digraph CRDTDAG {")

	ng := &crdtNodeGetter{NodeGetter: store.dagService}

	set := cid.NewSet()

	// nolint:errcheck
	fmt.Fprintln(w, "subgraph heads {")
	for _, h := range heads {
		// nolint:errcheck
		fmt.Fprintln(w, h)
	}
	// nolint:errcheck
	fmt.Fprintln(w, "}")

	for _, h := range heads {
		err := store.dotDAGRec(ctx, w, h.Cid, 0, ng, set)
		if err != nil {
			return err
		}
	}
	// nolint:errcheck
	fmt.Fprintln(w, "}")
	return nil
}

func (store *Datastore) dotDAGRec(ctx context.Context, w io.Writer, from cid.Cid, depth uint64, ng *crdtNodeGetter, set *cid.Set) error {
	cidLong := from.String()
	cidShort := cidLong[len(cidLong)-4:]

	ok := set.Visit(from)
	if !ok {
		return nil
	}

	cctx, cancel := context.WithTimeout(ctx, store.opts.DAGSyncerTimeout)
	defer cancel()
	nd, deltaBytes, err := ng.GetDelta(cctx, from)
	if err != nil {
		// See printDAGRec: tolerate blocks purged by Compact.
		// nolint:errcheck
		fmt.Fprintf(w, "%s [label=\"(purged/unavailable)\"]\n", cidLong)
		return nil
	}

	delta := store.newDelta()
	err = delta.Unmarshal(deltaBytes)
	if err != nil {
		return err
	}

	elems, _ := delta.GetElements()
	tombs, _ := delta.GetTombstones()

	// nolint:errcheck
	fmt.Fprintf(w, "%s [label=\"%d | %s: +%d -%d\"]\n",
		cidLong,
		delta.GetPriority(),
		cidShort,
		len(elems),
		len(tombs),
	)
	// nolint:errcheck
	fmt.Fprintf(w, "%s -> {", cidLong)
	for _, l := range nd.Links() {
		// nolint:errcheck
		fmt.Fprintf(w, "%s ", l.Cid)
	}
	// nolint:errcheck
	fmt.Fprintln(w, "}")
	// nolint:errcheck
	fmt.Fprintf(w, "subgraph sg_%s {\n", cidLong)
	for _, l := range nd.Links() {
		// nolint:errcheck
		fmt.Fprintln(w, l.Cid)
	}
	// nolint:errcheck
	fmt.Fprintln(w, "}")

	for _, l := range nd.Links() {
		if err = store.dotDAGRec(ctx, w, l.Cid, depth+1, ng, set); err != nil {
			return err
		}
	}
	return nil
}

// Stats wraps internal information about the datastore.
// Might be expanded in the future.
type Stats struct {
	Heads      []Head
	MaxHeight  uint64
	QueuedJobs int
}

// InternalStats returns internal datastore information like the current heads
// and max height.
func (store *Datastore) InternalStats(ctx context.Context) Stats {
	heads, height, _ := store.heads.List(ctx)

	return Stats{
		Heads:      heads,
		MaxHeight:  height,
		QueuedJobs: len(store.jobQueue), // capacity: numWorkers
	}
}

func (store *Datastore) newDelta() Delta {
	return store.opts.crdtOpts.DeltaFactory()
}

func (store *Datastore) getPriority(ctx context.Context, ng *crdtNodeGetter, c cid.Cid) (uint64, error) {
	_, deltaBytes, err := ng.GetDelta(ctx, c)
	if err != nil {
		return 0, err
	}
	delta := store.newDelta()
	err = delta.Unmarshal(deltaBytes)
	if err != nil {
		return 0, err
	}

	return delta.GetPriority(), nil
}

type cidSafeSet struct {
	set map[cid.Cid]struct{}
	mux sync.RWMutex
}

func newCidSafeSet() *cidSafeSet {
	return &cidSafeSet{
		set: make(map[cid.Cid]struct{}),
	}
}

func (s *cidSafeSet) Visit(c cid.Cid) bool {
	var b bool
	s.mux.Lock()
	{
		if _, ok := s.set[c]; !ok {
			s.set[c] = struct{}{}
			b = true
		}
	}
	s.mux.Unlock()
	return b
}

func (s *cidSafeSet) Remove(c cid.Cid) {
	s.mux.Lock()
	{
		delete(s.set, c)
	}
	s.mux.Unlock()
}

func (s *cidSafeSet) Has(c cid.Cid) (ok bool) {
	s.mux.RLock()
	{
		_, ok = s.set[c]
	}
	s.mux.RUnlock()
	return
}
