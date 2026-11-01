// Package kvdb is a replicated go-datastore: an add-wins observed-remove set of
// key/value elements replicated as a Merkle-DAG of deltas (Merkle-CRDTs,
// Sanjuán et al.).
//
// Each delta carries a dot, the dots it links and its author's previous
// counter, and every replica keeps a causal context of the dots it merged.
// That context lets a replica fold its state into a snapshot and drop the
// history below it, tombstones included, without coordination: an element
// whose dot a snapshot has seen and does not hold is removed everywhere.
// Storage is bounded by the live state plus one vector entry per writing
// incarnation.
package kvdb

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	mrand "math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/taubyte/tau/pkg/kvdb/pb"

	blocks "github.com/ipfs/go-block-format"
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

const (
	headsNs           = "h"
	setNs             = "s"
	processedBlocksNs = "b" // local blocks and their dots
	missingNs         = "m" // parents linked by a merged block, not yet covered
	contextNs         = "c"
	retryNs           = "r" // heads heard, not yet merged
	dirtyBitKey       = "d"
	badShutdownKey    = "bs" // set on New, cleared on a clean Close
	versionKey        = "crdt_version"
)

// Stored as formatVersion+1, as every earlier format was. A store of any other
// version is emptied on open and refills from its peers.
const formatVersion uint64 = 9

var (
	ErrNoMoreBroadcast = errors.New("receiving blocks aborted since no new blocks will be broadcasted")
	ErrClosed          = errors.New("crdt datastore is closed")
)

type Broadcaster interface {
	Broadcast(context.Context, []byte) error
	Next(context.Context) ([]byte, error)
}

type SessionDAGService interface {
	ipld.DAGService
	Session(context.Context) ipld.NodeGetter
}

type Options struct {
	Logger logging.StandardLogger
	// RebroadcastInterval is how often heads not already broadcast by others are
	// republished. Default: 1m.
	RebroadcastInterval time.Duration
	// PutHook is called when a key's value appears or changes, locally or
	// remotely; rewriting the same bytes is no change.
	PutHook func(k ds.Key, v []byte)
	// DeleteHook is called when a key goes from present to absent. A key keeping a
	// concurrent version is still present (PutHook is called if that becomes its
	// value), and a removal merged before the value it removes is no change. Hooks
	// run after the merge commits, at most one per key per merge.
	DeleteHook func(k ds.Key)
	NumWorkers int
	// DAGSyncerTimeout bounds a DAG fetch; 0 disables. Default: 5m.
	DAGSyncerTimeout time.Duration
	// MaxBatchDeltaSize commits a batch whose delta grows past it, and sizes a
	// snapshot's chunks. Default: 1MiB.
	MaxBatchDeltaSize int
	// RepairInterval is how often parents a walk failed to fetch are retried; 0
	// disables. Keep it well under DeliveryBound: only repair refetches them.
	// Default: 1m.
	RepairInterval time.Duration
	// BroadcastBatchDelay batches head broadcasts into one message; 0 disables.
	BroadcastBatchDelay time.Duration
	// CompactAfterBlocks compacts after a local write once the store holds this
	// many blocks and at least as many as live elements, so rewriting the state
	// costs O(1) per write amortized. 0 disables. Default: 128.
	CompactAfterBlocks int
	// ReplicaID prefixes every incarnation id, normally the node's peer id.
	// Required with a broadcaster: without it a store is a new peer at every
	// restart, which every judge waits G for.
	ReplicaID []byte

	// Retention is T: how long what a delta teaches is kept, and so how long a
	// replica can be away and still rejoin. Each delta carries its author's T, so
	// replicas need not agree on it (see SetRetention). It must be many times the
	// Margin. Default: 30 days.
	Retention time.Duration
	// DeliveryBound is D: a delta from a replica in contact reaches every replica
	// in contact within it, walk and merge included. At least DAGSyncerTimeout
	// plus RebroadcastInterval. Default: 10m.
	DeliveryBound time.Duration
	// ClockSkew is S, how far apart correct clocks may be; a block stamped further
	// ahead waits for the clock. Default: 1m.
	ClockSkew time.Duration
	// Margin is m: how long after a judge stops vouching every replica in contact
	// still holds what may be needed. At least 3D + 2S + 2*RebroadcastInterval; 0
	// means exactly that.
	Margin time.Duration
	Clock  func() time.Time
	// Uptime is time since boot, suspend included. It times this process's
	// intervals, never anything compared across replicas.
	Uptime func() time.Duration

	// manual leaves maintenance, heartbeats and repair to the caller.
	manual bool
	// treeCache is the tree-node cache budget in bytes; 0 for treeCacheBytes.
	treeCache int
}

const retentionFactor = 10

// minMargin covers a judge answering on the last heartbeat it heard, the
// answer's delivery, the walk after it, and the skew both ways.
func (opts *Options) minMargin() time.Duration {
	return 3*opts.DeliveryBound + 2*opts.ClockSkew + 2*opts.RebroadcastInterval
}

func (opts *Options) margin() time.Duration {
	if opts.Margin == 0 {
		return opts.minMargin()
	}
	return opts.Margin
}

