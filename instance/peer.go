package instance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/peer"
	pkgkvdb "github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/cm/settings"
)

// A node is made from its peer. Whatever a service's backing is built from is the
// peer's: its host and gossip, its replicated databases, its raft groups, its blocks.
// This file is where the node makes them; nothing is handed in.

// retentionPoll is how often a node rereads the T its databases write under from the
// region's settings. A node that reads a change late is safe: every write carries the
// retention it was written under.
const retentionPoll = time.Minute

// databases is every replicated database this node has open, one per name: two opens
// of one name on one peer would be two replicas over one store.
type databases struct {
	peer.Node
	opener opener

	mu        sync.Mutex
	open      map[string]kvdb.Instance
	retention time.Duration
}

// opener opens a database by name in place of the peer, for a test.
type opener interface {
	Open(name string) (kvdb.Instance, error)
}

func newDatabaseSet(p peer.Node, o opener) *databases {
	return &databases{Node: p, opener: o, open: map[string]kvdb.Instance{}}
}

// at is the database called name, opened on first ask.
func (d *databases) at(name string) (kvdb.Instance, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if db, ok := d.open[name]; ok {
		return db, nil
	}
	var db kvdb.Instance
	var err error
	if d.opener != nil {
		db, err = d.opener.Open(name)
	} else {
		db, err = pkgkvdb.Open(d.Node, name,
			pkgkvdb.WithLogger(logger),
			pkgkvdb.WithRebroadcast(rebroadcastSeconds*time.Second),
			pkgkvdb.WithRegionRetention(d.retention),
			pkgkvdb.WithOnClose(func() { d.forget(name, db) }))
	}
	if err != nil {
		return nil, err
	}
	d.open[name] = db
	return db, nil
}

// Database is the database called name, the one every service on this node shares.
func (d *databases) Database(name string) (kvdb.Instance, error) { return d.at(name) }

// forget drops name only while it is still db: a second Close of a database already
// reopened must not drop the new one.
func (d *databases) forget(name string, db kvdb.Instance) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.open[name] == db {
		delete(d.open, name)
	}
}

// setRetention moves every open database, and every later one, to t; 0 is the default.
// Nothing changes unless every open database can hold it, as every node refuses one
// it cannot the same way.
func (d *databases) setRetention(t time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t == d.retention {
		return nil
	}
	effective := t
	if effective == 0 {
		effective = pkgkvdb.DefaultRetention()
	}
	retainers := make([]kvdb.Retainer, 0, len(d.open))
	for name, db := range d.open {
		r, ok := db.(kvdb.Retainer)
		if !ok {
			continue
		}
		if err := r.CanRetain(effective); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		retainers = append(retainers, r)
	}
	for _, r := range retainers {
		if err := r.SetRetention(effective); err != nil {
			return err
		}
	}
	d.retention = t
	return nil
}

func (d *databases) Close() error {
	d.mu.Lock()
	open := make([]kvdb.Instance, 0, len(d.open))
	for _, db := range d.open {
		open = append(open, db)
	}
	d.mu.Unlock()
	for _, db := range open {
		db.Close()
	}
	return nil
}

// Holding is whether this node has bytes for path, without opening it.
func (d *databases) Holding(ctx context.Context, path string) (bool, error) {
	return pkgkvdb.Holding(ctx, d.Node, path)
}

// Paths is what this node holds below under, cut to depth segments.
func (d *databases) Paths(ctx context.Context, under string, depth int) ([]string, error) {
	return pkgkvdb.Paths(ctx, d.Node, under, depth)
}

// groups opens the consensus groups the services on this node ask for, over its
// peer, each group's members whichever nodes open its namespace, found by
// discovery. What a group keeps is in the peer's datastore.
type groups struct{ n *Instance }

func (g groups) Group(namespace string) (raft.Cluster, error) {
	if g.n.opts.groups != nil {
		return g.n.opts.groups.Group(namespace)
	}
	return raft.New(g.n.Node, namespace, append(g.n.cfg.Raft.Options(), raft.WithClientRetry(g.n.cfg.Clients.Retry))...)
}

// ErrNoRoot is a node asked for what lives in a directory and given none.
var ErrNoRoot = errors.New("node: this needs the node's own directory, and Config.Root names none")

// followRetention keeps the databases' T at what the region's settings say, read
// from cm: the one on this node when it runs one.
func (n *Instance) followRetention(ctx context.Context) {
	region := settings.On(n.HostOf(cm.Name))
	defer region.Close()
	n.applyRetention(ctx, region)
	ticker := time.NewTicker(retentionPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.applyRetention(ctx, region)
		}
	}
}

// applyRetention reads the region's T once and moves the databases to it. A cm not
// reached yet changes nothing.
func (n *Instance) applyRetention(ctx context.Context, region settings.Source) {
	r, err := region.Settings(ctx)
	if err == nil {
		err = n.dbs.setRetention(r.Retention)
	}
	if err != nil && ctx.Err() == nil {
		logger.Debugf("the region's retention: %s", err)
	}
}
