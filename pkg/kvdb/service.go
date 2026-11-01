package kvdb

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/peer"
	"go.uber.org/zap/zapcore"

	logging "github.com/ipfs/go-log/v2"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
)

func (kvd *kvDatabase) cleanup() {
	if kvd != nil {
		if kvd.datastore != nil && !kvd.closed {
			kvd.datastore.Close()
			kvd.closed = true
		}
	}
}

func (kvd *kvDatabase) Close() {
	kvd.closeCtxC()

	time.Sleep(100 * time.Millisecond)

	kvd.cleanup()

	kvd.factory.deleteDB(kvd.path)
}

func (f *factory) Close() {
	for _, k := range f.dbs {
		k.Close()
	}
}

type factory struct {
	dbs     map[string]*kvDatabase
	dbsLock sync.RWMutex
	node    peer.Node

	// retention is the T every database opened here writes under, 0 for
	// the default; fixed is set when the caller chose it, so a bad one
	// refuses to open rather than falls back.
	retention time.Duration
	fixed     bool
}

var slogger = logging.Logger("kvdb")

type FactoryOption func(*factory)

func WithRetention(t time.Duration) FactoryOption {
	return func(f *factory) { f.retention, f.fixed = t, true }
}

// WithFleet keeps T at what the cloud's fleet document says, reread every
// interval while the node runs.
func WithFleet(src FleetSource, every time.Duration) FactoryOption {
	return func(f *factory) {
		f.applyFleet(f.node.Context(), src)
		go f.followFleet(f.node.Context(), src, every)
	}
}

func New(node peer.Node, options ...FactoryOption) kvdb.Factory {
	f := &factory{
		dbs:  make(map[string]*kvDatabase),
		node: node,
	}
	for _, o := range options {
		o(f)
	}

	if slogger.Level() <= zapcore.DebugLevel {
		go func() {
			for {
				select {
				case <-f.node.Context().Done():
					return
				case <-time.After(10 * time.Second):
					f.dbsLock.RLock()
					for path, s := range f.dbs {
						slogger.Debug("KVDB ", path, "HEADS -> ", s.datastore.InternalStats(f.node.Context()).Heads)
					}
					f.dbsLock.RUnlock()
				}
			}
		}()
	}

	return f

}

func (f *factory) getDB(path string) *kvDatabase {
	return f.dbs[path]
}

func (f *factory) deleteDB(path string) {
	f.dbsLock.Lock()
	defer f.dbsLock.Unlock()

	delete(f.dbs, path)
}

func (f *factory) New(logger logging.StandardLogger, path string, rebroadcastIntervalSec int) (kvdb.KVDB, error) {
	f.dbsLock.Lock()
	defer f.dbsLock.Unlock()

	cachedDB := f.getDB(path)
	if cachedDB != nil {
		return cachedDB, nil
	}

	s := &kvDatabase{
		factory: f,
		path:    path,
	}

	var err error
	s.closeCtx, s.closeCtxC = f.node.NewChildContextWithCancel()
	s.broadcaster, err = NewPubSubBroadcaster(s.closeCtx, f.node.Messaging(), path+"/broadcast")
	if err != nil {
		s.closeCtxC()
		slogger.Error("kvdb broadcaster init failed with ", err)
		return nil, err
	}

	opts := DefaultOptions()
	opts.Logger = logger
	opts.ReplicaID = []byte(f.node.ID())
	if rebroadcastIntervalSec == 0 {
		rebroadcastIntervalSec = defaultRebroadcastIntervalSec
	}

	opts.RebroadcastInterval = time.Duration(rebroadcastIntervalSec * int(time.Second))
	opts.DeliveryBound = max(opts.DeliveryBound, opts.DAGSyncerTimeout+opts.RebroadcastInterval)
	if f.retention != 0 {
		if err := opts.checkRetention(f.retention); err == nil || f.fixed {
			opts.Retention = f.retention
		} else {
			// Every node opening this database computes the same margin and
			// refuses the same T, so falling back keeps them agreeing.
			slogger.Errorf("the fleet's retention cannot hold %s: %s; keeping %s", path, err, opts.Retention)
		}
	}
	// The key and the value's size, never the value: every workload write arrives
	// here, so logging v would put guest data in the node's log the moment an
	// operator raises this logger.
	opts.PutHook = func(k ds.Key, v []byte) {
		logger.Infof("Added: [%s] (%d bytes)", k, len(v))
	}

	opts.DeleteHook = func(k ds.Key) {
		logger.Infof("Removed: [%s]\n", k)
	}

	s.datastore, err = NewDatastore(f.node.Store(), ds.NewKey("crdt/"+path), f.node.DAG(), s.broadcaster, opts)
	if err != nil {
		slogger.Error("kvdb.New failed with ", err)
		s.closeCtxC()
		return nil, err
	}

	f.dbs[path] = s

	return s, nil
}

func (k *kvDatabase) Factory() kvdb.Factory {
	return k.factory
}

// Destroy closes this database and removes what this node holds for it. It
// goes under the CRDT, removing its prefix in the node's own datastore: Delete
// would publish a tombstone per key and every replica would apply it. Close
// comes first, so nothing writes into the prefix and a later New reopens it
// from what the fleet still holds.
func (kvd *kvDatabase) Destroy(ctx context.Context) error {
	path := kvd.path
	store := kvd.factory.node.Store()
	kvd.Close()

	results, err := store.Query(ctx, query.Query{
		Prefix:   ds.NewKey("crdt/" + path).String(),
		KeysOnly: true,
	})
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

// Holding reports whether this node has bytes for path without opening it:
// opening subscribes, and a node that opened to answer would become a holder
// because it was asked. A restarted node holds what it held.
func (f *factory) Holding(ctx context.Context, path string) (bool, error) {
	results, err := f.node.Store().Query(ctx, query.Query{
		Prefix: ds.NewKey("crdt/" + path).String(), KeysOnly: true, Limit: 1,
	})
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

// Paths cuts to depth segments below under. The depth is the caller's: only it
// knows where a database name ends and the CRDT's own keys begin.
func (f *factory) Paths(ctx context.Context, under string, depth int) ([]string, error) {
	results, err := f.node.Store().Query(ctx, query.Query{
		Prefix: ds.NewKey("crdt/" + under).String(), KeysOnly: true,
	})
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