func (opts *Options) checkRetention(t time.Duration) error {
	if t < retentionFactor*opts.margin() {
		return fmt.Errorf("retention %s is under %d margins of %s", t, retentionFactor, opts.margin())
	}
	return nil
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
	if opts.CompactAfterBlocks < 0 {
		return errors.New("invalid CompactAfterBlocks")
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
	if opts.Clock == nil || opts.Uptime == nil {
		return errors.New("the clocks are undefined")
	}
	if opts.DeliveryBound < opts.DAGSyncerTimeout+opts.RebroadcastInterval {
		return fmt.Errorf("delivery bound %s is under DAGSyncerTimeout plus RebroadcastInterval", opts.DeliveryBound)
	}
	if opts.ClockSkew < 0 {
		return errors.New("invalid ClockSkew")
	}
	if opts.Margin != 0 && opts.Margin < opts.minMargin() {
		return fmt.Errorf("margin %s is under %s", opts.Margin, opts.minMargin())
	}
	return opts.checkRetention(opts.Retention)
}

func DefaultOptions() *Options {
	return &Options{
		Logger:              logging.Logger("crdt"),
		RebroadcastInterval: time.Minute,
		NumWorkers:          5,
		DAGSyncerTimeout:    5 * time.Minute,
		MaxBatchDeltaSize:   1 * 1024 * 1024,
		RepairInterval:      time.Minute,
		CompactAfterBlocks:  128,
		Retention:           30 * 24 * time.Hour,
		DeliveryBound:       10 * time.Minute,
		ClockSkew:           time.Minute,
		Clock:               time.Now,
		Uptime:              bootClock,
	}
}

type Datastore struct {
	ctx    context.Context
	cancel context.CancelFunc

	opts   *Options
	logger logging.StandardLogger

	// store is raw behind the journal, which marks every state record written
	// through it.
	store   ds.Datastore
	raw     ds.Datastore
	journal *journal
	// logged is set when raw keeps a prefix of its writes and the DAG writes its
	// blocks to raw, so no Sync is needed to order them (see sharesLog).
	logged    bool
	namespace ds.Key
	pre       prefixes
	set       *set
	heads     *heads
	// base is the state's trees as of the journal's last emptying. Guarded by mu.
	base stateBase
	// gcMu serializes changes of the roots, the sessions and what they hold, and
	// removal of what nothing reaches (see gc.go). It is taken after mu.
	gcMu sync.Mutex
	gc   collector
	// fetches are the state fetches in progress, by state root. fetchMu is taken
	// alone.
	fetchMu sync.Mutex
	fetches map[cid.Cid]*stateFetch

	trees *treeCache

	dagService       ipld.DAGService
	broadcaster      Broadcaster
	broadcastBatchCh chan broadcastBatchHead

	seenHeadsMux sync.RWMutex
	seenHeads    map[cid.Cid]struct{}

	// mu serializes merges, local writes and compactions. The context is replaced,
	// never changed in place, so a covers check loads it without the lock.
	mu         sync.Mutex
	cc         *causalContext
	ccRead     atomic.Pointer[causalContext]
	ccMu       sync.Mutex
	ccChanged  chan struct{}
	replica    string
	counter    uint64
	closed     bool
	liveCount  int
	blockCount int
	// purging holds the blocks a purge took and drops without mu; dropping counts
	// them until they are gone.
	purging  *cidSafeSet
	dropping sync.WaitGroup

	// ttl is the retention this replica stamps, in milliseconds.
	ttl   atomic.Uint64
	times times
	// since is the fleet time last heard and sinceUp the uptime then; horizon is
	// the latest authored time of anything forgotten. Guarded by mu.
	since   uint64
	sinceUp time.Duration
	horizon uint64
	// erased holds, per incarnation a rebase replaced, the dots it wrote and no
	// longer held (see dropAllButOwn), until they expire. Guarded by mu.
	erased    map[string]*known
	lastWrite time.Duration
	// rejoining is set while waiting to be judged after a gap (see rejoin.go).
	rejoining bool
	gapFrom   uint64
	// lastHeard is the uptime a non-rejoining peer was last heard, from open;
	// liveHeard the same, 0 until one is heard; answered is when each rejoining
	// incarnation was last answered.
	lastHeard time.Duration
	liveHeard time.Duration
	// anyHeard and fleetHeard are when a peer whose clock agrees, and such a peer
	// that has heard a fleet, was last heard; 0 before the first.
	anyHeard   time.Duration
	fleetHeard time.Duration
	answered   map[string]time.Duration
	// merged is when each incarnation was last covered further; heartbeats
	// acknowledge those to their authors.
	merged map[string]time.Duration
	// contacts are the peers heard without a break of G, frontiers what
	// each live incarnation last said it holds, listed the peers frontiers
	// name that this replica has not heard, contactFrom when this
	// replica began hearing the fleet without a break, grounded whether it
	// knows it has heard every live peer, settledSince the fleet time it
	// last held all they held, forgotten the entries it dropped by expiry
	// lately, oks the verdicts that vouched for it while it rejoins, sent
	// how many broadcasts it built, turned the sent of the frontier it last
	// came to lack what a live peer holds in, lagged whether it lacked it
	// in its last frontier, and fell whether it has heard one since that it
	// does not hold from a peer whose last one it held (see settle.go).
	contacts     map[string]*contact
	frontiers    map[string]*frontier
	listed       map[string]*listing
	contactFrom  time.Duration
	grounded     bool
	settledSince uint64
	forgotten    map[string]*forgottenEntry
	oks          map[string]*ok
	sent         uint64
	turned       uint64
	lagged, fell bool
	// msg is reused for each broadcast with its heads and dots; each is
	// marshalled before store.mu is released.
	msg      pb.CRDTBroadcast
	msgHeads []pb.Head
	msgDots  []pb.Dot
	msgReps  []string
	msgAcks  []pb.Entry
	// ccWaited is set once someone waits on ccChanged.
	ccWaited bool
	// spareMerge is a finished delta merge kept for reuse; build is buildState's
	// storage, and parents the heads a write's block links.
	spareMerge *merging
	parents    []Head
	build      stateBuild
	redo       redoScratch
	compactDue bool
	// lows are lower bounds that let frequent passes skip empty scans: the
	// earliest pending-removal expiry, indexed-block half-life (at + ttl/2) and
	// kept own-operation expiry. Insertions lower them; a scan sets them, none
	// meaning unbounded and 0 unknown.
	lows lows
	// pendingAt is at or above the latest stamp of a pending removal held, none
	// until a scan finds it.
	pendingAt uint64
	// generation changes when a rebase replaces the state; a walk begun under an
	// older one merges nothing. rebuilding is the generation whose rebase still
	// walks the judge's state, 0 for none.
	generation uint64
	rebuilding uint64

	wg sync.WaitGroup

	jobQueue chan *dagJob
	sendJobs chan *dagJob
	// queuedChildren has each child fetched by one job only.
	queuedChildren *cidSafeSet
}

type dagJob struct {
	ctx        context.Context
	session    *sync.WaitGroup
	nodeGetter *crdtNodeGetter
	delta      *pb.Delta
	node       ipld.Node
	gen        uint64
}

type broadcastBatchHead struct {
	head     Head
	children []Head
}

// Close the Datastore before the given store.
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
	if bcast != nil && len(opts.ReplicaID) == 0 {
		return nil, errors.New("a store with a broadcaster needs a ReplicaID")
	}

	ctx, cancel := context.WithCancel(context.Background())
	dstore := &Datastore{
		ctx:              ctx,
		cancel:           cancel,
		opts:             opts,
		logger:           opts.Logger,
		store:            store,
		namespace:        namespace,
		pre:              newPrefixes(namespace),
		dagService:       dagSyncer,
		trees:            newTreeCache(opts),
		broadcaster:      bcast,
		broadcastBatchCh: make(chan broadcastBatchHead, 32000),
		seenHeads:        make(map[cid.Cid]struct{}),
		jobQueue:         make(chan *dagJob, opts.NumWorkers),
		sendJobs:         make(chan *dagJob),
		queuedChildren:   newCidSafeSet(),
		purging:          newCidSafeSet(),
		ccChanged:        make(chan struct{}),
		answered:         map[string]time.Duration{},
		merged:           map[string]time.Duration{},
		contacts:         map[string]*contact{},
		listed:           map[string]*listing{},
		frontiers:        map[string]*frontier{},
		forgotten:        map[string]*forgottenEntry{},
		oks:              map[string]*ok{},
		erased:           map[string]*known{},
		pendingAt:        none,
		gc:               newCollector(dagSyncer),
		fetches:          map[cid.Cid]*stateFetch{},
	}
	dstore.ttl.Store(durationMillis(opts.Retention))
	dstore.times = timesOf(opts)
	dstore.set = newSet(store, namespace.ChildString(setNs))
	dstore.set.logger = opts.Logger
	dstore.set.putHook = func(k string, v []byte) {
		if opts.PutHook != nil {
			// A merged value aliases the block it came in.
			opts.PutHook(ds.NewKey(k), slices.Clone(v))
		}
	}
	dstore.set.deleteHook = func(k string) {
		if opts.DeleteHook != nil {
			opts.DeleteHook(ds.NewKey(k))
		}
	}
	dstore.raw = store
	dstore.logged = sharesLog(store, dagSyncer)
	dstore.journal = newJournal(store, namespace, dstore.set)
	dstore.store = &journaled{Datastore: store, j: dstore.journal}
	dstore.set.store = dstore.store

	if err := dstore.open(ctx); err != nil {
		cancel()
		return nil, err
	}

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
	dstore.wg.Add(6)
	go func() {
		defer dstore.wg.Done()
		if !dstore.opts.manual {
			dstore.maintenance(ctx)
		}
	}()
	go func() {
		defer dstore.wg.Done()
		dstore.handleNext(ctx)
	}()
	go func() {
		defer dstore.wg.Done()
		if !dstore.opts.manual {
			dstore.rebroadcast(ctx)
		}
	}()
	go func() {
		defer dstore.wg.Done()
		dstore.broadcastBatchWorker(ctx)
	}()
	go func() {
		defer dstore.wg.Done()
		if !dstore.opts.manual {
			dstore.repair(ctx)
		}
	}()
	go func() {
		defer dstore.wg.Done()
		dstore.logStats(ctx)
	}()

	return dstore, nil
}

