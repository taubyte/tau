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
)

// A Head is a block no other local block links: what the next local delta
// will link, and what this replica broadcasts.
type Head struct {
	Cid    cid.Cid
	Height uint64
	Dot    dot
}

func (h Head) String() string {
	return fmt.Sprintf("{Cid: %s, Height: %d, Dot: %s}", h.Cid, h.Height, h.Dot)
}

// heads is the persisted head set with an in-memory copy. Changes are staged
// in a merge's txn and applied to the copy once that txn commits.
type heads struct {
	store     ds.Datastore
	namespace ds.Key

	mu    sync.RWMutex
	cache map[cid.Cid]Head
}

func newHeads(ctx context.Context, store ds.Datastore, namespace ds.Key) (*heads, error) {
	hh := &heads{store: store, namespace: namespace, cache: map[cid.Cid]Head{}}
	err := scanStore(ctx, store, namespace, func(k string, v []byte) error {
		c, err := dshelp.DsKeyToCidV1(ds.NewKey(strings.TrimPrefix(k, namespace.String())), cid.DagProtobuf)
		if err != nil {
			return fmt.Errorf("bad head key %s: %w", k, err)
		}
		h, err := decodeHead(c, v)
		if err != nil {
			return err
		}
		hh.cache[c] = h
		return nil
	})
	if err != nil {
		return nil, err
	}
	return hh, nil
}

func (hh *heads) key(c cid.Cid) ds.Key {
	return hh.namespace.Child(dshelp.MultihashToDsKey(c.Hash()))
}

func encodeHead(h Head) []byte {
	return append(binary.AppendUvarint(nil, h.Height), encodeDot(h.Dot)...)
}

func decodeHead(c cid.Cid, v []byte) (Head, error) {
	height, n := binary.Uvarint(v)
	if n <= 0 {
		return Head{}, errors.New("error decoding head height")
	}
	d, err := decodeDot(v[n:])
	if err != nil {
		return Head{}, err
	}
	return Head{Cid: c, Height: height, Dot: d}, nil
}

func (hh *heads) Get(c cid.Cid) (Head, bool) {
	hh.mu.RLock()
	defer hh.mu.RUnlock()
	h, ok := hh.cache[c]
	return h, ok
}

func (hh *heads) Len() int {
	hh.mu.RLock()
	defer hh.mu.RUnlock()
	return len(hh.cache)
}

// List returns the heads and the greatest height among them.
func (hh *heads) List() ([]Head, uint64) {
	hh.mu.RLock()
	defer hh.mu.RUnlock()
	var maxHeight uint64
	out := make([]Head, 0, len(hh.cache))
	for _, h := range hh.cache {
		out = append(out, h)
		if h.Height > maxHeight {
			maxHeight = h.Height
		}
	}
	return out, maxHeight
}

// headChanges is a merge's staged edit of the head set.
type headChanges struct {
	add    map[cid.Cid]Head
	remove map[cid.Cid]struct{}
}

func newHeadChanges() *headChanges {
	return &headChanges{add: map[cid.Cid]Head{}, remove: map[cid.Cid]struct{}{}}
}

func (hc *headChanges) addHead(h Head) {
	delete(hc.remove, h.Cid)
	hc.add[h.Cid] = h
}

func (hc *headChanges) removeHead(c cid.Cid) {
	delete(hc.add, c)
	hc.remove[c] = struct{}{}
}

func (hh *heads) stage(t *txn, hc *headChanges) {
	for c := range hc.remove {
		t.del(hh.key(c))
	}
	for c, h := range hc.add {
		t.put(hh.key(c), encodeHead(h))
	}
}

func (hh *heads) apply(hc *headChanges) {
	hh.mu.Lock()
	defer hh.mu.Unlock()
	for c := range hc.remove {
		delete(hh.cache, c)
	}
	for c, h := range hc.add {
		hh.cache[c] = h
	}
}

// add adds one head outside a merge.
func (hh *heads) add(ctx context.Context, h Head) error {
	if err := hh.store.Put(ctx, hh.key(h.Cid), encodeHead(h)); err != nil {
		return err
	}
	hh.mu.Lock()
	hh.cache[h.Cid] = h
	hh.mu.Unlock()
	return nil
}
