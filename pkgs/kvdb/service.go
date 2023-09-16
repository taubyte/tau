package kvdb

import (
	"sync"
	"time"

	crdt "github.com/ipfs/go-ds-crdt"
	"github.com/taubyte/go-interfaces/kvdb"
	"github.com/taubyte/p2p/peer"

	logging "github.com/ipfs/go-log/v2"

	ds "github.com/ipfs/go-datastore"
)

// make sure kvdb is closed
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

// close all kvdb
func (f *factory) Close() {
	for _, k := range f.dbs {
		k.Close()
	}

	f.wg.Wait()
}

// RWMutex allows multiple readers to access resource while ensuring exclusive access for a single writer
type factory struct {
	dbs     map[string]*kvDatabase
	wg      sync.WaitGroup
	dbsLock sync.RWMutex
	node    peer.Node
}

// new a pointer of factory
func New(node peer.Node) kvdb.Factory {
	return &factory{
		dbs:  make(map[string]*kvDatabase),
		node: node,
	}
}

// safely access and retrieve a database path with a read lock
func (f *factory) getDB(path string) *kvDatabase {
	f.dbsLock.RLock()
	defer f.dbsLock.RUnlock()
	return f.dbs[path]
}

// safely delete a database path with a write lock
func (f *factory) deleteDB(path string) {
	f.dbsLock.Lock()
	defer f.dbsLock.Unlock()

	delete(f.dbs, path)
}

func (f *factory) New(logger logging.StandardLogger, path string, rebroadcastIntervalSec int) (kvdb.KVDB, error) {
	cachedDB := f.getDB(path)
	// return cachedDB if it already exists
	if cachedDB != nil {
		return cachedDB, nil
	}
	// assign a database to current factory
	s := &kvDatabase{
		factory: f,
		path:    path,
	}

	var err error
	s.closeCtx, s.closeCtxC = f.node.NewChildContextWithCancel()
	//CRDTs are data types that can be replicated across multiple computers in a network
	s.broadcaster, err = crdt.NewPubSubBroadcaster(s.closeCtx, f.node.Messaging(), path+"/broadcast")
	if err != nil {
		s.closeCtxC()
		logger.Fatal(err)
		return nil, err
	}

	opts := crdt.DefaultOptions()
	opts.Logger = logger
	
	// Set default rebroadcast interval if not provided
	if rebroadcastIntervalSec == 0 {
		rebroadcastIntervalSec = defaultRebroadcastIntervalSec
	}

	opts.RebroadcastInterval = time.Duration(rebroadcastIntervalSec * int(time.Second))
	// The PutHook function is triggered whenever an element
	// is successfully added to the datastore (either by a local
	// or remote update), and only when that addition is considered the
	// prevalent value.
	opts.PutHook = func(k ds.Key, v []byte) {
		logger.Infof("Added: [%s] -> %s\n", k, string(v))

	}

	opts.DeleteHook = func(k ds.Key) {
		logger.Infof("Removed: [%s]\n", k)
	}
	// try to creates a new CRDT (Conflict-free Replicated Data Type) datastore
	s.datastore, err = crdt.New(f.node.Store(), ds.NewKey("crdt/"+path), f.node.DAG(), s.broadcaster, opts)
	if err != nil {
		logger.Error("kvdb.New failed with ", err)
		s.closeCtxC()
		return nil, err
	}

	f.wg.Add(1)
	// anonymous goroutine periodically logs the number of heads in the CRDT datastore 
	// every 3 seconds while the database is active
	go func() {
		defer f.wg.Done()
		for {
			select {
			case <-time.After(3 * time.Second):
				logger.Debug("KVDB ", path, "HEADS -> ", s.datastore.InternalStats().Heads)
			case <-s.closeCtx.Done():
				return
			}
		}
	}()

	// Safely add the created kvDatabase to the factory's dbs map
	f.dbsLock.Lock()
	defer f.dbsLock.Unlock()
	f.dbs[path] = s

	return s, nil
}

func (k *kvDatabase) Factory() kvdb.Factory {
	return k.factory
}