// open brings the store to the current format, loads its state and takes an
// incarnation: the one it closed under earlier in this process, or a new one.
func (store *Datastore) open(ctx context.Context) error {
	if err := store.checkFormat(ctx); err != nil {
		return err
	}
	if err := store.replay(ctx); err != nil {
		return err
	}

	var err error
	if store.heads, err = newHeads(ctx, store.store, store.namespace.ChildString(headsNs)); err != nil {
		return fmt.Errorf("error building heads: %w", err)
	}
	if store.cc, err = store.loadContext(ctx); err != nil {
		return fmt.Errorf("error loading the causal context: %w", err)
	}
	if store.base, err = store.loadBase(ctx); err != nil {
		return fmt.Errorf("error loading the state base: %w", err)
	}
	if store.erased, err = store.loadErased(ctx); err != nil {
		return fmt.Errorf("error loading the erased dots: %w", err)
	}
	if store.since, err = readMillis(ctx, store.store, store.sinceKey()); err != nil {
		return err
	}
	if store.horizon, err = readMillis(ctx, store.store, store.horizonKey()); err != nil {
		return err
	}
	if store.settledSince, err = readMillis(ctx, store.store, store.settledKey()); err != nil {
		return err
	}
	store.sinceUp = store.times.up()
	store.ccRead.Store(store.cc)
	if err := store.openGC(ctx); err != nil {
		return fmt.Errorf("error loading what the state's roots reach: %w", err)
	}
	if err := store.dropOrphans(ctx); err != nil {
		return fmt.Errorf("error dropping unmerged blocks: %w", err)
	}
	if _, err := store.reclaim(ctx); err != nil {
		return fmt.Errorf("error removing unreached blocks: %w", err)
	}
	if store.liveCount, err = countPrefix(ctx, store.store, store.set.elemsPrefix()); err != nil {
		return err
	}
	if store.blockCount, err = countPrefix(ctx, store.store, store.blocksPrefix()); err != nil {
		return err
	}

	// The bad-shutdown key at startup means the last run did not Close: mark dirty
	// so repair retries what a walk left missing, and sync now so a crash before
	// the next flush is not missed.
	hadBadShutdown, err := store.store.Has(ctx, store.badShutdownKey())
	if err != nil {
		return fmt.Errorf("error checking bad-shutdown key: %w", err)
	}
	if hadBadShutdown {
		store.logger.Warn("previous shutdown was not clean; marking datastore as dirty to trigger repair")
		store.MarkDirty(ctx)
	}
	if err := store.store.Put(ctx, store.badShutdownKey(), nil); err != nil {
		return fmt.Errorf("error writing bad-shutdown key: %w", err)
	}
	if err := store.store.Sync(ctx, store.badShutdownKey()); err != nil {
		return fmt.Errorf("error syncing bad-shutdown key: %w", err)
	}

	// Last, so that an open that fails never holds an incarnation.
	store.lastWrite = store.times.up()
	store.lastHeard = store.lastWrite
	if err := store.loadRejoin(ctx); err != nil {
		return fmt.Errorf("error loading the rejoin state: %w", err)
	}
	if inc, ok := incarnations.resume(store.incarnationKey(), store.cc.counter); ok {
		store.replica, store.counter = inc.replica, inc.counter
	} else if err := store.drawIncarnation(); err != nil {
		return err
	}
	// A rebase that crashed before writing its own operations again writes
	// them now.
	if err := store.reissue(ctx); err != nil {
		return err
	}

	hs, maxHeight := store.heads.List()
	store.logger.Infof("crdt Datastore created. Number of heads: %d. Current max-height: %d. Dirty: %t", len(hs), maxHeight, store.IsDirty(ctx))
	return nil
}

// SetRetention changes T for what is written from now on; what was written
// keeps its T, so lowering it forgets nothing promised.
func (store *Datastore) SetRetention(t time.Duration) error {
	if err := store.opts.checkRetention(t); err != nil {
		return err
	}
	store.ttl.Store(durationMillis(t))
	return nil
}

func (store *Datastore) retention() uint64 { return store.ttl.Load() }

func (store *Datastore) clock() uint64 { return millis(store.opts.Clock()) }

// stampNow is the later of the clock and the fleet time last heard, so a clock
// behind the fleet does not write what is already old. The caller holds
// store.mu.
func (store *Datastore) stampNow() uint64 { return max(store.clock(), store.since) }

func (store *Datastore) incarnationKey() incarnationKey {
	return incarnationKey{replicaID: string(store.opts.ReplicaID), namespace: store.namespace.String()}
}

func (store *Datastore) versionKey() ds.Key {
	return store.namespace.ChildString(versionKey)
}

// checkFormat empties a store of another format; there is no migration, the
// replica fetches its copy again. A store with no version is new.
func (store *Datastore) checkFormat(ctx context.Context) error {
	data, err := store.store.Get(ctx, store.versionKey())
	switch {
	case errors.Is(err, ds.ErrNotFound):
	case err != nil:
		return err
	default:
		if v, n := binary.Uvarint(data); n > 0 && v == formatVersion+1 {
			return nil
		}
		store.logger.Warnf("crdt store at %s is in another format; emptying it", store.namespace)
		t := newTxn(store.raw)
		if err := scanStore(ctx, store.raw, store.namespace, func(k string, _ []byte) error {
			t.del(ds.RawKey(k))
			return nil
		}); err != nil {
			return err
		}
		if err := t.commit(ctx); err != nil {
			return err
		}
	}
	return store.store.Put(ctx, store.versionKey(), binary.AppendUvarint(nil, formatVersion+1))
}

