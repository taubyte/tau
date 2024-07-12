package kvdb

import (
	"sync"
	"time"

	crdt "github.com/ipfs/go-ds-crdt"
	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/peer"
	"go.uber.org/zap/zapcore"

	logging "github.com/ipfs/go-log/v2"

	ds "github.com/ipfs/go-datastore"
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
						slogger.Debug("KVDB ", path, "HEADS -> ", s.datastore.InternalStats().Heads)
					}
					f.dbsLock.RUnlock()
				}
			}
		}()
	}

	return f

}

func (f *factory) getDB(path string) *kvDatabase {
	f.dbsLock.RLock()
	defer f.dbsLock.RUnlock()
	return f.dbs[path]
}

func (f *factory) deleteDB(path string) {
	f.dbsLock.Lock()
	defer f.dbsLock.Unlock()

	delete(f.dbs, path)
}

// TODO: This should be Time.Duration
func (f *factory) New(logger logging.StandardLogger, path string, rebroadcastIntervalSec int) (kvdb.KVDB, error) {
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
		slogger.Fatal(err)
		return nil, err
	}

	opts := crdt.DefaultOptions()
	opts.Logger = logger
	if rebroadcastIntervalSec == 0 {
		rebroadcastIntervalSec = defaultRebroadcastIntervalSec
	}

	opts.RebroadcastInterval = time.Duration(rebroadcastIntervalSec * int(time.Second))
	opts.PutHook = func(k ds.Key, v []byte) {
		logger.Infof("Added: [%s] -> %s\n", k, string(v))
	}

	opts.DeleteHook = func(k ds.Key) {
		logger.Infof("Removed: [%s]\n", k)
	}

	s.datastore, err = crdt.New(f.node.Store(), ds.NewKey("crdt/"+path), f.node.DAG(), s.broadcaster, opts)
	if err != nil {
		slogger.Error("kvdb.New failed with ", err)
		s.closeCtxC()
		return nil, err
	}

	f.dbsLock.Lock()
	defer f.dbsLock.Unlock()
	f.dbs[path] = s

	return s, nil
}

func (k *kvDatabase) Factory() kvdb.Factory {
	return k.factory
}
