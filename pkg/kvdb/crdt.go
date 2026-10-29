// Package kvdb is a replicated go-datastore: an add-wins observed-remove set
// of key/value elements, replicated as a Merkle-DAG of deltas (Sanjuán,
// Pöyhtäri, Teixeira, "Merkle-CRDTs: Merkle-DAGs meet CRDTs").
//
// Every delta carries a dot naming it (see dot), the dots of the blocks it
// links, and its author's previous counter; every replica keeps a causal
// context of the dots it has merged. That context is what lets a replica fold
// its whole state into a snapshot (Compact) and drop the history below it,
// tombstones included, without coordination: a snapshot says which elements
// are live and which dots it has seen, and an element whose dot it has seen
// and does not hold is removed, on every replica, in every order.
//
// Storage under any put/delete churn is bounded by the live state plus one
// vector entry per writing incarnation.
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
	setNs             = "s"  // set
	processedBlocksNs = "b"  // local blocks and their dots
	missingNs         = "m"  // parents linked by a merged block and not yet covered
	contextNs         = "c"  // the causal context
	retryNs           = "r"  // heads heard and not merged yet
	dirtyBitKey       = "d"  // dirty
	badShutdownKey    = "bs" // bad-shutdown: set on New, cleared on clean Close
	versionKey        = "crdt_version"
)

// formatVersion is the on-disk format, stored as formatVersion+1 the way every
// earlier format stored its own. A store holding any other version is emptied
// on open and refills from its peers.
const formatVersion uint64 = 6

// Common errors.
var (
	ErrNoMoreBroadcast = errors.New("receiving blocks aborted since no new blocks will be broadcasted")
	ErrClosed          = errors.New("crdt datastore is closed")
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
	// PutHook is called when a key's value, as Get reads it, appears or
	// changes, by a local or a remote update, with the new value. A write
	// of the bytes the key already holds is no change. Default: nil.
	PutHook func(k ds.Key, v []byte)
	// DeleteHook is called when a key goes from present to absent, by a
	// local or a remote update. A key that keeps a concurrent version is
	// still present: if that version is now its value, PutHook is called
	// instead. A removal merged before the value it removes is no change
	// either, since this replica never shows that value. Default: nil.
	//
	// Each hook reports what one merge did to one key, after the merge
	// commits: an overwrite calls PutHook only, and no merge calls both for
	// one key.
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
	// delta size gets too big, and is the size of a snapshot's chunks.
	// Default: 1MiB.
	MaxBatchDeltaSize int
	// RepairInterval specifies how often to retry the parents a walk
	// could not fetch, when the store is marked dirty. 0 to disable. It
	// should be well under DeliveryBound: a delta is promised to every
	// replica in contact within D, and a parent whose fetch failed is
	// fetched again only by repair. Default: 1m.
	RepairInterval time.Duration
	// BroadcastBatchDelay batches the new Head broadcasts when
	// publishing new deltas into a single message. 0 to
	// disable. Default: 0.
	BroadcastBatchDelay time.Duration
	// CompactAfterBlocks makes a local write compact the store once it
	// holds at least this many blocks and at least as many blocks as live
	// elements, so rewriting the live state costs O(1) per write
	// amortized. 0 disables. Default: 128.
	CompactAfterBlocks int
	// ReplicaID prefixes every incarnation id this store draws, normally
	// the node's peer id. Required with a broadcaster: peers tell one
	// store's incarnations from another's by it, and without it a store is
	// a new peer at every restart, which every judge waits G for. Default:
	// none.
	ReplicaID []byte

	// Retention is T: how long what a delta teaches, a removal above all,
	// is kept after it was written, and so how long a replica can be away
	// and still rejoin with what it holds. Every delta carries its
	// author's T, so a change of T applies to what is written from then on
	// and replicas need not agree on it at any one moment (see
	// SetRetention). It must be many times the Margin. Default: 30 days.
	Retention time.Duration
	// DeliveryBound is D: a delta written by a replica in contact reaches
	// every replica in contact within it, walk and merge included. It is
	// at least DAGSyncerTimeout plus RebroadcastInterval. Default: 10m.
	DeliveryBound time.Duration
	// ClockSkew is S, how far apart the clocks of correct replicas may be.
	// A block stamped further ahead of this replica's clock waits until
	// the clock reaches it. Default: 1m.
	ClockSkew time.Duration
	// Margin is m, how long after a judge stops vouching for what may
	// still need something every replica in contact still holds it. At
	// least 3D + 2S + 2*RebroadcastInterval; 0 means exactly that.
	Margin time.Duration
	// Clock is the wall clock every stamp, expiry and judgement reads.
	// Default: time.Now.
	Clock func() time.Time
	// Uptime is the time since this machine booted, suspend included. It
	// times this process's own intervals, never anything compared across
	// replicas. Default: the boot clock.
	Uptime func() time.Duration

	// manual leaves what time drives, maintenance, heartbeats and repair,
	// to the caller: tests drive them step by step.
	manual bool
}