func countPrefix(ctx context.Context, store ds.Datastore, prefix ds.Key) (int, error) {
	n := 0
	err := scanBytes(ctx, store, prefix, func(_, _ []byte) error {
		n++
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

var errFound = errors.New("found")

func anyUnder(ctx context.Context, store ds.Datastore, prefix ds.Key) (bool, error) {
	err := scanBytes(ctx, store, prefix, func(_, _ []byte) error { return errFound })
	if errors.Is(err, errFound) {
		return true, nil
	}
	return false, err
}

func (store *Datastore) covered(d dot) bool {
	return store.ccRead.Load().covers(d)
}

func (store *Datastore) setContext(c *causalContext) {
	store.cc = c
	store.ccRead.Store(c)
	store.ccMu.Lock()
	if store.ccWaited {
		close(store.ccChanged)
		store.ccChanged, store.ccWaited = make(chan struct{}), false
	}
	store.ccMu.Unlock()
}

// contextChanged is closed the next time the context grows.
func (store *Datastore) contextChanged() <-chan struct{} {
	store.ccMu.Lock()
	defer store.ccMu.Unlock()
	store.ccWaited = true
	return store.ccChanged
}

// cancelWhenCovered cancels a fetch once every dot it fetches is covered: a
// block a snapshot covers may be held by nobody, and the fetch would wait to
// time out.
func (store *Datastore) cancelWhenCovered(ctx context.Context, cancel context.CancelFunc, dots []dot) {
	for {
		changed := store.contextChanged()
		covered := true
		for _, d := range dots {
			if d.isZero() || !store.covered(d) {
				covered = false
				break
			}
		}
		if covered {
			cancel()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
	}
}

func (store *Datastore) handleNext(ctx context.Context) {
	if store.broadcaster == nil {
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

		msg, err := store.decodeBroadcast(data)
		if err != nil {
			store.logger.Error(err)
			continue
		}
		store.handleBroadcast(ctx, msg)
	}
}

func (store *Datastore) adoptHeads(ctx context.Context, received []Head) {
	dg := &crdtNodeGetter{NodeGetter: store.dagService}
	for _, head := range received {
		cctx, cancel := context.WithTimeout(ctx, store.opts.DAGSyncerTimeout)
		_, delta, err := dg.GetDelta(cctx, head.Cid)
		cancel()
		if err != nil {
			store.logger.Error(err)
			continue
		}
		head.Height = delta.GetPriority()
		head.Dot = dotFromPB(delta.GetDot())
		head.At, head.TTL = delta.GetAt(), delta.GetTtl()
		// A walk that merged it meanwhile made it a head, or a parent.
		if store.covered(head.Dot) {
			continue
		}
		if err := store.heads.add(ctx, head); err != nil {
			store.logger.Error(err)
		}
	}
}

func randomizeInterval(d time.Duration) time.Duration {
	leeway := (d * 30 / 100)
	randGen := mrand.New(mrand.NewSource(time.Now().UnixNano()))
	randomInterval := time.Duration(randGen.Int63n(int64(leeway*2))) - leeway
	return d + randomInterval
}

func (store *Datastore) rebroadcast(ctx context.Context) {
	timer := time.NewTimer(randomizeInterval(store.opts.RebroadcastInterval))
	for {
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
			store.heartbeat(ctx)
			timer.Reset(randomizeInterval(store.opts.RebroadcastInterval))
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
			kept := heads[:0]
			for _, h := range heads {
				replaced := false
				for _, child := range batchHead.children {
					if h.Cid.Equals(child.Cid) {
						replaced = true
						break
					}
				}
				if !replaced {
					kept = append(kept, h)
				}
			}
			heads = append(kept, batchHead.head)
		case <-t.C:
			if err := store.broadcastHeads(ctx, heads); err != nil {
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
	timer := time.NewTimer(0)
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
				if err := store.repairDAG(ctx); err != nil {
					store.logger.Error(err)
				}
			}
			timer.Reset(store.opts.RepairInterval)
		}
	}
}

// rebroadcastHeads sends the heads nobody else broadcast during the last
// interval.
func (store *Datastore) rebroadcastHeads(ctx context.Context) {
	heads, _ := store.heads.List()

	store.seenHeadsMux.Lock()
	headsToBroadcast := make([]Head, 0, len(heads))
	for _, h := range heads {
		if _, ok := store.seenHeads[h.Cid]; !ok {
			headsToBroadcast = append(headsToBroadcast, h)
		}
	}
	clear(store.seenHeads)
	store.seenHeadsMux.Unlock()

	if err := store.send(ctx, headsToBroadcast, true); err != nil {
		store.logger.Warnf("broadcast failed: %v", err)
	}
}

func (store *Datastore) logStats(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	for {
		select {
		case <-ticker.C:
			heads, height := store.heads.List()
			store.logger.Infof(
				"Number of heads: %d. Current max height: %d. Queued jobs: %d. Dirty: %t",
				len(heads), height, len(store.jobQueue), store.IsDirty(ctx),
			)
		case <-ctx.Done():
			ticker.Stop()
			return
		}
	}
}

// isIndexed is false for a block a purge is dropping.
func (store *Datastore) isIndexed(ctx context.Context, c cid.Cid) (bool, error) {
	if store.purging.Has(c) {
		return false, nil
	}
	return store.store.Has(ctx, store.blockKey(c))
}

func (store *Datastore) handleBlock(ctx context.Context, h Head) error {
	return store.handleBlockAt(ctx, store.currentGeneration(), h)
}

func (store *Datastore) currentGeneration() uint64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.generation
}

func (store *Datastore) handleBlockAt(ctx context.Context, gen uint64, h Head) error {
	if !h.Dot.isZero() && store.covered(h.Dot) {
		return nil
	}
	if store.gone(h.link(), store.dropClockNow()) || store.ahead(h.At) {
		return nil
	}
	held, err := store.isIndexed(ctx, h.Cid)
	if err != nil {
		return fmt.Errorf("error checking for known block %s: %w", h.Cid, err)
	}
	if held {
		return nil
	}
	err = store.handleBranchAt(ctx, gen, []link{h.link()})
	if err != nil {
		store.retryLater(ctx, h.link())
	}
	return err
}

// A head heard once may not be heard again, so a failed walk is recorded for
// repair.
func (store *Datastore) retryLater(ctx context.Context, l link) {
	if !l.dot.isZero() {
		if err := store.store.Put(ctx, store.retryKey(l.dot), encodeLink(l)); err != nil {
			store.logger.Warnf("error recording block %s to retry: %s", l.cid, err)
		}
	}
	store.MarkDirty(ctx)
}

func (store *Datastore) retryPrefix() ds.Key { return store.namespace.ChildString(retryNs) }

func (store *Datastore) retryKey(d dot) ds.Key { return dotKey(store.pre.retry, d) }

// A block that cannot be fetched marks the store dirty for repair. A walk is
// bounded by D as a whole: one that takes longer is left to repair, so nothing
// fetched late is merged on a judgement made when it began.
func (store *Datastore) handleBranch(ctx context.Context, from []link) error {
	return store.handleBranchAt(ctx, store.currentGeneration(), from)
}

func (store *Datastore) handleBranchAt(ctx context.Context, gen uint64, from []link) error {
	cctx, cancel := context.WithTimeout(ctx, store.opts.DeliveryBound)
	defer cancel()

	dg := &crdtNodeGetter{NodeGetter: store.dagService}
	if sessionMaker, ok := store.dagService.(SessionDAGService); ok {
		dg = &crdtNodeGetter{NodeGetter: sessionMaker.Session(cctx)}
	}

	var session sync.WaitGroup
	err := store.sendNewJobs(cctx, gen, &session, dg, from)
	session.Wait()
	return err
}

func (store *Datastore) dagWorker() {
	for job := range store.jobQueue {
		ctx := job.ctx
		select {
		case <-ctx.Done():
			store.queuedChildren.Remove(job.node.Cid())
			job.session.Done()
			continue
		default:
		}

		children, err := store.processNode(ctx, job.gen, job.nodeGetter, job.delta, job.node)
		if err != nil {
			store.logger.Error(err)
			// A block from the future is heard again when its author
			// rebroadcasts; recording it would let a clock far ahead fill
			// other replicas' stores.
			if !errors.Is(err, errAhead) {
				store.retryLater(ctx, linkOf(job.node.Cid(), job.delta))
			}
			job.session.Done()
			continue
		}
		go func(j *dagJob) {
			if err := store.sendNewJobs(ctx, j.gen, j.session, j.nodeGetter, children); err != nil {
				store.logger.Error(err)
				store.MarkDirty(ctx)
			}
			j.session.Done()
		}(job)
	}
}

// sendNewJobs skips blocks covered by now or fetched by another job, and
// blocks until every job is queued.
func (store *Datastore) sendNewJobs(ctx context.Context, gen uint64, session *sync.WaitGroup, ng *crdtNodeGetter, links []link) error {
	var fetch []cid.Cid
	dots := map[cid.Cid]dot{}
	now := store.dropClockNow()
	for _, l := range links {
		if !l.dot.isZero() && store.covered(l.dot) || store.gone(l, now) {
			continue
		}
		if !store.queuedChildren.Visit(l.cid) {
			continue
		}
		fetch = append(fetch, l.cid)
		dots[l.cid] = l.dot
	}
	if len(fetch) == 0 {
		return nil
	}

	// What is fetched is marked first: a crash before its merge indexes it
	// leaves it to reclaim, not held by nothing for ever.
	if err := store.stage(ctx, fetch, true); err != nil {
		store.unstage(fetch)
		for _, c := range fetch {
			store.queuedChildren.Remove(c)
		}
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, store.opts.DAGSyncerTimeout)
	defer cancel()
	waiting := make([]dot, 0, len(fetch))
	for _, c := range fetch {
		waiting = append(waiting, dots[c])
	}
	go store.cancelWhenCovered(cctx, cancel, waiting)

	queued := make(map[cid.Cid]struct{})
	var errs []error
	for deltaOpt := range ng.GetDeltas(cctx, fetch) {
		if deltaOpt.err != nil {
			errs = append(errs, fmt.Errorf("error getting delta: %w", deltaOpt.err))
			continue
		}
		queued[deltaOpt.node.Cid()] = struct{}{}

		session.Add(1)
		job := &dagJob{
			ctx:        ctx,
			session:    session,
			nodeGetter: ng,
			delta:      deltaOpt.delta,
			node:       deltaOpt.node,
			gen:        gen,
		}
		select {
		case store.sendJobs <- job:
		case <-ctx.Done():
			// The job was never sent, so it cannot complete; nor can the
			// fetches not queued yet. Release them all for the next walk.
			session.Done()
			for _, c := range fetch {
				if _, ok := queued[c]; !ok || c == job.node.Cid() {
					store.queuedChildren.Remove(c)
					store.unstage([]cid.Cid{c})
				}
			}
			return errors.Join(append(errs, ctx.Err())...)
		}
	}

	// A block that could not be fetched is released for the next walk.
	// It is not a failure if this replica has come to cover it meanwhile,
	// for instance by merging a snapshot from another branch.
	stillMissing := 0
	for _, c := range fetch {
		if _, ok := queued[c]; ok {
			continue
		}
		store.queuedChildren.Remove(c)
		store.unstage([]cid.Cid{c})
		if d := dots[c]; d.isZero() || !store.covered(d) {
			stillMissing++
		}
	}
	if stillMissing == 0 {
		return nil
	}
	return errors.Join(append(errs, fmt.Errorf("%d blocks could not be fetched", stillMissing))...)
}

// sendJobWorker is the jobQueue's only sender, so workers shut down without
// races.
func (store *Datastore) sendJobWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			if len(store.sendJobs) > 0 {
				store.MarkDirty(ctx)
			}
			close(store.jobQueue)
			return
		case j := <-store.sendJobs:
			store.jobQueue <- j
		}
	}
}

