package kvdb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
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
	// At and TTL are the block's stamps (see pb.Delta).
	At, TTL uint64
}

func (h Head) String() string {
	return fmt.Sprintf("{Cid: %s, Height: %d, Dot: %s}", h.Cid, h.Height, h.Dot)
}

func (h Head) link() link { return link{cid: h.Cid, dot: h.Dot, at: h.At, ttl: h.TTL} }

// heads is the persisted head set with an in-memory copy. Changes are staged
// in a merge's txn and applied to the copy once that txn commits.
type heads struct {
	store     ds.Datastore
	namespace ds.Key
	prefix    string

	mu    sync.RWMutex
	cache map[cid.Cid]Head
}

func newHeads(ctx context.Context, store ds.Datastore, namespace ds.Key) (*heads, error) {
	hh := &heads{store: store, namespace: namespace, prefix: namespace.String(), cache: map[cid.Cid]Head{}}
	err := scanStore(ctx, store, namespace, func(k string, v []byte) error {
		c, err := dshelp.DsKeyToCidV1(ds.NewKey(strings.TrimPrefix(k, namespace.String())), cid.Raw)
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

// key is the namespace's child dshelp.MultihashToDsKey names c by, built in
// one allocation.
func (hh *heads) key(c cid.Cid) ds.Key {
	return hashKey(hh.prefix, c)
}

func encodeHead(h Head) []byte { return appendHead(nil, h) }

func appendHead(b []byte, h Head) []byte {
	b = binary.AppendUvarint(b, h.Height)
	b = binary.AppendUvarint(b, h.At)
	b = binary.AppendUvarint(b, h.TTL)
	return appendDot(b, h.Dot)
}

func decodeHead(c cid.Cid, v []byte) (Head, error) {
	var f [3]uint64
	rest, err := uvarintsInto(f[:], v)
	if err != nil {
		return Head{}, errors.New("error decoding head")
	}
	d, err := decodeDot(rest)
	if err != nil {
		return Head{}, err
	}
	return Head{Cid: c, Height: f[0], At: f[1], TTL: f[2], Dot: d}, nil
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
	return hh.appendList(nil)
}

// appendList is List appending the heads to out.
func (hh *heads) appendList(out []Head) ([]Head, uint64) {
	hh.mu.RLock()
	defer hh.mu.RUnlock()
	var maxHeight uint64
	out = slices.Grow(out, len(hh.cache))
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
		t.del(t.key(appendHashKey(t.buf(), hh.prefix, c)))
	}
	for c, h := range hc.add {
		t.put(t.key(appendHashKey(t.buf(), hh.prefix, c)), t.keep(appendHead(t.buf(), h)))
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

// reset forgets every head; the caller has removed them from the store.
func (hh *heads) reset() {
	hh.mu.Lock()
	defer hh.mu.Unlock()
	clear(hh.cache)
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
