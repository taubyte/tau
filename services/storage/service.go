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

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/placement"
	"github.com/taubyte/tau/pkg/proto/codec"
	"github.com/taubyte/tau/pkg/proto/gen/tau/storage/v2/storagev2connect"
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

// Service is this node's part of the store: the index shards placement gives it, the
// blocks it holds, and the RPC that writes and reads both.
type Service struct {
	host    host.Host
	self    string
	cfg     Config
	bytes   Bytes
	local   *Local
	shards  *shardSet
	catalog Catalog
	members func() []placement.Member
	fleet   *fleetPolicy
	clock   *Clock
	now     func() time.Time
	meter   Meter
	sweep   time.Duration
	logf    func(format string, args ...any)

	serverOptions []rpc.ServerOption

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

// Option injects what this service is built from.
type Option func(*options) error

type options struct {
	bytes   Bytes
	local   Datastore
	shards  ShardOpener
	catalog Catalog
	members func() []placement.Member
	fleet   fleetSource
	now     func() time.Time
	meter   Meter
	sweep   time.Duration
	logf    func(format string, args ...any)

	serverOptions []rpc.ServerOption
}

// WithBytes is this node's blockstore. Required.
func WithBytes(b Bytes) Option {
	return func(o *options) error {
		if b == nil {
			return ErrNoBytes
		}
		o.bytes = b
		return nil
	}
}

// WithLocal is the node-local store the never-replicated rows live in. Required.
func WithLocal(store Datastore) Option {
	return func(o *options) error {
		if store == nil {
			return errors.New("storage: no local store")
		}
		o.local = store
		return nil
	}
}

// WithShards opens and closes this node's index shards. Required.
func WithShards(opener ShardOpener) Option {
	return func(o *options) error {
		if opener == nil {
			return ErrNoShards
		}
		o.shards = opener
		return nil
	}
}

// WithCatalog is which buckets exist, so a shard's holders keep it open. Required.
func WithCatalog(c Catalog) Option {
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
func WithMembers(members func() []placement.Member) Option {
	return func(o *options) error {
		o.members = members
		return nil
	}
}

// WithFleet is where R and K are read. Without it they are the defaults.
func WithFleet(source fleetSource) Option {
	return func(o *options) error {
		o.fleet = source
		return nil
	}
}

// WithMeter is told what this service accounted for.
func WithMeter(m Meter) Option {
	return func(o *options) error {
		o.meter = m
		return nil
	}
}

// WithClock is the wall clock stamps and local rows are taken from.
func WithClock(now func() time.Time) Option {
	return func(o *options) error {
		o.now = now
		return nil
	}
}

// WithSweep is how often this node reconsiders which shards it holds.
func WithSweep(d time.Duration) Option {
	return func(o *options) error {
		if d <= 0 {
			return fmt.Errorf("storage: a sweep of %s never runs", d)
		}
		o.sweep = d
		return nil
	}
}

// WithLogger is where a failure no caller is told about is said.
func WithLogger(logf func(format string, args ...any)) Option {
	return func(o *options) error {
		if logf == nil {
			return errors.New("storage: a nil logger")
		}
		o.logf = logf
		return nil
	}
}

// WithServerOptions tunes the RPC server.
func WithServerOptions(opts ...rpc.ServerOption) Option {
	return func(o *options) error {
		o.serverOptions = append(o.serverOptions, opts...)
		return nil
	}
}

// New validates and allocates; Start serves.
func New(h host.Host, cfg Config, opts ...Option) (*Service, error) {
	if h == nil {
		return nil, errors.New("storage: no host")
	}
	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	o := options{now: time.Now, sweep: DefaultSweep, logf: func(string, ...any) {}}
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, err
		}
	}
	switch {
	case o.bytes == nil:
		return nil, ErrNoBytes
	case o.local == nil:
		return nil, errors.New("storage: no local store")
	case o.shards == nil:
		return nil, ErrNoShards
	case o.catalog == nil:
		return nil, ErrNoCatalog
	}

	local, err := NewLocal(o.local, o.now)
	if err != nil {
		return nil, err
	}
	shards, err := newShardSet(o.shards)
	if err != nil {
		return nil, err
	}
	s := &Service{
		host:          h,
		self:          h.ID().String(),
		cfg:           cfg,
		bytes:         o.bytes,
		local:         local,
		shards:        shards,
		catalog:       o.catalog,
		members:       o.members,
		clock:         NewClock(o.now, cfg.MaxSkew),
		now:           o.now,
		meter:         o.meter,
		sweep:         o.sweep,
		logf:          o.logf,
		serverOptions: o.serverOptions,
		peers:         map[libp2ppeer.ID]*storagePeer{},
	}
	if o.fleet != nil {
		s.fleet = &fleetPolicy{from: o.fleet}
	}
	return s, nil
}