func (store *Datastore) dirtyKey() ds.Key {
	return store.namespace.ChildString(dirtyBitKey)
}

func (store *Datastore) badShutdownKey() ds.Key {
	return store.namespace.ChildString(badShutdownKey)
}

func (store *Datastore) MarkDirty(ctx context.Context) {
	store.logger.Warn("marking datastore as dirty")
	if err := store.store.Put(ctx, store.dirtyKey(), nil); err != nil {
		store.logger.Errorf("error setting dirty bit: %s", err)
	}
}

func (store *Datastore) IsDirty(ctx context.Context) bool {
	ok, err := store.store.Has(ctx, store.dirtyKey())
	if err != nil {
		store.logger.Errorf("error checking dirty bit: %s", err)
	}
	return ok
}

func (store *Datastore) MarkClean(ctx context.Context) {
	store.logger.Info("marking datastore as clean")
	if err := store.store.Delete(ctx, store.dirtyKey()); err != nil {
		store.logger.Errorf("error clearing dirty bit: %s", err)
	}
}

func (store *Datastore) processNode(ctx context.Context, gen uint64, ng *crdtNodeGetter, delta *pb.Delta, node ipld.Node) ([]link, error) {
	current := node.Cid()
	// Release the reservation whether or not the merge succeeds, or a
	// failed block would stay reserved and stall every later walk of it.
	defer store.queuedChildren.Remove(current)
	defer store.unstage([]cid.Cid{current})

	// A block stamped ahead of this replica's clock by more than the skew
	// waits for the clock: a clock far ahead delays only its owner's writes.
	if store.ahead(delta.GetAt()) {
		store.dropFailed(ctx, current)
		return nil, fmt.Errorf("block %s: %w", current, errAhead)
	}

	// The state fetch is left before reclaim runs: once nothing is left to
	// merge it for, what it staged goes with that reclaim.
	var fetch *stateFetch
	var held *heldDAG
	settled := false
	leave := func() {
		if fetch != nil {
			store.leaveFetch(ctx, fetch, settled)
			fetch = nil
		}
	}
	defer leave()
	if delta.GetSnapshot() {
		var err error
		held = newHeldDAG(store.dagService)
		fetch, err = store.fetchState(ctx, ng, current, delta, held)
		if err != nil {
			store.dropFailed(ctx, current)
			return nil, fmt.Errorf("error fetching snapshot %s: %w", current, err)
		}
		ahead, err := store.carriesAhead(ctx, delta, held)
		if err != nil || ahead {
			store.dropFailed(ctx, current)
			if err == nil {
				err = errAhead
			}
			return nil, fmt.Errorf("snapshot %s: %w", current, err)
		}
	}

	// What a snapshot lets this replica purge goes once the lock is
	// released.
	var taken []cid.Cid
	store.mu.Lock()
	defer func() {
		store.mu.Unlock()
		if _, err := store.dropTaken(ctx, taken); err != nil {
			store.logger.Warnf("error purging what snapshot %s covers: %s", current, err)
		}
		leave()
		if delta.GetSnapshot() {
			if _, err := store.reclaim(ctx); err != nil {
				store.logger.Warnf("error removing unreached state blocks: %s", err)
			}
		}
	}()

	// What has expired goes before the merge reads the context, whether or
	// not maintenance has run since: an entry kept past its expiry would
	// refuse what every replica that forgot it admits.
	if _, err := store.expireLocked(ctx); err != nil {
		store.dropFailed(ctx, current)
		return nil, err
	}
	d := dotFromPB(delta.GetDot())
	if store.cc.covers(d) || gen != store.generation {
		settled = store.cc.covers(d)
		return nil, store.dropUnindexed(ctx, current)
	}
	// A block a purge has taken merges once the purge is done: the copy
	// fetched may be the one it is dropping.
	if store.purging.Has(current) {
		store.dropFailed(ctx, current)
		return nil, fmt.Errorf("block %s: %w", current, errTaken)
	}
	if !delta.GetSnapshot() {
		for _, e := range delta.GetElements() {
			if e.GetDot() != nil {
				return nil, fmt.Errorf("block %s is a snapshot chunk, not a delta", current)
			}
		}
		if store.expired(delta.GetAt()+delta.GetTtl(), store.dropClock()) {
			// Too old to be merged: what it added that is still live is
			// in a snapshot by now, and what it removed may already be
			// forgotten.
			return nil, store.unmerged(ctx, current, d, delta.GetAt())
		}
		walk, err := store.mergeDelta(ctx, current, delta)
		if err != nil {
			store.dropFailed(ctx, current)
			return nil, fmt.Errorf("error merging delta from %s: %w", current, err)
		}
		store.afterMerge(ctx)
		return walk, nil
	}
	if d.replica != store.replica {
		since, err := store.snapshotSince(ctx, delta, held)
		if err != nil {
			store.dropFailed(ctx, current)
			return nil, err
		}
		stale, err := store.stale(ctx, since)
		if err != nil {
			store.dropFailed(ctx, current)
			return nil, err
		}
		if stale {
			// Its author had not heard the fleet since before something
			// this replica can no longer refute: what it holds may have
			// been removed, and it is never merged.
			store.logger.Warnf("refusing snapshot %s: written by a replica that had heard nothing since %d", current, since)
			settled = true
			return nil, store.unmerged(ctx, current, d, 0)
		}
	}
	walk, theirs, err := store.mergeSnapshot(ctx, current, delta, held, fetch.b.sess)
	if err != nil {
		store.dropFailed(ctx, current)
		return nil, fmt.Errorf("error merging snapshot %s: %w", current, err)
	}
	settled = true
	if taken, err = store.purge(ctx, theirs); err != nil {
		store.logger.Warnf("error purging what snapshot %s covers: %s", current, err)
	}
	if theirs != nil && d.replica != store.replica {
		if err := store.reached(ctx, theirs); err != nil {
			store.logger.Warnf("error forgetting what snapshot %s shows reached the fleet: %s", current, err)
		}
	}
	store.afterMerge(ctx)
	return walk, nil
}