// retentionFactor is how many margins T must be at least: "many times".
const retentionFactor = 10

// minMargin is the least margin the proofs allow for D, S and the
// rebroadcast interval: a judge that answers on the last heartbeat it heard,
// the answer's delivery, the walk that follows, and the skew both ways.
func (opts *Options) minMargin() time.Duration {
	return 3*opts.DeliveryBound + 2*opts.ClockSkew + 2*opts.RebroadcastInterval
}

func (opts *Options) margin() time.Duration {
	if opts.Margin == 0 {
		return opts.minMargin()
	}
	return opts.Margin
}

// checkRetention refuses a T that is not many times the margin.
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

// DefaultOptions initializes an Options object with sensible defaults.
func DefaultOptions() *Options {
	return &Options{
		Logger:              logging.Logger("crdt"),
		RebroadcastInterval: time.Minute,
		NumWorkers:          5,
		DAGSyncerTimeout:    5 * time.Minute,
		MaxBatchDeltaSize:   1 * 1024 * 1024, // 1MB,
		RepairInterval:      time.Minute,
		CompactAfterBlocks:  128,
		Retention:           30 * 24 * time.Hour,
		DeliveryBound:       10 * time.Minute,
		ClockSkew:           time.Minute,
		Clock:               time.Now,
		Uptime:              bootClock,
	}
}

// Datastore makes a go-datastore a distributed Key-Value store using
// Merkle-CRDTs and IPLD.
type Datastore struct {
	ctx    context.Context
	cancel context.CancelFunc

	opts   *Options
	logger logging.StandardLogger

	// store is raw behind the journal, which marks every state record
	// written through it.
	store     ds.Datastore
	raw       ds.Datastore
	journal   *journal
	namespace ds.Key
	pre       prefixes
	set       *set
	heads     *heads
	// base is the state's trees as they were when the journal was last
	// emptied. Guarded by mu.
	base stateBase
	// gcMu serializes every change of what owns a state block with reclaim,
	// and guards staged, the blocks operations in progress will acquire.
	// It is taken after mu.
	gcMu   sync.Mutex
	staged map[cid.Cid]int
	// fetches are the state fetches in progress, by state root (see
	// stateFetch). fetchMu is taken alone.
	fetchMu sync.Mutex
	fetches map[cid.Cid]*stateFetch

	dagService       ipld.DAGService
	broadcaster      Broadcaster
	broadcastBatchCh chan broadcastBatchHead

	seenHeadsMux sync.RWMutex
	seenHeads    map[cid.Cid]struct{}

	// mu serializes every merge, local write and compaction. The
	// context is replaced, never changed in place, so readers that only
	// ask whether a dot is covered load it without the lock.
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
	// purging holds the blocks a purge has taken and is dropping without
	// mu, which dropping counts until they are gone.
	purging  *cidSafeSet
	dropping sync.WaitGroup

	// ttl is the retention this replica stamps, in milliseconds.
	ttl   atomic.Uint64
	times times
	// since is the fleet time this replica last heard, and sinceUp the
	// uptime it heard it at; horizon is the latest authored time of
	// anything it forgot. All three are guarded by mu.
	since   uint64
	sinceUp time.Duration
	horizon uint64
	// erased is, for each incarnation a rebase replaced, the dots it wrote
	// and no longer held (see dropAllButOwn), until they expire. Guarded by
	// mu.
	erased map[string]*known
	// lastWrite is the uptime of this incarnation's last write.
	lastWrite time.Duration
	// rejoining is set while this replica waits to be judged after a gap,
	// with the fleet time it had heard before it (see rejoin.go).
	rejoining bool
	gapFrom   uint64
	// lastHeard is the uptime it last heard a peer that was not rejoining,
	// counted from its open; liveHeard the same, 0 until one is heard.
	// answered is when it last answered each rejoining incarnation.
	lastHeard time.Duration
	liveHeard time.Duration
	// anyHeard and fleetHeard are the uptimes it last heard a peer whose
	// clock agrees with this one's, and such a peer that has heard a fleet,
	// 0 before the first.
	anyHeard   time.Duration
	fleetHeard time.Duration
	answered   map[string]time.Duration
	// merged is when this replica last covered more of each incarnation:
	// its heartbeats acknowledge those to their authors.
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
	// compactDue asks the next maintenance for a snapshot.
	compactDue bool
	// lows are lower bounds, kept so the frequent passes skip scans that
	// would find nothing: the earliest expiry of a pending removal, the
	// earliest half-life (at + ttl/2) of an indexed block, the earliest
	// expiry of a kept own operation. Every insertion lowers them; a scan
	// sets them to what it found, none = unbounded; 0 means unknown.
	lows lows
	// pendingAt is at or above the latest stamp of a pending removal held
	// here, none until a scan finds it.
	pendingAt uint64
	// generation changes when a rebase replaces the state: a walk begun
	// under an older one merges nothing. rebuilding is the generation whose
	// rebase still walks the judge's state, 0 when none does.
	generation uint64
	rebuilding uint64

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
	delta      *pb.Delta       // the current delta
	node       ipld.Node       // the current ipld Node
	gen        uint64          // the generation the walk began under
}

