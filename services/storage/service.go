package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/placement"
	"github.com/taubyte/tau/pkg/proto/codec"
	"github.com/taubyte/tau/pkg/proto/gen/tau/storage/v2/storagev2connect"
	"github.com/taubyte/tau/services/cm/settings"
)

// AssetsBucket is the bucket a project's own assets are kept in, one per
// environment: its builds' artifacts and logs. A `storage` resource's bucket is its
// id, a CID, so the two never meet.
const AssetsBucket = "assets"

// Bytes is this node's blockstore as the write and read paths see it.
type Bytes interface {
	Blocks

	// Add stores content on this node: its root, its length and every block it made.
	Add(ctx context.Context, content io.Reader) (root cid.Cid, size uint64, blocks []cid.Cid, err error)

	// Fetch makes every block of root present on this node, asking from first, and
	// returns them.
	Fetch(ctx context.Context, root cid.Cid, from libp2ppeer.ID) ([]cid.Cid, error)

	// Read is an object's bytes, from wherever they are held.
	Read(ctx context.Context, root cid.Cid) (io.ReadSeekCloser, error)

	// Sync makes every block this node stored durable.
	Sync(ctx context.Context) error
}

// DefaultSweep is how often a node reconsiders which shards it holds.
const DefaultSweep = 10 * time.Second

var (
	ErrNoBytes   = errors.New("storage: no blockstore to keep bytes in")
	ErrNoShards  = errors.New("storage: nothing to open an index shard with")
	ErrNoCatalog = errors.New("storage: no catalog of the buckets that exist")

	// ErrUnderHeld is a write fewer nodes took than the cloud promises: not
	// acknowledged, and nothing of it is a version.
	ErrUnderHeld = errors.New("storage: fewer nodes hold it than the cloud promises")
)

// Service is the storage service: the store, as an instance runs it.
type Service interface {
	service.Service
	Store
}

// New is the storage service, unstarted. Start builds it from its instance.
func New() Service { return &server{} }

// server is this node's part of the store: the index shards placement gives it, the
// blocks it holds, and the RPC that writes and reads both.
type server struct {
	host    host.Host
	self    string
	cfg     Config
	bytes   Bytes
	local   *Local
	shards  *shardSet
	catalog Catalog
	members func() []placement.Member
	clock   *Clock
	now     func() time.Time
	meter   Meter
	sweep   time.Duration
	logf    func(format string, args ...any)

	// region is the region's settings, R and K among them; see promise.
	region regionSettings

	// proofs is what the repair loop last established of each root; see
	// repair.go.
	repairMu sync.Mutex
	proofs   map[string]proof

	// sweeper frees blocks no reference reaches, and keeps go through it.
	// asked is when collect last asked about each reference; owed is a
	// reference dropped since swept, the last sweep. See collect.go.
	sweeper   *Sweeper
	collectMu sync.Mutex
	asked     map[string]time.Time
	owed      atomic.Bool
	swept     time.Time

	// floor is the clock floor this node keeps durably; see stamp.
	floorMu sync.Mutex
	floor   uint64

	// life is the service's lifetime, from serve to Close: what it starts on its
	// own runs on it.
	life context.Context

	serverOptions []rpc.ServerOption

	// running is the maintenance loop, which Close waits out before it closes
	// what the loop uses.
	running sync.WaitGroup

	// also is what Start opened for this service beside it, closed with it.
	also []io.Closer

	peersMu sync.Mutex
	peers   map[libp2ppeer.ID]*storagePeer

	startMu   sync.Mutex
	server    *rpc.Server
	stop      context.CancelFunc
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

type storagePeer struct {
	conn *rpc.Client
	svc  storagev2connect.StorageServiceClient
}

// option injects what this service is built from.
type option func(*options) error

type options struct {
	bytes   Bytes
	local   Datastore
	shards  ShardOpener
	catalog Catalog
	members func() []placement.Member
	now     func() time.Time
	meter   Meter
	sweep   time.Duration
	logf    func(format string, args ...any)

	// region is the region's settings, R and K among them; see promise.
	region regionSettings

	serverOptions []rpc.ServerOption
}

// WithBytes is this node's blockstore. Required.
func withBytes(b Bytes) option {
	return func(o *options) error {
		if b == nil {
			return ErrNoBytes
		}
		o.bytes = b
		return nil
	}
}

// WithLocal is the node-local store the never-replicated rows live in. Required.
func withLocal(store Datastore) option {
	return func(o *options) error {
		if store == nil {
			return errors.New("storage: no local store")
		}
		o.local = store
		return nil
	}
}

// WithShards opens and closes this node's index shards. Required.
func withShards(opener ShardOpener) option {
	return func(o *options) error {
		if opener == nil {
			return ErrNoShards
		}
		o.shards = opener
		return nil
	}
}

// WithCatalog is which buckets exist, so a shard's holders keep it open. Required.
func withCatalog(c Catalog) option {
	return func(o *options) error {
		if c == nil {
			return ErrNoCatalog
		}
		o.catalog = c
		return nil
	}
}

// WithMembers is the storage nodes placement runs over, a function since membership
// changes under the service. Without it this node is the whole store.
func withMembers(members func() []placement.Member) option {
	return func(o *options) error {
		o.members = members
		return nil
	}
}

// WithMeter is told what this service accounted for.
func withMeter(m Meter) option {
	return func(o *options) error {
		o.meter = m
		return nil
	}
}

// WithClock is the wall clock stamps and local rows are taken from.
func withClock(now func() time.Time) option {
	return func(o *options) error {
		o.now = now
		return nil
	}
}

// WithSweep is how often this node reconsiders which shards it holds.
func withSweep(d time.Duration) option {
	return func(o *options) error {
		if d <= 0 {
			return fmt.Errorf("storage: a sweep of %s never runs", d)
		}
		o.sweep = d
		return nil
	}
}

// WithLogger is where a failure no caller is told about is said.
func withLogger(logf func(format string, args ...any)) option {
	return func(o *options) error {
		if logf == nil {
			return errors.New("storage: a nil logger")
		}
		o.logf = logf
		return nil
	}
}

// WithServerOptions tunes the RPC server.
func withServerOptions(opts ...rpc.ServerOption) option {
	return func(o *options) error {
		o.serverOptions = append(o.serverOptions, opts...)
		return nil
	}
}

// build is a server over what it is given, not yet serving.
func build(h host.Host, cfg Config, opts ...option) (*server, error) {
	s := &server{}
	if err := s.assemble(h, cfg, opts...); err != nil {
		return nil, err
	}
	return s, nil
}

// assemble validates and allocates; serve serves.
func (s *server) assemble(h host.Host, cfg Config, opts ...option) error {
	if h == nil {
		return errors.New("storage: no host")
	}
	cfg, err := cfg.withDefaults()
	if err != nil {
		return err
	}
	o := options{now: time.Now, sweep: DefaultSweep, logf: func(string, ...any) {}}
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return err
		}
	}
	switch {
	case o.bytes == nil:
		return ErrNoBytes
	case o.local == nil:
		return errors.New("storage: no local store")
	case o.shards == nil:
		return ErrNoShards
	case o.catalog == nil:
		return ErrNoCatalog
	}

	local, err := NewLocal(o.local, o.now)
	if err != nil {
		return err
	}
	shards, err := newShardSet(o.shards)
	if err != nil {
		return err
	}
	sweeper, err := NewSweeper(local, o.bytes, nil)
	if err != nil {
		return err
	}
	s.sweeper = sweeper
	s.owed.Store(true)
	s.host, s.self, s.cfg = h, h.ID().String(), cfg
	s.bytes, s.local, s.shards, s.catalog, s.members = o.bytes, local, shards, o.catalog, o.members
	s.clock, s.now, s.meter, s.sweep, s.logf = NewClock(o.now, cfg.MaxSkew), o.now, o.meter, o.sweep, o.logf
	s.serverOptions = o.serverOptions
	s.peers = map[libp2ppeer.ID]*storagePeer{}
	return nil
}