// afterMerge completes a rejoin held open until this replica holds what a
// judge vouched with. The caller holds store.mu.
func (store *Datastore) afterMerge(ctx context.Context) {
	if err := store.endRejoinIfHeld(ctx); err != nil {
		store.logger.Errorf("ending the rejoin: %s", err)
	}
}

// unmerged settles a block that will never be merged: nothing waits for it,
// its fetched block goes, and the horizon takes forgot. The caller holds
// store.mu.
func (store *Datastore) unmerged(ctx context.Context, c cid.Cid, d dot, forgot uint64) error {
	m := store.beginMerge()
	defer m.t.endReads()
	m.horizon = max(m.horizon, forgot)
	m.t.del(store.missingKey(d))
	m.t.del(store.retryKey(d))
	if err := m.commit(ctx); err != nil {
		return err
	}
	return store.dropUnindexed(ctx, c)
}

// dropUnindexed removes a block fetched for a covered dot and never indexed:
// no purge would find it. What a snapshot's state brought goes when its fetch
// ends.
func (store *Datastore) dropUnindexed(ctx context.Context, c cid.Cid) error {
	held, err := store.isIndexed(ctx, c)
	if err != nil || held {
		return err
	}
	return store.removeBlocks(ctx, []cid.Cid{c})
}

// A failed merge's block is not indexed, so if a snapshot covers it before the
// retry nothing else would remove it.
func (store *Datastore) dropFailed(ctx context.Context, c cid.Cid) {
	if err := store.dropUnindexed(ctx, c); err != nil {
		store.logger.Warnf("error dropping the blocks of a failed merge: %s", err)
	}
}

// repairDAG retries every uncovered parent a merged block links and every head
// adopted without merging.
func (store *Datastore) repairDAG(ctx context.Context) error {
	start := time.Now()
	defer func() {
		store.logger.Infof("DAG repair finished. Took %s", time.Since(start).Truncate(time.Second))
	}()

	// Clean first, so a failure recorded while this repair runs keeps its
	// mark for the next one.
	store.MarkClean(ctx)
	now := store.dropClockNow()
	var todo []link
	var done []ds.Key
	for _, prefix := range []ds.Key{store.missingPrefix(), store.retryPrefix()} {
		err := scanStore(ctx, store.store, prefix, func(k string, v []byte) error {
			d, err := parseDotSegment(k[len(prefix.String())+1:])
			if err != nil {
				return err
			}
			l, err := decodeLink(d, v)
			if err != nil {
				return err
			}
			// Covered by now, or expired with nobody holding it: nothing
			// to fetch any more.
			if store.covered(d) || store.gone(l, now) {
				done = append(done, ds.RawKey(k))
				return nil
			}
			todo = append(todo, l)
			return nil
		})
		if err != nil {
			return err
		}
	}
	t := newTxn(store.store)
	for _, k := range done {
		t.del(k)
	}
	if err := t.commit(ctx); err != nil {
		return err
	}
	hs, _ := store.heads.List()
	for _, h := range hs {
		held, err := store.isIndexed(ctx, h.Cid)
		if err != nil {
			return err
		}
		if !held {
			todo = append(todo, h.link())
		}
	}

	if err := store.handleBranch(ctx, todo); err != nil {
		store.MarkDirty(ctx)
		return fmt.Errorf("error repairing: %w", err)
	}
	if store.IsDirty(ctx) {
		return errors.New("repair left parents missing")
	}
	return nil
}

// Repair marks the store clean once nothing is missing.
func (store *Datastore) Repair(ctx context.Context) error {
	return store.repairDAG(ctx)
}

func (store *Datastore) Get(ctx context.Context, key ds.Key) (value []byte, err error) {
	return store.set.Element(ctx, key.String())
}

func (store *Datastore) Has(ctx context.Context, key ds.Key) (exists bool, err error) {
	return store.set.InSet(ctx, key.String())
}

func (store *Datastore) GetSize(ctx context.Context, key ds.Key) (size int, err error) {
	return ds.GetBackedSize(ctx, store, key)
}

func (store *Datastore) Query(ctx context.Context, q query.Query) (query.Results, error) {
	qr, err := store.set.Elements(ctx, q)
	if err != nil {
		return nil, err
	}
	return query.NaiveQueryApply(q, qr), nil
}

func (store *Datastore) Put(ctx context.Context, key ds.Key, value []byte) error {
	return store.write(ctx, []op{{key: key.String(), value: value, put: true}})
}

func (store *Datastore) Delete(ctx context.Context, key ds.Key) error {
	return store.write(ctx, []op{{key: key.String()}})
}

type op struct {
	key   string
	value []byte
	put   bool
}

// Each op removes every version of its key held here, and a put then adds the
// new one, so a key's live versions are only ever concurrent ones.
func (store *Datastore) write(ctx context.Context, ops []op) error {
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return ErrClosed
	}
	if err := store.idleRetire(); err != nil {
		store.mu.Unlock()
		return err
	}
	delta := store.stamped(pb.DeltaFromVTPool())
	defer releaseDelta(delta)
	t := newTxn(store.store)
	for _, o := range ops {
		live, err := store.set.liveElems(ctx, t, o.key)
		if err != nil {
			t.release()
			store.mu.Unlock()
			return err
		}
		for _, e := range live {
			tombstoneFor(delta, e, grow(&delta.Tombstones, pb.ElementFromVTPool))
		}
		if o.put {
			el := grow(&delta.Elements, pb.ElementFromVTPool)
			el.Key, el.Value = bytesOf(o.key), o.value
		}
	}
	t.release()
	if len(delta.Elements)+len(delta.Tombstones) == 0 {
		store.mu.Unlock()
		return nil
	}
	head, replaced, err := store.publishLocked(ctx, delta)
	if store.opts.BroadcastBatchDelay > 0 {
		// A batched broadcast keeps them.
		replaced = slices.Clone(replaced)
	}
	store.mu.Unlock()
	if err != nil {
		return err
	}
	if err := store.broadcast(ctx, head, replaced); err != nil {
		return err
	}
	store.maybeCompact(ctx)
	return nil
}

// idleRetire draws a new incarnation for a write after more than half the
// retention idle, so it never writes under one its peers may have forgotten.
// The caller holds store.mu.
func (store *Datastore) idleRetire() error {
	idle := store.times.up() - store.lastWrite
	if store.counter == 0 || durationMillis(idle) <= store.retention()/2 {
		return nil
	}
	return store.drawIncarnation()
}

const nonceLen = 16

// Without a ReplicaID nothing tells one store's incarnations from another's,
// so an incarnation is its own peer.
func peerOf(incarnation string) string {
	if len(incarnation) <= nonceLen {
		return incarnation
	}
	return incarnation[:len(incarnation)-nonceLen]
}

// The caller holds store.mu or is opening.
func (store *Datastore) drawIncarnation() error {
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("error drawing an incarnation: %w", err)
	}
	store.replica = string(append(append([]byte{}, store.opts.ReplicaID...), nonce...))
	store.counter = 0
	store.lastWrite = store.times.up()
	return nil
}