type broadcastBatchHead struct {
	head     Head
	children []Head
}

// NewDatastore returns a Merkle-CRDT-based Datastore persisting under the
// given namespace of store. It needs a DAG-Service for IPLD nodes and a
// Broadcaster to announce and receive heads.
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
		staged:           map[cid.Cid]int{},
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
// incarnation: the one this store closed under earlier in this process, or a
// new one.
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
	if err := store.dropOrphans(ctx); err != nil {
		return fmt.Errorf("error dropping unmerged blocks: %w", err)
	}
	if _, err := store.reclaim(ctx); err != nil {
		return fmt.Errorf("error removing unowned state blocks: %w", err)
	}
	if store.liveCount, err = countPrefix(ctx, store.store, store.set.elemsPrefix()); err != nil {
		return err
	}
	if store.blockCount, err = countPrefix(ctx, store.store, store.blocksPrefix()); err != nil {
		return err
	}

	// The bad-shutdown key is present at startup only if the previous run
	// did not Close: mark dirty so the repair loop retries the parents a
	// walk left missing. Sync it now, or a crash before the next flush
	// would go undetected.
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

// SetRetention changes T for what this replica writes from now on. What it
// wrote before keeps the T it was written with, so lowering T forgets
// nothing earlier than it was promised, and raising it keeps more.
func (store *Datastore) SetRetention(t time.Duration) error {
	if err := store.opts.checkRetention(t); err != nil {
		return err
	}
	store.ttl.Store(durationMillis(t))
	return nil
}

// retention is the T this replica stamps, in milliseconds.
func (store *Datastore) retention() uint64 { return store.ttl.Load() }

// clock is this replica's wall clock in milliseconds.
func (store *Datastore) clock() uint64 { return millis(store.opts.Clock()) }

// stampNow is the time a block written now carries: its clock, or the fleet
// time it last heard if that is later, so a clock behind the fleet does not
// write what is already old. The caller holds store.mu.
func (store *Datastore) stampNow() uint64 { return max(store.clock(), store.since) }

func (store *Datastore) incarnationKey() incarnationKey {
	return incarnationKey{replicaID: string(store.opts.ReplicaID), namespace: store.namespace.String()}
}

