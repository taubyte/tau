package database

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	ds "github.com/ipfs/go-datastore"
	logging "github.com/ipfs/go-log/v2"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/database"
	kvengine "github.com/taubyte/tau/pkg/database/engines/kv"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	pkgkvdb "github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/pkg/placement"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/register"
	statsService "github.com/taubyte/tau/services/stats"
)

// Service is the database service as an instance runs it.
type Service interface {
	service.Service

	// Local is this service as the compute tier's kv seam, with no stream.
	Local() wasmtier.KVs

	// Held is how many databases this node has resident.
	Held() int
}

// Config is the operator's: how often this node reconsiders what it holds, and how
// many databases it keeps resident for how long. Zero is each one's default.
type Config struct {
	Sweep       time.Duration `yaml:"sweep"`
	MaxResident int           `yaml:"max_resident"`
	Idle        time.Duration `yaml:"idle"`
}

// ErrNoConsensus is an instance that opens no raft groups, so no ledger can be
// ordered.
var ErrNoConsensus = errors.New("database: this instance opens no raft groups, so a ledger has no group to live in")

// retentionPoll is how often a database rereads the cloud's retention.
const retentionPoll = time.Minute

var logger = logging.Logger("tau.database")

// placementSource is discovery as this service reads the fleet from it.
type placementSource interface {
	Placement() []placement.Member
}

// Start runs the database service on an instance: each database a replicated store of
// its own on the peer, each ledger a raft group the instance opens, what it
// holds read off the peer's disk, its declarations through the register in the
// instance's region, and its fleet what the discovery beside it sees.
func (s *server) Start(ctx context.Context, h service.Host) error {
	var cfg Config
	if err := h.Config(&cfg); err != nil {
		return err
	}
	groups, ok := service.Get[raft.Groups](h)
	if !ok {
		return ErrNoConsensus
	}
	ledgers, err := NewRaftLedgers(groups, LedgerBudget)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	views := register.ViewsOn(h)
	replicas := &replicas{Node: h.Peer(), fleet: views, open: map[string]kvdb.Instance{}}
	copies, err := NewCopies(replicas, ledgers, replicas)
	if err != nil {
		views.Close()
		return err
	}
	engine, err := kvengine.New(copies.Entries)
	if err != nil {
		views.Close()
		return fmt.Errorf("database: %w", err)
	}
	registry := database.NewRegistry()
	if err := registry.Register(ctx, engine); err != nil {
		views.Close()
		return fmt.Errorf("database: %w", err)
	}

	opts := []option{
		withEngines(registry),
		withConsensus(database.CRDT, database.Raft),
		withRegister(views),
		withHoldings(copies),
		withClock(h.Now),
		withSweep(cfg.Sweep),
		// THE FLEET AS THIS NODE SEES IT, asked each sweep: a node running no
		// discovery sees no fleet and moves nothing.
		withMembers(func() []placement.Member {
			if d, ok := service.Get[placementSource](h); ok {
				return d.Placement()
			}
			return nil
		}),
	}
	if cfg.MaxResident != 0 {
		opts = append(opts, withMaxResident(cfg.MaxResident))
	}
	if cfg.Idle != 0 {
		opts = append(opts, withIdle(cfg.Idle))
	}
	if err := s.assemble(h.Peer().Host(), opts...); err != nil {
		views.Close()
		return err
	}
	// THE CONNECTIONS BEFORE THE GROUPS UNDER THEM, and the stores last: a ledger's
	// connection is a prefix over a group.
	s.owned = append(s.owned, ledgers, replicas, views)
	return s.serve(ctx)
}

// Report is this service's part of what its node says about itself: the fleet
// revision it computes with, so a node with a stale floor is visible to the rest, and
// how much repair it knows is outstanding.
func (s *server) Report(snap *statsService.Snapshot) {
	if s.floor == nil {
		return
	}
	snap.FleetRevision = s.FleetRevision()
	snap.DatabasesPending = s.Pending()
}

// replicas is every database's store on this peer, each opened once, following the
// cloud's retention, and closed with the service.
type replicas struct {
	peer.Node
	fleet register.Fleet

	mu   sync.Mutex
	open map[string]kvdb.Instance
}

var (
	_ Stores = (*replicas)(nil)
	_ Disk   = (*replicas)(nil)
)

func (r *replicas) at(name string) (kvdb.Instance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if db, ok := r.open[name]; ok {
		return db, nil
	}
	var db kvdb.Instance
	db, err := pkgkvdb.Open(r.Node, name,
		pkgkvdb.WithLogger(logger),
		pkgkvdb.WithRetentionFrom(func(ctx context.Context) (time.Duration, error) {
			return register.Retention(ctx, r.fleet)
		}, retentionPoll),
		pkgkvdb.WithOnClose(func() { r.forget(name, db) }))
	if err != nil {
		return nil, fmt.Errorf("database: opening the store for %s: %w", name, err)
	}
	r.open[name] = db
	return db, nil
}

// forget drops name only while it is still db: a second Close of a store already
// reopened must not drop the new one.
func (r *replicas) forget(name string, db kvdb.Instance) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.open[name] == db {
		delete(r.open, name)
	}
}

func (r *replicas) Store(name string) (database.Store, error) {
	db, err := r.at(name)
	if err != nil {
		return nil, err
	}
	return rows{db}, nil
}

// Release opens the store only to destroy it: a restarted node holds bytes it never
// opened.
func (r *replicas) Release(ctx context.Context, name string) error {
	db, err := r.at(name)
	if err != nil {
		return err
	}
	destroyer, ok := db.(interface {
		Destroy(context.Context) error
	})
	if !ok {
		return errors.New("database: this store cannot release a database it holds")
	}
	return destroyer.Destroy(ctx)
}

func (r *replicas) Holding(ctx context.Context, path string) (bool, error) {
	return pkgkvdb.Holding(ctx, r.Node, path)
}

func (r *replicas) Paths(ctx context.Context, under string, depth int) ([]string, error) {
	return pkgkvdb.Paths(ctx, r.Node, under, depth)
}

func (r *replicas) Close() error {
	r.mu.Lock()
	open := make([]kvdb.Instance, 0, len(r.open))
	for _, db := range r.open {
		open = append(open, db)
	}
	r.mu.Unlock()
	for _, db := range open {
		db.Close()
	}
	return nil
}

// rows is a replicated store as one database's entries, telling "not there" from
// "could not look".
type rows struct{ db kvdb.Instance }

func (r rows) Get(ctx context.Context, key string) ([]byte, bool, error) {
	v, err := r.db.Get(ctx, key)
	switch {
	case err == nil:
		return v, true, nil
	case errors.Is(err, ds.ErrNotFound):
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("database: reading a row: %w", err)
	}
}

func (r rows) Put(ctx context.Context, key string, value []byte) error {
	return r.db.Put(ctx, key, value)
}

func (r rows) Delete(ctx context.Context, key string) error {
	if err := r.db.Delete(ctx, key); err != nil && !errors.Is(err, ds.ErrNotFound) {
		return err
	}
	return nil
}

func (r rows) List(ctx context.Context, prefix string) ([]string, error) {
	return r.db.List(ctx, prefix)
}