func (store *Datastore) nextDot() (dot, uint64) {
	return dot{replica: store.replica, counter: store.counter + 1}, store.counter
}

// The returned heads last until store.mu is released. The counter advances
// only once the merge commits, so a failed write leaves no gap. The caller
// holds store.mu.
func (store *Datastore) publishLocked(ctx context.Context, delta *pb.Delta) (Head, []Head, error) {
	d, prev := store.nextDot()
	hs, height := store.heads.appendList(store.parents[:0])
	store.parents = hs
	delta.Dot = d.pooledPB()
	delta.Prev = prev
	delta.Priority = height + 1
	delta.Parents = delta.Parents[:0]
	for _, h := range hs {
		p := grow(&delta.Parents, pb.ParentFromVTPool)
		p.Cid, p.Dot, p.At, p.Ttl = bytesOf(h.Cid.KeyString()), h.Dot.pooledPB(), h.At, h.TTL
	}

	nd, err := makeNode(delta, hs)
	if err != nil {
		return Head{}, nil, fmt.Errorf("error creating new block: %w", err)
	}
	if err := store.writeBlocks(ctx, block{kind: kindDelta, dot: d, at: delta.At, ttl: delta.Ttl}, nd); err != nil {
		return Head{}, nil, err
	}
	if _, err := store.mergeDelta(ctx, nd.Cid(), delta); err != nil {
		store.discardBlocks(ctx, nd.Cid())
		return Head{}, nil, fmt.Errorf("error processing new block: %w", err)
	}
	store.counter = d.counter
	store.lastWrite = store.times.up()
	return Head{Cid: nd.Cid(), Height: delta.Priority, Dot: d, At: delta.At, TTL: delta.Ttl}, hs, nil
}

func parentsOf(hs []Head) []*pb.Parent {
	out := make([]*pb.Parent, 0, len(hs))
	for _, h := range hs {
		out = append(out, &pb.Parent{Cid: h.Cid.Bytes(), Dot: h.Dot.toPB(), At: h.At, Ttl: h.TTL})
	}
	return out
}

func linkOf(c cid.Cid, delta *pb.Delta) link {
	return link{cid: c, dot: dotFromPB(delta.GetDot()), at: delta.GetAt(), ttl: delta.GetTtl()}
}

func (store *Datastore) stamped(delta *pb.Delta) *pb.Delta {
	delta.At = store.stampNow()
	delta.Ttl = store.retention()
	delta.Since = store.broadcastSince()
	delta.Horizon = store.horizon
	return delta
}

// A tombstone is needed as long as its delta, and never less than its element.
func tombstoneFor(delta *pb.Delta, e element, tomb *pb.Element) {
	ttl := delta.Ttl
	if exp := e.expires(); exp > delta.At+ttl {
		ttl = exp - delta.At
	}
	tomb.Key, tomb.Dot, tomb.At, tomb.Ttl = bytesOf(e.key), e.dot.pooledPB(), delta.At, ttl
}

// grow reuses a message a pooled delta kept in its capacity, or takes one from
// get.
func grow[T any](s *[]*T, get func() *T) *T {
	n := len(*s)
	if n < cap(*s) {
		*s = (*s)[:n+1]
		if m := (*s)[n]; m != nil {
			return m
		}
	} else {
		*s = append(*s, nil)
	}
	m := get()
	(*s)[n] = m
	return m
}

// The pooled messages borrow their bytes, so they are cleared first and no
// later use writes into them. Pooled messages are only filled and marshalled,
// never unmarshalled into.
func releaseDelta(delta *pb.Delta) {
	for _, els := range [][]*pb.Element{delta.Elements, delta.Tombstones} {
		for _, e := range els {
			e.Key, e.Value = nil, nil
			if e.Dot != nil {
				e.Dot.Replica = nil
			}
		}
	}
	for _, p := range delta.Parents {
		p.Cid = nil
		if p.Dot != nil {
			p.Dot.Replica = nil
		}
	}
	if delta.Dot != nil {
		delta.Dot.Replica = nil
	}
	delta.ReturnToVTPool()
}

func (store *Datastore) maybeCompact(ctx context.Context) {
	if store.opts.CompactAfterBlocks == 0 {
		return
	}
	store.mu.Lock()
	due := store.blockCount >= store.opts.CompactAfterBlocks && store.blockCount >= store.liveCount
	store.mu.Unlock()
	if !due {
		return
	}
	if _, err := store.Compact(ctx); err != nil {
		store.logger.Errorf("compacting: %s", err)
	}
}

// A value cannot be read back without the elements, context and heads, so
// prefix does not narrow the sync.
func (store *Datastore) Sync(ctx context.Context, prefix ds.Key) error {
	return store.store.Sync(ctx, store.namespace)
}

// A write after Close fails with ErrClosed.
func (store *Datastore) Close() error {
	closeCtx := context.Background()
	store.cancel()
	store.wg.Wait()
	store.mu.Lock()
	if !store.closed {
		store.closed = true
		incarnations.release(store.incarnationKey(), incarnation{replica: store.replica, counter: store.counter})
	}
	store.mu.Unlock()
	store.dropping.Wait()
	if store.IsDirty(closeCtx) {
		store.logger.Warn("datastore is being closed marked as dirty")
	}
	// Clear the bad-shutdown key last, after all workers have exited, so
	// any dirty marks they still needed to write have had a chance to
	// land.
	if err := store.store.Delete(closeCtx, store.badShutdownKey()); err != nil {
		store.logger.Errorf("error clearing bad-shutdown key: %s", err)
	} else if err := store.store.Sync(closeCtx, store.badShutdownKey()); err != nil {
		store.logger.Errorf("error syncing bad-shutdown key deletion: %s", err)
	}
	return nil
}

// The batch publishes one delta on Commit; the last operation on a key wins.
func (store *Datastore) Batch(ctx context.Context) (ds.Batch, error) {
	return &crdtBatch{store: store, index: map[string]int{}}, nil
}

type crdtBatch struct {
	store *Datastore
	mu    sync.Mutex
	ops   []op
	index map[string]int
	size  int
}

func (b *crdtBatch) add(ctx context.Context, o op) error {
	b.mu.Lock()
	if i, ok := b.index[o.key]; ok {
		b.ops[i] = o
	} else {
		b.index[o.key] = len(b.ops)
		b.ops = append(b.ops, o)
	}
	b.size += len(o.key) + len(o.value)
	over := b.size > b.store.opts.MaxBatchDeltaSize
	b.mu.Unlock()
	if over {
		b.store.logger.Warn("delta size over MaxBatchDeltaSize. Commiting.")
		return b.Commit(ctx)
	}
	return nil
}

func (b *crdtBatch) Put(ctx context.Context, key ds.Key, value []byte) error {
	return b.add(ctx, op{key: key.String(), value: value, put: true})
}

func (b *crdtBatch) Delete(ctx context.Context, key ds.Key) error {
	return b.add(ctx, op{key: key.String()})
}

func (b *crdtBatch) Commit(ctx context.Context) error {
	b.mu.Lock()
	ops := b.ops
	b.ops, b.index, b.size = nil, map[string]int{}, 0
	b.mu.Unlock()
	return b.store.write(ctx, ops)
}

// A BlockWriter stores a block and tells the exchange it is held, without the
// block service's per-block context and span or its check first.
// peer.DAGService is one.
type BlockWriter interface {
	WriteBlock(ctx context.Context, b blocks.Block) error
}