// Start serves and begins keeping this node's shards.
func (s *Service) Start(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.closed.Load() {
		return errors.New("storage: closed")
	}
	if s.server != nil {
		return errors.New("storage: already started")
	}

	server, err := rpc.Listen(ctx, s.host, Protocol, s.mount(), s.serverOptions...)
	if err != nil {
		return fmt.Errorf("storage: listening: %w", err)
	}
	s.server = server

	loop, stop := context.WithCancel(context.WithoutCancel(ctx))
	s.stop = stop
	go s.run(loop)
	return nil
}

// Close stops serving and closes every shard this node holds.
func (s *Service) Close() error {
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
		_, _, err := s.shards.reconcile(context.Background(), nil)
		s.closeErr = errors.Join(s.closeErr, err)

		s.peersMu.Lock()
		for _, p := range s.peers {
			p.conn.Close()
		}
		s.peers = nil
		s.peersMu.Unlock()
	})
	return s.closeErr
}

func (s *Service) run(ctx context.Context) {
	ticker := time.NewTicker(s.sweep)
	defer ticker.Stop()
	for {
		if err := s.reconcile(ctx); err != nil && ctx.Err() == nil {
			s.logf("storage: keeping shards: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// reconcile opens the shards placement gives this node for every bucket the catalog
// or a write has shown it, and closes the rest.
func (s *Service) reconcile(ctx context.Context) error {
	buckets, err := s.catalog.Buckets(ctx)
	if err != nil {
		return err
	}
	seen, err := s.local.Seen(ctx)
	if err != nil {
		return err
	}
	replicas, _, _ := s.fleet.read(ctx)
	desired, err := desiredShards(append(buckets, seen...), s.placement(), replicas, s.self)
	if err != nil {
		return err
	}
	_, _, err = s.shards.reconcile(ctx, desired)
	return err
}

// placement is the storage nodes, and this one alone when nothing says otherwise.
func (s *Service) placement() []placement.Member {
	if s.members != nil {
		if members := s.members(); len(members) > 0 {
			return members
		}
	}
	return []placement.Member{{ID: s.self, Accepts: true}}
}

// indexOf is the instance's index on this node, opening its shard if placement gives
// it here; ok is false when it does not.
func (s *Service) indexOf(ctx context.Context, a Address) (index, bool, error) {
	if i, ok, err := s.shards.index(a.Bucket()); ok || err != nil {
		i.meter = s.meter
		return i, ok, err
	}
	shard, err := shardOf(a.Bucket())
	if err != nil {
		return index{}, false, err
	}
	replicas, _, _ := s.fleet.read(ctx)
	if !slices.Contains(indexHolders(shard, s.placement(), replicas), s.self) {
		return index{}, false, nil
	}
	if err := s.shards.ensure(ctx, shard); err != nil {
		return index{}, false, err
	}
	i, ok, err := s.shards.index(a.Bucket())
	i.meter = s.meter
	return i, ok, err
}

// peer is a client of another storage node.
func (s *Service) peer(id string) (storagev2connect.StorageServiceClient, error) {
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
