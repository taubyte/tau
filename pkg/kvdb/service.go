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
}

var slogger = logging.Logger("kvdb")

func New(node peer.Node) kvdb.Factory {
	f := &factory{
		dbs:  make(map[string]*kvDatabase),
		node: node,
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
	// THE KEY AND HOW BIG THE VALUE WAS, NEVER THE VALUE. Every write a
	// workload makes — a cache entry, a database row, an object's metadata —
	// arrives here, so logging `v` puts guest data in the node's log the moment
	// an operator raises this logger to info, which is exactly what an operator
	// does when something is wrong. It also copied every value written whether
	// or not the level was on: the conversion happens at the call site, before
	// Infof gets to check.
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

// Destroy closes this database and removes what this node holds for it.
//
// IT GOES UNDER THE CRDT RATHER THAN THROUGH IT. The replicated set lives at a
// key prefix inside the node's own datastore — see New, which opens it at
// `crdt/<path>` — so removing that prefix is a local write the broadcaster
// never sees. Going through Delete would publish a tombstone per key, and every
// replica would apply it: the node letting go would delete the database for the
// whole fleet.
//
// CLOSE FIRST, so nothing is writing into the prefix while it is being removed
// and the topic is already unsubscribed. Close also drops this database from the
// factory, so a later New opens it again from whatever the fleet still holds.
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

// Holding reports whether this node has any bytes for a database path, WITHOUT
// opening it.
//
// THE WITHOUT IS THE POINT. Opening subscribes, so a node that answered "do you
// hold this?" by opening would become a holder because it was asked — and would
// then answer a digest that looks converged, authorising a drop by a node whose
// replacement it never was.
//
// It reads the same prefix Destroy removes, so it is true for a copy this node
// has never opened in this run: a restarted node holds what it held.
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

// Paths is every database path this node has bytes for under a prefix, cut to
// `depth` segments below it.
//
// THE DEPTH IS THE CALLER'S, because only the caller knows how it names a
// database — the keys below that are the CRDT's own structure, and a reader
// that guessed where the name ends would enumerate internals as databases.
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