func (store *Datastore) versionKey() ds.Key {
	return store.namespace.ChildString(versionKey)
}

// checkFormat empties a store written in another format. There is no
// migration: a replica that loses its copy this way fetches it again. A
// store with no version written is a new one.
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
	results, err := store.Query(ctx, query.Query{Prefix: prefix.String(), KeysOnly: true})
	if err != nil {
		return 0, err
	}
	defer results.Close()
	n := 0
	for {
		r, ok := results.NextSync()
		if !ok {
			return n, nil
		}
		if r.Error != nil {
			return 0, r.Error
		}
		n++
	}
}

func (store *Datastore) covered(d dot) bool {
	return store.ccRead.Load().covers(d)
}

func (store *Datastore) setContext(c *causalContext) {
	store.cc = c
	store.ccRead.Store(c)
	store.ccMu.Lock()
	close(store.ccChanged)
	store.ccChanged = make(chan struct{})
	store.ccMu.Unlock()
}

// contextChanged is closed the next time the context grows.
func (store *Datastore) contextChanged() <-chan struct{} {
	store.ccMu.Lock()
	defer store.ccMu.Unlock()
	return store.ccChanged
}

// cancelWhenCovered cancels a fetch once this replica covers every dot it is
// fetching: a block nobody holds any more because a snapshot covers it is
// otherwise waited on until the fetch times out.
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

// handleNext will loop on broadcaster.Next(), walking every head it hears
// that this replica does not cover.
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
	// 30% of the configured interval
	leeway := (d * 30 / 100)
	// A random number between -leeway|+leeway.
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
				if err := store.repairDAG(ctx); err != nil {
					store.logger.Error(err)
				}
			}
			timer.Reset(store.opts.RepairInterval)
		}
	}
}

// rebroadcastHeads sends out the heads not heard from anyone else during the
// last interval.
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

// Log some stats every 5 minutes.
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

// isIndexed reports whether this replica holds a block: indexed, and not
// taken by a purge that is dropping it.
func (store *Datastore) isIndexed(ctx context.Context, c cid.Cid) (bool, error) {
	if store.purging.Has(c) {
		return false, nil
	}
	return store.store.Has(ctx, store.blockKey(c))
}

// handleBlock walks the branch under a head unless this replica already
// covers or holds it.
func (store *Datastore) handleBlock(ctx context.Context, h Head) error {
	return store.handleBlockAt(ctx, store.currentGeneration(), h)
}

func (store *Datastore) currentGeneration() uint64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.generation
}

// handleBlockAt is handleBlock for a walk begun under generation gen.
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

// retryLater records a block whose walk failed so repair retries it: a head
// heard once may not be heard again.
func (store *Datastore) retryLater(ctx context.Context, l link) {
	if !l.dot.isZero() {
		if err := store.store.Put(ctx, store.retryKey(l.dot), encodeLink(l)); err != nil {
			store.logger.Warnf("error recording block %s to retry: %s", l.cid, err)
		}
	}
	store.MarkDirty(ctx)
}

func (store *Datastore) retryPrefix() ds.Key { return store.namespace.ChildString(retryNs) }

func (store *Datastore) retryKey(d dot) ds.Key {
	return store.retryPrefix().ChildString(d.segment())
}

// handleBranch walks down from the given blocks and returns once the walk is
// done. A block that cannot be fetched marks the store dirty: it is still
// missing, and repair retries it.
//
// A walk is bounded by D, the delivery bound, as a whole: one that takes
// longer is abandoned and left to repair, so nothing it fetches late is
// merged on the strength of a judgement made when it began.
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

