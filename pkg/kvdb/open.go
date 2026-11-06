package kvdb

import (
	"context"
	"sort"
	"strings"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	logging "github.com/ipfs/go-log/v2"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/peer"
)

var _ kvdb.Retainer = (*kvDatabase)(nil)

var slogger = logging.Logger("kvdb")

// OpenOption configures one database as it is opened.
type OpenOption func(*openOptions)

type openOptions struct {
	logger      logging.StandardLogger
	rebroadcast time.Duration
	retention   time.Duration
	// fallback is a T this database writes under only if it can hold it.
	fallback bool
	onClose  func()

	// follow reads the cloud's T, every poll.
	follow func(context.Context) (time.Duration, error)
	poll   time.Duration

	observer Observer
}

// Observer hears every change the database commits, written on this node or merged
// from another: a key whose value appears or changes, or one that goes absent. It is
// called on the merge path, so it must not block and must not call into the
// database. Keys are as Put takes them, with no leading slash.
type Observer interface {
	Put(key string, value []byte)
	Delete(key string)
}

// WithObserver has o hear every change this database commits.
func WithObserver(o Observer) OpenOption {
	return func(opts *openOptions) { opts.observer = o }
}

// WithLogger is where the database says what it does. Default the package's own.
func WithLogger(l logging.StandardLogger) OpenOption {
	return func(o *openOptions) { o.logger = l }
}

// WithRebroadcast is how often the database re-announces its heads.
func WithRebroadcast(d time.Duration) OpenOption {
	return func(o *openOptions) { o.rebroadcast = d }
}

// WithRetention is the T this database writes under; 0 is the default. One it
// cannot hold is refused.
func WithRetention(t time.Duration) OpenOption {
	return func(o *openOptions) { o.retention, o.fallback = t, false }
}

// WithFleetRetention is the cloud's T, written under when this database can hold it
// and the default otherwise: every node opening it computes the same margin, so
// every node falls back alike.
func WithFleetRetention(t time.Duration) OpenOption {
	return func(o *openOptions) { o.retention, o.fallback = t, true }
}

// WithRetentionFrom has the database follow the cloud's T, read with read every
// poll: 0 is the default, a T it cannot hold leaves it where it was, and a read that
// fails keeps the last. Every database of one cloud reads one document, so every
// node moves the same way.
func WithRetentionFrom(read func(context.Context) (time.Duration, error), poll time.Duration) OpenOption {
	return func(o *openOptions) { o.follow, o.poll = read, poll }
}

// WithOnClose runs once the database is closed, for whoever keeps the set of a peer's
// open databases.
func WithOnClose(f func()) OpenOption {
	return func(o *openOptions) { o.onClose = f }
}