// writeBlocks indexes blocks this replica authors before writing them. A crash
// before the covering merge leaves index entries whose dots are not covered,
// which the next open drops with their blocks: never a block nothing indexes,
// nor a merged entry naming a lost block.
func (store *Datastore) writeBlocks(ctx context.Context, b block, nodes ...ipld.Node) error {
	t := newTxn(store.store)
	for _, nd := range nodes {
		t.put(t.key(appendHashKey(t.buf(), store.pre.blocks, nd.Cid())), t.keep(appendBlock(t.buf(), b)))
	}
	err := t.commit(ctx)
	t.release()
	if err == nil && !store.logged {
		err = store.raw.Sync(ctx, store.blocksPrefix())
	}
	if err != nil {
		return err
	}
	// Adding a block waits on no peer, so no timeout bounds it.
	for _, nd := range nodes {
		if err = store.addBlock(ctx, nd); err != nil {
			break
		}
	}
	if err != nil {
		cids := make([]cid.Cid, len(nodes))
		for i, nd := range nodes {
			cids[i] = nd.Cid()
		}
		store.discardBlocks(ctx, cids...)
		return fmt.Errorf("error writing new blocks: %w", err)
	}
	return nil
}

// A BlockRemover removes blocks without the block service's per-block context
// and span. peer.DAGService is one.
type BlockRemover interface {
	DeleteBlocks(ctx context.Context, cids []cid.Cid) error
}

func (store *Datastore) removeBlocks(ctx context.Context, cids []cid.Cid) error {
	if r, ok := store.dagService.(BlockRemover); ok {
		return r.DeleteBlocks(ctx, cids)
	}
	return store.dagService.RemoveMany(ctx, cids)
}

func (store *Datastore) addBlock(ctx context.Context, nd ipld.Node) error {
	if w, ok := store.dagService.(BlockWriter); ok {
		return w.WriteBlock(ctx, nd)
	}
	return store.dagService.Add(ctx, nd)
}

func (store *Datastore) addBlocks(ctx context.Context, nodes []ipld.Node) error {
	w, direct := store.dagService.(BlockWriter)
	if !direct {
		return store.dagService.AddMany(ctx, nodes)
	}
	for _, nd := range nodes {
		if err := w.WriteBlock(ctx, nd); err != nil {
			return err
		}
	}
	return nil
}

// A failure leaves them to the next open.
func (store *Datastore) discardBlocks(ctx context.Context, cids ...cid.Cid) {
	if err := store.removeBlocks(ctx, cids); err != nil {
		store.logger.Warnf("error removing unmerged blocks: %s", err)
		return
	}
	if err := store.syncBlocks(ctx); err != nil {
		store.logger.Warnf("error removing unmerged blocks: %s", err)
		return
	}
	t := newTxn(store.store)
	for _, c := range cids {
		t.del(store.blockKey(c))
	}
	if err := t.commit(ctx); err != nil {
		store.logger.Warnf("error unindexing unmerged blocks: %s", err)
	}
}

// Every merged block's dot is covered, so an index entry whose dot is not was
// left by a crash between writing a block and merging it.
func (store *Datastore) dropOrphans(ctx context.Context) error {
	var orphans []cid.Cid
	prefix := store.blocksPrefix()
	err := scanStore(ctx, store.store, prefix, func(k string, v []byte) error {
		b, err := decodeBlock(v)
		if err != nil {
			return err
		}
		if store.cc.covers(b.dot) {
			return nil
		}
		c, err := cidOfBlockKey(prefix, k)
		if err != nil {
			return err
		}
		orphans = append(orphans, c)
		return nil
	})
	if err != nil || len(orphans) == 0 {
		return err
	}
	store.logger.Warnf("dropping %d blocks written and never merged", len(orphans))
	if err := store.removeBlocks(ctx, orphans); err != nil {
		return err
	}
	if err := store.syncBlocks(ctx); err != nil {
		return err
	}
	return store.unindex(ctx, orphans)
}

func (store *Datastore) broadcastHeads(ctx context.Context, heads []Head) error {
	if len(heads) == 0 {
		return nil
	}
	return store.send(ctx, heads, false)
}

func (store *Datastore) send(ctx context.Context, heads []Head, beat bool) error {
	if store.broadcaster == nil {
		return nil
	}
	bcastBytes, err := store.encodeBroadcast(ctx, heads, beat)
	if err != nil {
		return err
	}
	if err := store.broadcaster.Broadcast(ctx, bcastBytes); err != nil {
		return fmt.Errorf("error broadcasting %d heads: %w", len(heads), err)
	}
	return nil
}

func (store *Datastore) broadcast(ctx context.Context, head Head, children []Head) error {
	if store.broadcaster == nil {
		return nil
	}
	if head.Cid == cid.Undef {
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

func (store *Datastore) PrintDAG(ctx context.Context) error {
	return store.DotDAG(ctx, nopWriter{printer: true})
}

type nopWriter struct{ printer bool }

func (w nopWriter) Write(p []byte) (int, error) {
	if w.printer {
		fmt.Print(string(p))
	}
	return len(p), nil
}

func (store *Datastore) DotDAG(ctx context.Context, w io.Writer) error {
	heads, _ := store.heads.List()
	if _, err := fmt.Fprintln(w, "digraph CRDTDAG {"); err != nil {
		return err
	}
	ng := &crdtNodeGetter{NodeGetter: store.dagService}
	seen := cid.NewSet()
	stack := make([]cid.Cid, 0, len(heads))
	for _, h := range heads {
		stack = append(stack, h.Cid)
	}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !seen.Visit(c) {
			continue
		}
		held, err := store.isIndexed(ctx, c)
		if err != nil {
			return err
		}
		if !held {
			if _, err := fmt.Fprintf(w, "%s [label=\"(not held)\"]\n", c); err != nil {
				return err
			}
			continue
		}
		nd, delta, err := ng.GetDelta(ctx, c)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "%s [label=\"%d | %s | snapshot=%t: +%d -%d\"]\n", c, delta.GetPriority(), dotFromPB(delta.GetDot()), delta.GetSnapshot(), len(delta.GetElements()), len(delta.GetTombstones())); err != nil {
			return err
		}
		links, err := blockLinks(nd, nil)
		if err != nil {
			return err
		}
		for _, l := range links {
			if _, err := fmt.Fprintf(w, "%s -> %s\n", c, l); err != nil {
				return err
			}
			stack = append(stack, l)
		}
	}
	_, err := fmt.Fprintln(w, "}")
	return err
}

type Stats struct {
	Heads      []Head
	MaxHeight  uint64
	QueuedJobs int
}

func (store *Datastore) InternalStats(ctx context.Context) Stats {
	heads, height := store.heads.List()
	return Stats{
		Heads:      heads,
		MaxHeight:  height,
		QueuedJobs: len(store.jobQueue),
	}
}

type cidSafeSet struct {
	set map[cid.Cid]struct{}
	mux sync.RWMutex
}

func newCidSafeSet() *cidSafeSet {
	return &cidSafeSet{set: make(map[cid.Cid]struct{})}
}

func (s *cidSafeSet) Visit(c cid.Cid) bool {
	s.mux.Lock()
	defer s.mux.Unlock()
	if _, ok := s.set[c]; ok {
		return false
	}
	s.set[c] = struct{}{}
	return true
}

func (s *cidSafeSet) Remove(c cid.Cid) {
	s.mux.Lock()
	defer s.mux.Unlock()
	delete(s.set, c)
}

func (s *cidSafeSet) Has(c cid.Cid) bool {
	s.mux.RLock()
	defer s.mux.RUnlock()
	_, ok := s.set[c]
	return ok
}

func (s *cidSafeSet) Len() int {
	s.mux.RLock()
	defer s.mux.RUnlock()
	return len(s.set)
}