// serve serves and begins keeping this node's shards.
func (s *server) serve(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.closed.Load() {
		return errors.New("storage: closed")
	}
	if s.server != nil {
		return errors.New("storage: already started")
	}

	// THE CLOCK STARTS WHERE THE LAST RUN LEFT IT, whatever the wall clock says.
	floor, err := s.local.ClockFloor(ctx)
	if err != nil {
		return fmt.Errorf("storage: reading the clock's floor: %w", err)
	}
	s.clock.Floor(floor)
	s.floor = floor

	// What this service starts on its own runs on its lifetime, which Close ends.
	// It is set before the first request can arrive.
	life, stop := context.WithCancel(context.WithoutCancel(ctx))
	s.life = life
	server, err := rpc.Listen(ctx, s.host, Protocol, s.mount(), s.serverOptions...)
	if err != nil {
		stop()
		return fmt.Errorf("storage: listening: %w", err)
	}
	s.server, s.stop = server, stop
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		s.run(life)
	}()
	return nil
}

// Close stops serving and closes every shard this node holds.
func (s *server) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.startMu.Lock()
		server, stop := s.server, s.stop
		s.server, s.stop = nil, nil
		s.startMu.Unlock()
		if stop != nil {
			stop()
		}
		if server != nil {
			s.closeErr = server.Close()
		}
		s.running.Wait()
		if s.shards != nil {
			s.closeErr = errors.Join(s.closeErr, s.shards.closeAll(context.Background()))
		}
		for _, c := range s.also {
			s.closeErr = errors.Join(s.closeErr, c.Close())
		}

		s.peersMu.Lock()
		for _, p := range s.peers {
			p.conn.Close()
		}
		s.peers = nil
		s.peersMu.Unlock()
	})
	return s.closeErr
}