// Open opens the replicated database at path over p: a CRDT in p's store, gossiped on
// p under path. The caller owns it and closes it. Two opens of one path on one peer
// are two replicas over one store, so whoever owns the peer keeps one per path.
func Open(p peer.Node, path string, options ...OpenOption) (kvdb.Instance, error) {
	d, err := open(p, path, options...)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func open(p peer.Node, path string, options ...OpenOption) (*kvDatabase, error) {
	o := openOptions{logger: slogger, rebroadcast: time.Duration(defaultRebroadcastIntervalSec) * time.Second}
	for _, apply := range options {
		apply(&o)
	}

	d := &kvDatabase{Node: p, path: path, onClose: o.onClose}
	var err error
	d.closeCtx, d.closeCtxC = p.NewChildContextWithCancel()
	d.broadcaster, err = NewPubSubBroadcaster(d.closeCtx, p.Messaging(), path+"/broadcast")
	if err != nil {
		d.closeCtxC()
		return nil, err
	}

	opts := DefaultOptions()
	opts.Logger = o.logger
	opts.ReplicaID = []byte(p.ID())
	opts.RebroadcastInterval = o.rebroadcast
	opts.DeliveryBound = max(opts.DeliveryBound, opts.DAGSyncerTimeout+opts.RebroadcastInterval)
	if o.retention != 0 {
		switch err := opts.checkRetention(o.retention); {
		case err == nil:
			opts.Retention = o.retention
		case o.fallback:
			o.logger.Errorf("the fleet's retention cannot hold %s: %s; keeping %s", path, err, opts.Retention)
		default:
			d.closeCtxC()
			return nil, err
		}
	}
	// The key and the value's size, never the value: every workload write arrives
	// here, so logging v would put guest data in the node's log the moment an
	// operator raises this logger.
	opts.PutHook = func(k ds.Key, v []byte) {
		o.logger.Infof("Added: [%s] (%d bytes)", k, len(v))
		if o.observer != nil {
			o.observer.Put(strings.TrimPrefix(k.String(), "/"), v)
		}
	}
	opts.DeleteHook = func(k ds.Key) {
		o.logger.Infof("Removed: [%s]\n", k)
		if o.observer != nil {
			o.observer.Delete(strings.TrimPrefix(k.String(), "/"))
		}
	}

	d.datastore, err = NewDatastore(p.Store(), storeKey(path), p.DAG(), d.broadcaster, opts)
	if err != nil {
		d.closeCtxC()
		return nil, err
	}
	if o.follow != nil && o.poll > 0 {
		go d.follow(o.follow, o.poll, o.logger)
	}
	return d, nil
}

// follow moves this database to the T read, until it closes.
func (d *kvDatabase) follow(read func(context.Context) (time.Duration, error), poll time.Duration, logger logging.StandardLogger) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		if err := d.retainAsRead(read); err != nil && d.closeCtx.Err() == nil {
			logger.Errorf("%s: the cloud's retention: %s", d.path, err)
		}
		select {
		case <-d.closeCtx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *kvDatabase) retainAsRead(read func(context.Context) (time.Duration, error)) error {
	t, err := read(d.closeCtx)
	if err != nil {
		return err
	}
	if t == 0 {
		t = DefaultRetention()
	}
	if err := d.CanRetain(t); err != nil {
		return err
	}
	return d.SetRetention(t)
}

func storeKey(path string) ds.Key { return ds.NewKey("crdt/" + path) }

// DatastoreOf is the replicated store under db, for a reader that needs an ordered
// prefix query, which kvdb.Instance does not offer. ok is false for a database this
// package did not open.
func DatastoreOf(db kvdb.Instance) (*Datastore, bool) {
	d, ok := db.(*kvDatabase)
	if !ok {
		return nil, false
	}
	return d.datastore, true
}

// CanRetain is whether this database can write under T, without changing it.
func (d *kvDatabase) CanRetain(t time.Duration) error { return d.datastore.opts.checkRetention(t) }

// SetRetention changes T for what this database writes from now on.
func (d *kvDatabase) SetRetention(t time.Duration) error { return d.datastore.SetRetention(t) }

func (d *kvDatabase) cleanup() {
	if d != nil && d.datastore != nil && !d.closed {
		d.datastore.Close()
		d.closed = true
	}
}

func (d *kvDatabase) Close() {
	d.closeCtxC()

	time.Sleep(100 * time.Millisecond)

	d.cleanup()
	if d.onClose != nil {
		d.onClose()
	}
}

// Destroy closes this database and removes what this node holds for it. It goes under
// the CRDT, removing its prefix in the node's own datastore: Delete would publish a
// tombstone per key and every replica would apply it. Close comes first, so nothing
// writes into the prefix and a later Open starts again from what the fleet still holds.
func (d *kvDatabase) Destroy(ctx context.Context) error {
	store := d.Store()
	d.Close()

	results, err := store.Query(ctx, query.Query{Prefix: storeKey(d.path).String(), KeysOnly: true})
	if err != nil {
		return err
	}
	defer results.Close()

	batch, err := store.Batch(ctx)
	if err != nil {
		return err
	}
	for entry := range results.Next() {
		if entry.Error != nil {
			return entry.Error
		}
		if err := batch.Delete(ctx, ds.NewKey(entry.Key)); err != nil {
			return err
		}
	}
	return batch.Commit(ctx)
}

// Holding reports whether p has bytes for path without opening it: opening subscribes,
// and a node that opened to answer would become a holder because it was asked. A
// restarted node holds what it held.
func Holding(ctx context.Context, p peer.Node, path string) (bool, error) {
	results, err := p.Store().Query(ctx, query.Query{Prefix: storeKey(path).String(), KeysOnly: true, Limit: 1})
	if err != nil {
		return false, err
	}
	defer results.Close()

	entry, held := results.NextSync()
	if entry.Error != nil {
		return false, entry.Error
	}
	return held, nil
}

// Paths cuts to depth segments below under, over what p holds. The depth is the
// caller's: only it knows where a database name ends and the CRDT's own keys begin.
func Paths(ctx context.Context, p peer.Node, under string, depth int) ([]string, error) {
	results, err := p.Store().Query(ctx, query.Query{Prefix: storeKey(under).String(), KeysOnly: true})
	if err != nil {
		return nil, err
	}
	defer results.Close()

	seen := map[string]bool{}
	var out []string
	for entry := range results.Next() {
		if entry.Error != nil {
			return nil, entry.Error
		}
		segments := ds.NewKey(entry.Key).Namespaces()
		// "crdt" plus the prefix's own segments, then the caller's depth.
		want := 1 + len(ds.NewKey(under).Namespaces()) + depth
		if len(segments) < want {
			continue
		}
		path := strings.Join(segments[1:want], "/")
		if !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out, nil
}