// dagWorker should run in its own goroutine. Workers are launched during
// initialization in NewDatastore().
func (store *Datastore) dagWorker() {
	for job := range store.jobQueue {
		ctx := job.ctx
		select {
		case <-ctx.Done():
			// drain jobs from queue when we are done
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

// sendNewJobs fetches the given blocks and queues each for a worker, skipping
// the ones this replica covers by now or another job is already fetching.
// It blocks until all jobs have been queued.
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

func (store *Datastore) dirtyKey() ds.Key {
	return store.namespace.ChildString(dirtyBitKey)
}

// badShutdownKey is written on New and removed on a clean Close. Its presence
// at startup indicates the previous run did not Close() cleanly, so the store
// should be treated as dirty and repaired.
func (store *Datastore) badShutdownKey() ds.Key {
	return store.namespace.ChildString(badShutdownKey)
}

// MarkDirty marks the Datastore as dirty.
func (store *Datastore) MarkDirty(ctx context.Context) {
	store.logger.Warn("marking datastore as dirty")
	if err := store.store.Put(ctx, store.dirtyKey(), nil); err != nil {
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
	if err := store.store.Delete(ctx, store.dirtyKey()); err != nil {
		store.logger.Errorf("error clearing dirty bit: %s", err)
	}
}

// processNode merges one fetched block and returns the parents to walk.
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
			store.leaveFetch(fetch, settled)
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
				store.logger.Warnf("error removing unowned state blocks: %s", err)
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
	walk, theirs, err := store.mergeSnapshot(ctx, current, delta, held)
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

// afterMerge is what a merge of another replica's block may complete: a rejoin
// held open until this replica holds what a judge vouched with. The caller
// holds store.mu.
func (store *Datastore) afterMerge(ctx context.Context) {
	if err := store.endRejoinIfHeld(ctx); err != nil {
		store.logger.Errorf("ending the rejoin: %s", err)
	}
}

// unmerged settles a block that will never be merged: nothing waits for it
// any more, the block fetched for it goes, and the horizon takes forgot, the
// time of what it taught if that is being forgotten. The caller holds
// store.mu.
func (store *Datastore) unmerged(ctx context.Context, c cid.Cid, d dot, forgot uint64) error {
	m := store.beginMerge()
	m.horizon = max(m.horizon, forgot)
	m.t.del(store.missingKey(d))
	m.t.del(store.retryKey(d))
	if err := m.commit(ctx); err != nil {
		return err
	}
	return store.dropUnindexed(ctx, c)
}

// dropUnindexed removes a block fetched for a dot this replica already
// covers and never indexed: no purge would find it otherwise. What a
// snapshot's state brought is not owned, and goes with the next reclaim.
func (store *Datastore) dropUnindexed(ctx context.Context, c cid.Cid) error {
	held, err := store.isIndexed(ctx, c)
	if err != nil || held {
		return err
	}
	return store.dagService.Remove(ctx, c)
}

// dropFailed removes the fetched block of a merge that failed: it is not
// indexed, so if a snapshot covers it before the retry nothing would.
func (store *Datastore) dropFailed(ctx context.Context, c cid.Cid) {
	if err := store.dropUnindexed(ctx, c); err != nil {
		store.logger.Warnf("error dropping the blocks of a failed merge: %s", err)
	}
}

// repairDAG retries every parent a merged block links and this replica does
// not cover yet, and every head it adopted without merging.
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

// Repair retries every parent this replica is missing and marks the store
// clean once none is left.
func (store *Datastore) Repair(ctx context.Context) error {
	return store.repairDAG(ctx)
}

// Get retrieves the object `value` named by `key`.
// Get will return ErrNotFound if the key is not mapped to a value.
func (store *Datastore) Get(ctx context.Context, key ds.Key) (value []byte, err error) {
	return store.set.Element(ctx, key.String())
}

// Has returns whether the `key` is mapped to a `value`.
func (store *Datastore) Has(ctx context.Context, key ds.Key) (exists bool, err error) {
	return store.set.InSet(ctx, key.String())
}

// GetSize returns the size of the `value` named by `key`.
func (store *Datastore) GetSize(ctx context.Context, key ds.Key) (size int, err error) {
	return ds.GetBackedSize(ctx, store, key)
}

// Query searches the datastore and returns a query result.
func (store *Datastore) Query(ctx context.Context, q query.Query) (query.Results, error) {
	qr, err := store.set.Elements(ctx, q)
	if err != nil {
		return nil, err
	}
	return query.NaiveQueryApply(q, qr), nil
}

// Put stores the object `value` named by `key`.
func (store *Datastore) Put(ctx context.Context, key ds.Key, value []byte) error {
	return store.write(ctx, []op{{key: key.String(), value: value, put: true}})
}

// Delete removes the value for given `key`.
func (store *Datastore) Delete(ctx context.Context, key ds.Key) error {
	return store.write(ctx, []op{{key: key.String()}})
}

type op struct {
	key   string
	value []byte
	put   bool
}

// write publishes one delta for ops. Each op removes every version of its
// key this replica holds, and a put then adds the new one: a key's live
// versions are only ever the ones written concurrently.
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
	delta := store.stamped(&pb.Delta{})
	t := newTxn(store.store)
	for _, o := range ops {
		live, err := store.set.liveElems(ctx, t, o.key)
		if err != nil {
			store.mu.Unlock()
			return err
		}
		for _, e := range live {
			delta.Tombstones = append(delta.Tombstones, tombstoneFor(delta, e))
		}
		if o.put {
			delta.Elements = append(delta.Elements, &pb.Element{Key: o.key, Value: o.value})
		}
	}
	if len(delta.Elements)+len(delta.Tombstones) == 0 {
		store.mu.Unlock()
		return nil
	}
	head, replaced, err := store.publishLocked(ctx, delta)
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

// idleRetire draws a new incarnation for a write that comes after this one
// was idle for more than half the retention, so it never writes under an
// incarnation its peers may have forgotten. The caller holds store.mu.
func (store *Datastore) idleRetire() error {
	idle := store.times.up() - store.lastWrite
	if store.counter == 0 || durationMillis(idle) <= store.retention()/2 {
		return nil
	}
	return store.drawIncarnation()
}

// nonceLen is the length of the nonce that ends every incarnation id.
const nonceLen = 16

// peerOf is the peer an incarnation was drawn by: its ReplicaID, or the
// incarnation itself when it has none, as nothing then tells the
// incarnations of one store from those of another.
func peerOf(incarnation string) string {
	if len(incarnation) <= nonceLen {
		return incarnation
	}
	return incarnation[:len(incarnation)-nonceLen]
}

// drawIncarnation starts a new incarnation: the peer id and a nonce drawn
// at random, its counter at 0. The caller holds store.mu or is opening.
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

// nextDot is the dot of this incarnation's next delta and its prev.
func (store *Datastore) nextDot() (dot, uint64) {
	return dot{replica: store.replica, counter: store.counter + 1}, store.counter
}

// publishLocked writes delta as a new block over the current heads and merges
// it. The counter advances only once the merge commits, so a failed write
// leaves no gap in this incarnation's chain. The caller holds store.mu.
func (store *Datastore) publishLocked(ctx context.Context, delta *pb.Delta) (Head, []Head, error) {
	d, prev := store.nextDot()
	hs, height := store.heads.List()
	delta.Dot = d.toPB()
	delta.Prev = prev
	delta.Priority = height + 1
	delta.Parents = parentsOf(hs)

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

// linkOf is the link to a fetched block.
func linkOf(c cid.Cid, delta *pb.Delta) link {
	return link{cid: c, dot: dotFromPB(delta.GetDot()), at: delta.GetAt(), ttl: delta.GetTtl()}
}

// stamped sets a block this replica is about to write to now: when it is
// written and how long what it teaches is kept.
func (store *Datastore) stamped(delta *pb.Delta) *pb.Delta {
	delta.At = store.stampNow()
	delta.Ttl = store.retention()
	delta.Since = store.broadcastSince()
	delta.Horizon = store.horizon
	return delta
}

// tombstoneFor is the tombstone delta writes for e: needed as long as delta
// is, and never less long than e is.
func tombstoneFor(delta *pb.Delta, e element) *pb.Element {
	ttl := delta.Ttl
	if exp := e.expires(); exp > delta.At+ttl {
		ttl = exp - delta.At
	}
	return &pb.Element{Key: e.key, Dot: e.dot.toPB(), At: delta.At, Ttl: ttl}
}

// maybeCompact compacts once the store holds CompactAfterBlocks blocks and
// at least one block per live element.
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

// Sync flushes everything this store has written to the underlying datastore.
// A key's value cannot be read back without the elements, the context and
// the heads, so the prefix does not narrow it.
func (store *Datastore) Sync(ctx context.Context, prefix ds.Key) error {
	return store.store.Sync(ctx, store.namespace)
}

// Close shuts down the CRDT datastore. A write after it fails with ErrClosed.
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

// Batch accumulates Puts and Deletes and publishes them as one delta on
// Commit. The last operation on a key wins.
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

// Commit publishes the batch as one delta.
func (b *crdtBatch) Commit(ctx context.Context) error {
	b.mu.Lock()
	ops := b.ops
	b.ops, b.index, b.size = nil, map[string]int{}, 0
	b.mu.Unlock()
	return b.store.write(ctx, ops)
}

type blockAt struct {
	node ipld.Node
	kind blockKind
}

// writeBlocks indexes blocks this replica authors before writing them, so a
// crash before the merge that covers them leaves an index entry with a dot
// the context does not cover, which the next open drops with its block.
func (store *Datastore) writeBlocks(ctx context.Context, b block, nodes ...ipld.Node) error {
	blocks := make([]blockAt, len(nodes))
	for i, nd := range nodes {
		blocks[i] = blockAt{node: nd, kind: b.kind}
	}
	t := newTxn(store.store)
	for _, ba := range blocks {
		t.put(store.blockKey(ba.node.Cid()), encodeBlock(block{kind: ba.kind, dot: b.dot, at: b.at, ttl: b.ttl}))
	}
	if err := t.commit(ctx); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, store.opts.DAGSyncerTimeout)
	defer cancel()
	for _, b := range blocks {
		if err := store.dagService.Add(cctx, b.node); err != nil {
			cids := make([]cid.Cid, len(blocks))
			for i, b := range blocks {
				cids[i] = b.node.Cid()
			}
			store.discardBlocks(ctx, cids...)
			return fmt.Errorf("error writing new block %s: %w", b.node.Cid(), err)
		}
	}
	return nil
}

// discardBlocks removes blocks this replica wrote and did not merge, with
// their index entries. A failure leaves them to the next open.
func (store *Datastore) discardBlocks(ctx context.Context, cids ...cid.Cid) {
	if err := store.dagService.RemoveMany(ctx, cids); err != nil {
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

// dropOrphans removes the blocks this replica wrote and never merged: every
// merged block's dot is covered, so an index entry whose dot is not is one a
// crash left between writing a block and merging it.
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
	if err := store.dagService.RemoveMany(ctx, orphans); err != nil {
		return err
	}
	return store.unindex(ctx, orphans)
}

func (store *Datastore) broadcastHeads(ctx context.Context, heads []Head) error {
	if len(heads) == 0 { // nothing to announce
		return nil
	}
	return store.send(ctx, heads, false)
}

// send broadcasts heads, none included, with this replica's fleet time, and
// its frontier when it is a heartbeat.
func (store *Datastore) send(ctx context.Context, heads []Head, beat bool) error {
	if store.broadcaster == nil { // offline
		return nil
	}
	store.logger.Debugf("broadcasting %s", heads)

	bcastBytes, err := store.encodeBroadcast(ctx, heads, beat)
	if err != nil {
		return err
	}
	if err := store.broadcaster.Broadcast(ctx, bcastBytes); err != nil {
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

// PrintDAG pretty prints the blocks this replica holds under its heads, down
// to what a snapshot covers.
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

// DotDAG writes a dot-format representation of the blocks this replica holds
// under its heads to w.
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
		for _, l := range nd.Links() {
			if _, err := fmt.Fprintf(w, "%s -> %s\n", c, l.Cid); err != nil {
				return err
			}
			stack = append(stack, l.Cid)
		}
	}
	_, err := fmt.Fprintln(w, "}")
	return err
}

// Stats wraps internal information about the datastore.
type Stats struct {
	Heads      []Head
	MaxHeight  uint64
	QueuedJobs int
}

// InternalStats returns internal datastore information like the current heads
// and max height.
func (store *Datastore) InternalStats(ctx context.Context) Stats {
	heads, height := store.heads.List()
	return Stats{
		Heads:      heads,
		MaxHeight:  height,
		QueuedJobs: len(store.jobQueue), // capacity: numWorkers
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