func (s *server) run(ctx context.Context) {
	ticker := time.NewTicker(s.sweep)
	defer ticker.Stop()
	for {
		if err := s.reconcile(ctx); err != nil && ctx.Err() == nil {
			s.logf("storage: keeping shards: %v", err)
		}
		if err := s.repair(ctx); err != nil && ctx.Err() == nil {
			s.logf("storage: repairing copies: %v", err)
		}
		if err := s.collect(ctx); err != nil && ctx.Err() == nil {
			s.logf("storage: collecting versions: %v", err)
		}
		if err := s.sweepIfDue(ctx); err != nil && ctx.Err() == nil {
			s.logf("storage: sweeping blocks: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// reconcile opens the shards placement gives this node for every bucket the catalog
// or a write has shown it, and closes the rest once they are handed off (handoff.go).
func (s *server) reconcile(ctx context.Context) error {
	since := s.shards.mark()
	buckets, err := s.catalog.Buckets(ctx)
	if err != nil {
		return err
	}
	seen, err := s.local.Seen(ctx)
	if err != nil {
		return err
	}
	replicas, barrier, err := s.promise(ctx)
	if err != nil {
		return err
	}
	members := s.placement()
	keep, err := desiredShards(append(buckets, seen...), members, replicas, s.self)
	if err != nil {
		return err
	}
	for _, shard := range s.shards.held() {
		if _, desired := keep[shard]; !desired && !s.handedOff(ctx, shard, members, replicas, barrier) {
			keep[shard] = struct{}{}
		}
	}
	_, _, err = s.shards.reconcile(ctx, keep, since)
	return err
}

// placement is the storage nodes: whoever discovery finds serving storage and this
// node (host.go), or this node alone when composed with no members, as a test is.
func (s *server) placement() []placement.Member {
	if s.members == nil {
		return []placement.Member{{ID: s.self, Accepts: true}}
	}
	return s.members()
}

// regionSettings is where R and K are read: cm, through a client that keeps them.
type regionSettings interface {
	Settings(ctx context.Context) (settings.Region, error)
}

// promise is R and K as the region's settings say, each capped where it is used by
// the holders placement names. A node that has not heard them refuses rather than
// computing with numbers it guessed.
func (s *server) promise(ctx context.Context) (replicas, barrier int, err error) {
	if s.region == nil {
		return 0, 0, ErrNoSettings
	}
	r, err := s.region.Settings(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %w", ErrNoSettings, err)
	}
	return r.StorageReplicas, r.StorageBarrier, nil
}

// clockLease is how far ahead of the stamps it mints a node keeps its clock floor:
// one durable write per lease of writing, and a node back from a restart starts at
// most this far ahead of the last stamp it minted.
const clockLease = time.Second

// stamp mints the next stamp, never one below a stamp this node minted before it
// last stopped: no stamp is returned until the floor kept durably is above it. A
// wall clock that came back behind would otherwise mint a version below one this
// node already wrote, which every replica then orders first.
func (s *server) stamp(ctx context.Context) (uint64, error) {
	st, err := s.clock.Now()
	if err != nil {
		return 0, err
	}
	physical, _ := UnpackStamp(st)
	s.floorMu.Lock()
	defer s.floorMu.Unlock()
	if physical < s.floor {
		return st, nil
	}
	next := physical + uint64(clockLease/time.Millisecond)
	if err := s.local.SetClockFloor(ctx, next); err != nil {
		return 0, fmt.Errorf("storage: keeping the clock's floor: %w", err)
	}
	s.floor = next
	return st, nil
}

// indexOf is the instance's index on this node, opening its shard if placement gives
// it here and held open until release; ok is false when placement does not.
func (s *server) indexOf(ctx context.Context, a Address) (i index, release func(), ok bool, err error) {
	if i, release, ok, err := s.shards.hold(ctx, a.Bucket(), false); ok || err != nil {
		i.meter = s.meter
		return i, release, ok, err
	}
	shard, err := shardOf(a.Bucket())
	if err != nil {
		return index{}, nil, false, err
	}
	replicas, _, err := s.promise(ctx)
	if err != nil {
		return index{}, nil, false, err
	}
	if !slices.Contains(indexHolders(shard, s.placement(), replicas), s.self) {
		return index{}, nil, false, nil
	}
	i, release, ok, err = s.shards.hold(ctx, a.Bucket(), true)
	i.meter = s.meter
	return i, release, ok, err
}

// peer is a client of another storage node.
func (s *server) peer(id string) (storagev2connect.StorageServiceClient, error) {
	pid, err := libp2ppeer.Decode(id)
	if err != nil {
		return nil, fmt.Errorf("storage: %q is not a peer: %w", id, err)
	}
	s.peersMu.Lock()
	defer s.peersMu.Unlock()
	if s.peers == nil {
		return nil, errors.New("storage: closed")
	}
	if p, ok := s.peers[pid]; ok {
		return p.svc, nil
	}
	conn, err := rpc.NewClient(s.host, pid, Protocol)
	if err != nil {
		return nil, err
	}
	p := &storagePeer{conn: conn, svc: storagev2connect.NewStorageServiceClient(conn.HTTP(), conn.URL(), codec.Option())}
	s.peers[pid] = p
	return p.svc, nil
}
