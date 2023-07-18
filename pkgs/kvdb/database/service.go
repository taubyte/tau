package database

import (
	"fmt"
	"sync"
	"time"

	crdt "github.com/ipfs/go-ds-crdt"
	p2p "github.com/taubyte/go-interfaces/p2p/peer"

	logging "github.com/ipfs/go-log/v2"

	ds "github.com/ipfs/go-datastore"
)

func (kvd *KVDatabase) cleanup() {
	if kvd != nil {
		if kvd.datastore != nil {
			kvd.datastore.Close()
			kvd.datastore = nil
		}
	}
}

func (kvd *KVDatabase) Close() {
	kvd.closeCtxC()
	kvd.cleanup()

	dbsLock.Lock()
	defer dbsLock.Unlock()
	delete(dbs, kvd.path)
}

var (
	dbs     = make(map[string]*KVDatabase)
	dbsLock sync.RWMutex
)

func getDB(path string) *KVDatabase {
	dbsLock.RLock()
	defer dbsLock.RUnlock()
	return dbs[path]
}

func New(logger logging.StandardLogger, node p2p.Node, path string, rebroadcastIntervalSec int) (s *KVDatabase, err error) {
	cachedDB := getDB(path)
	if cachedDB != nil {
		return cachedDB, nil
	}

	dbsLock.Lock()
	defer dbsLock.Unlock()

	s = &KVDatabase{
		path: path,
	}

	s.closeCtx, s.closeCtxC = node.NewChildContextWithCancel()

	s.broadcaster, err = crdt.NewPubSubBroadcaster(s.closeCtx, node.Messaging(), path+"/broadcast")
	if err != nil {
		s.closeCtxC()
		logger.Fatal(err)
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

	s.datastore, err = crdt.New(node.Store(), ds.NewKey("crdt/"+path), node.DAG(), s.broadcaster, opts)
	if err != nil {
		logger.Error("kvdb.New failed with ", err)
		s.closeCtxC()
		return nil, err
	}

	go func() {
		for {
			select {
			case <-time.After(3 * time.Second):
				fmt.Println("KVDB ", path, "HEADS -> ", s.datastore.InternalStats().Heads)
			case <-s.closeCtx.Done():
				return
			}
		}
	}()

	dbs[path] = s
	return s, nil
}
