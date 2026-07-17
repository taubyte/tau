package kvdb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"

	dshelp "github.com/ipfs/boxo/datastore/dshelp"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	logging "github.com/ipfs/go-log/v2"
)

// Heads represents a set of the current root CIDs of the Merkle-CRDT DAG.
type Heads interface {
	Get(ctx context.Context, c cid.Cid) (Head, bool)
	Len(ctx context.Context) (int, error)
	LenDAG(ctx context.Context, dagName string) (int, error)
	Replace(ctx context.Context, old Head, new Head) error
	Add(ctx context.Context, head Head) error
	List(ctx context.Context) ([]Head, uint64, error)
	ListDAG(ctx context.Context, dagName string) ([]Head, uint64, error)
}

type Head struct {
	HeadValue
	Cid cid.Cid
}

type HeadValue struct {
	Height  uint64
	DAGName string
}

func (h Head) String() string {
	return fmt.Sprintf("{Cid: %s, Height: %d, DAGName: %s}", h.Cid.String(), h.Height, h.DAGName)
}

// heads manages the current Merkle-CRDT heads.
type heads struct {
	store ds.Datastore
	// cache contains the current contents of the store
	cache         map[cid.Cid]HeadValue
	cacheMux      sync.RWMutex
	namespace     ds.Key
	namespaceDags ds.Key

	logger logging.StandardLogger
}

func newHeads(ctx context.Context, store ds.Datastore, namespace, namespaceDags ds.Key, logger logging.StandardLogger) (*heads, error) {
	hh := &heads{
		store:         store,
		namespace:     namespace,
		namespaceDags: namespaceDags,
		logger:        logger,
		cache:         make(map[cid.Cid]HeadValue),
	}
	if err := hh.primeCache(ctx); err != nil {
		return nil, err
	}
	return hh, nil
}

func (hh *heads) key(h Head) ds.Key {
	if dagName := h.DAGName; dagName == "" {
		// /<namespace>/<cid>
		return hh.namespace.Child(dshelp.MultihashToDsKey(h.Cid.Hash()))
	} else {
		// /<namespaceDags>/<dagName>/<cid>
		return hh.namespaceDags.Child(ds.NewKey(dagName)).Child(dshelp.MultihashToDsKey(h.Cid.Hash()))
	}
}

func (hh *heads) write(ctx context.Context, store ds.Write, h Head) error {
	buf := make([]byte, binary.MaxVarintLen64)
	n := binary.PutUvarint(buf, h.Height)
	if n == 0 {
		return errors.New("error encoding height")
	}
	return store.Put(ctx, hh.key(h), buf[0:n])
}

func (hh *heads) delete(ctx context.Context, store ds.Write, h Head) error {
	err := store.Delete(ctx, hh.key(h))
	// The go-datastore API currently says Delete doesn't return
	// ErrNotFound, but it used to say otherwise.  Leave this
	// here to be safe.
	if err == ds.ErrNotFound {
		return nil
	}
	return err
}

// Get returns a Head if a given cid is among the current heads.
func (hh *heads) Get(ctx context.Context, c cid.Cid) (Head, bool) {
	var ok bool
	var hv HeadValue
	hh.cacheMux.RLock()
	{
		hv, ok = hh.cache[c]
	}
	hh.cacheMux.RUnlock()
	return Head{Cid: c, HeadValue: hv}, ok
}

func (hh *heads) Len(ctx context.Context) (int, error) {
	var ret int
	hh.cacheMux.RLock()
	{
		ret = len(hh.cache)
	}
	hh.cacheMux.RUnlock()
	return ret, nil
}

func (hh *heads) LenDAG(ctx context.Context, dagName string) (int, error) {
	var ret int
	hh.cacheMux.RLock()
	{
		for _, v := range hh.cache {
			if v.DAGName == dagName {
				ret++
			}
		}
	}
	hh.cacheMux.RUnlock()
	return ret, nil
}

// Replace replaces a head with a new cid.
func (hh *heads) Replace(ctx context.Context, old Head, new Head) error {
	hh.logger.Debugf("replacing DAG head: %s -> %s", old, new)
	if old.DAGName != new.DAGName {
		hh.logger.Warnf("new head and old head belong to different DAGs: %s -> %s", old, new)
	}

	var store ds.Write = hh.store

	batchingDs, batching := store.(ds.Batching)
	var err error
	if batching {
		store, err = batchingDs.Batch(ctx)
		if err != nil {
			return err
		}
	}

	err = hh.write(ctx, store, new)
	if err != nil {
		return err
	}

	hh.cacheMux.Lock()
	defer hh.cacheMux.Unlock()

	if !batching {
		hh.cache[new.Cid] = new.HeadValue
	}

	err = hh.delete(ctx, store, old)
	if err != nil {
		return err
	}
	if !batching {
		delete(hh.cache, old.Cid)
	}

	if batching {
		err := store.(ds.Batch).Commit(ctx)
		if err != nil {
			return err
		}
		delete(hh.cache, old.Cid)
		hh.cache[new.Cid] = new.HeadValue
	}
	return nil
}

func (hh *heads) Add(ctx context.Context, head Head) error {
	hh.logger.Debugf("adding new DAG head: %s", head)
	if err := hh.write(ctx, hh.store, head); err != nil {
		return err
	}

	hh.cacheMux.Lock()
	{
		hh.cache[head.Cid] = head.HeadValue
	}
	hh.cacheMux.Unlock()
	return nil
}

func (hh *heads) list(ctx context.Context, dagName string, useDagName bool) ([]Head, uint64, error) {
	var maxHeight uint64
	var heads []Head

	hh.cacheMux.RLock()
	{
		heads = make([]Head, 0, len(hh.cache))
		for c, headValue := range hh.cache {
			if !useDagName || headValue.DAGName == dagName {
				heads = append(heads, Head{Cid: c, HeadValue: headValue})
				if headValue.Height > maxHeight {
					maxHeight = headValue.Height
				}
			}
		}
	}
	hh.cacheMux.RUnlock()

	// sort.Slice(heads, func(i, j int) bool {
	// 	ci := heads[i].Bytes()
	// 	cj := heads[j].Bytes()
	// 	return bytes.Compare(ci, cj) < 0
	// })

	return heads, maxHeight, nil
}

// List returns the list of current heads plus the max height.
func (hh *heads) List(ctx context.Context) ([]Head, uint64, error) {
	return hh.list(ctx, "", false)
}

func (hh *heads) ListDAG(ctx context.Context, dagName string) ([]Head, uint64, error) {
	return hh.list(ctx, dagName, true)
}

// DeleteDAG removes all heads for the given dagName and returns them. The
// returned heads can be used as traversal roots to remove associated DAG
// blocks and set entries.
func (hh *heads) DeleteDAG(ctx context.Context, dagName string) ([]Head, error) {
	hh.cacheMux.Lock()
	defer hh.cacheMux.Unlock()

	var store ds.Write = hh.store
	batchingDs, batching := store.(ds.Batching)
	var err error
	if batching {
		store, err = batchingDs.Batch(ctx)
		if err != nil {
			return nil, err
		}
	}

	var deleted []Head
	for c, hv := range hh.cache {
		if hv.DAGName != dagName {
			continue
		}
		h := Head{Cid: c, HeadValue: hv}
		if err := hh.delete(ctx, store, h); err != nil {
			return nil, err
		}
		deleted = append(deleted, h)
	}

	if batching {
		if err := store.(ds.Batch).Commit(ctx); err != nil {
			return nil, err
		}
	}

	for _, h := range deleted {
		delete(hh.cache, h.Cid)
	}

	return deleted, nil
}

// primeCache builds the heads cache based on what's in storage; since
// it is called from the constructor only we don't bother locking.
func (hh *heads) primeCache(ctx context.Context) (ret error) {
	err := hh.primeCacheNs(ctx)
	if err != nil {
		return err
	}
	return hh.primeCacheDagsNs(ctx)
}

func (hh *heads) primeCacheNs(ctx context.Context) (ret error) {
	q := query.Query{
		Prefix:   hh.namespace.String(),
		KeysOnly: false,
	}

	results, err := hh.store.Query(ctx, q)
	if err != nil {
		return err
	}
	// nolint:errcheck
	defer results.Close()

	for r := range results.Next() {
		if r.Error != nil {
			return r.Error
		}
		headKey := ds.NewKey(strings.TrimPrefix(r.Key, hh.namespace.String()))
		headCid, err := dshelp.DsKeyToCidV1(headKey, cid.DagProtobuf)
		if err != nil {
			return fmt.Errorf("unable to convert given key %s to a Cid V1: %w", r.Key, err)
		}
		height, n := binary.Uvarint(r.Value)
		if n <= 0 {
			return errors.New("error decoding height")
		}

		hv := HeadValue{
			Height:  height,
			DAGName: "",
		}

		hh.cache[headCid] = hv
	}
	return nil
}

func (hh *heads) primeCacheDagsNs(ctx context.Context) (ret error) {
	// same with dags namespace
	q := query.Query{
		Prefix:   hh.namespaceDags.String(),
		KeysOnly: false,
	}

	results, err := hh.store.Query(ctx, q)
	if err != nil {
		return err
	}
	// nolint:errcheck
	defer results.Close()

	for r := range results.Next() {
		if r.Error != nil {
			return r.Error
		}

		headKey := ds.NewKey(strings.TrimPrefix(r.Key, hh.namespaceDags.String()))
		// left with /<dagName>/<cid>
		namespcs := headKey.Namespaces()
		if len(namespcs) != 2 {
			hh.logger.Error("bad head key: %s", r.Key)
			continue
		}
		dagName := namespcs[0]
		cidKey := namespcs[1]

		headCid, err := dshelp.DsKeyToCidV1(ds.NewKey(cidKey), cid.DagProtobuf)
		if err != nil {
			return err
		}
		height, n := binary.Uvarint(r.Value)
		if n <= 0 {
			return errors.New("error decoding height")
		}

		hv := HeadValue{
			Height:  height,
			DAGName: dagName,
		}

		hh.cache[headCid] = hv
	}

	return nil
}
