package kvdb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	dshelp "github.com/ipfs/boxo/datastore/dshelp"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"google.golang.org/protobuf/proto"
)

// A state's blocks, its StateRoot and the nodes of its trees, are shared:
// one node sits under any number of snapshots, this replica's and those it
// merged, and under the base the journal is applied to. No dot owns one,
// so the purge that drops a snapshot's block cannot drop its state. A state
// block is owned instead, with a durable reference count:
//
//	/n/<cid>  its references: one per owned block linking it, and one per
//	          root naming it
//	/x/<cid>  a block that may be held and owned by nothing, to be removed
//
// The roots are the StateRoot of each indexed snapshot (its index entry
// names it) and the base's trees. Acquiring a block that is not owned yet
// acquires every block it links, so an owned block's whole closure is owned
// and held; releasing a block's last reference releases what it links and
// marks it for removal. Both are writes of the txn that changes the roots,
// committed with commitAtomic, so a crash leaves the counts of before or of
// after, never a mix.
//
// A block is marked before it is written, or fetched, and acquiring it, or
// indexing a fetched delta or snapshot, unmarks it. reclaim removes the
// marked blocks that nothing owns or indexes and no operation in progress
// has staged: a compaction or a fetch stages every block it will acquire or
// index, so none goes before. After a crash nothing is staged, and whatever
// a compaction or a fetch wrote and never kept goes.
const (
	ownedNs    = "n"
	reclaimNs  = "x"
	baseKey    = "sr"
	redoKey    = "w"
	appliedKey = "wa"
)

// reclaimBatch bounds the blocks reclaim removes under gcMu at a time:
// every merge that changes ownership waits for it.
const reclaimBatch = 512

func (store *Datastore) ownedPrefix() ds.Key   { return store.namespace.ChildString(ownedNs) }
func (store *Datastore) reclaimPrefix() ds.Key { return store.namespace.ChildString(reclaimNs) }

func (store *Datastore) ownedKey(c cid.Cid) ds.Key {
	return store.ownedPrefix().Child(dshelp.MultihashToDsKey(c.Hash()))
}

func (store *Datastore) reclaimKey(c cid.Cid) ds.Key {
	return store.reclaimPrefix().Child(dshelp.MultihashToDsKey(c.Hash()))
}

// refs changes ownership inside one txn. Blocks are read through ng.
type refs struct {
	store *Datastore
	t     *txn
	ng    ipld.NodeGetter
}

func (store *Datastore) refsIn(t *txn, ng ipld.NodeGetter) *refs {
	return &refs{store: store, t: t, ng: ng}
}

func (r *refs) count(ctx context.Context, c cid.Cid) (uint64, error) {
	v, err := r.t.get(ctx, r.store.ownedKey(c))
	if errors.Is(err, ds.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, k := binary.Uvarint(v)
	if k <= 0 || n == 0 {
		return 0, fmt.Errorf("bad reference count of %s", c)
	}
	return n, nil
}

func (r *refs) links(ctx context.Context, c cid.Cid) ([]cid.Cid, error) {
	nd, err := r.ng.Get(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("reading state block %s: %w", c, err)
	}
	ls := nd.Links()
	out := make([]cid.Cid, len(ls))
	for i, l := range ls {
		out[i] = l.Cid
	}
	return out, nil
}

// acquire adds a reference to c.
func (r *refs) acquire(ctx context.Context, c cid.Cid) error {
	if !c.Defined() {
		return nil
	}
	n, err := r.count(ctx, c)
	if err != nil {
		return err
	}
	r.t.put(r.store.ownedKey(c), binary.AppendUvarint(nil, n+1))
	if n > 0 {
		return nil
	}
	r.t.del(r.store.reclaimKey(c))
	kids, err := r.links(ctx, c)
	if err != nil {
		return err
	}
	for _, k := range kids {
		if err := r.acquire(ctx, k); err != nil {
			return err
		}
	}
	return nil
}

// release drops a reference to c.
func (r *refs) release(ctx context.Context, c cid.Cid) error {
	if !c.Defined() {
		return nil
	}
	n, err := r.count(ctx, c)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("releasing state block %s, which nothing owns", c)
	}
	if n > 1 {
		r.t.put(r.store.ownedKey(c), binary.AppendUvarint(nil, n-1))
		return nil
	}
	r.t.del(r.store.ownedKey(c))
	r.t.put(r.store.reclaimKey(c), nil)
	kids, err := r.links(ctx, c)
	if err != nil {
		return err
	}
	for _, k := range kids {
		if err := r.release(ctx, k); err != nil {
			return err
		}
	}
	return nil
}

// stage keeps cids from reclaim until unstage. With mark, each one nothing
// owns is also marked for removal, durably, before the caller writes it.
func (store *Datastore) stage(ctx context.Context, cids []cid.Cid, mark bool) error {
	store.gcMu.Lock()
	defer store.gcMu.Unlock()
	for _, c := range cids {
		store.staged[c]++
	}
	if !mark {
		return nil
	}
	t := newTxn(store.store)
	for _, c := range cids {
		owned, err := store.store.Has(ctx, store.ownedKey(c))
		if err != nil {
			return err
		}
		if !owned {
			t.put(store.reclaimKey(c), nil)
		}
	}
	return t.commit(ctx)
}

func (store *Datastore) unstage(cids []cid.Cid) {
	store.gcMu.Lock()
	defer store.gcMu.Unlock()
	for _, c := range cids {
		if store.staged[c]--; store.staged[c] <= 0 {
			delete(store.staged, c)
		}
	}
}

// reclaim removes every marked block that nothing owns or stages, and
// returns how many it removed.
func (store *Datastore) reclaim(ctx context.Context) (int, error) {
	total := 0
	for {
		n, more, err := store.reclaimSome(ctx)
		total += n
		if err != nil || !more {
			return total, err
		}
	}
}

func (store *Datastore) reclaimSome(ctx context.Context) (int, bool, error) {
	store.gcMu.Lock()
	defer store.gcMu.Unlock()
	prefix := store.reclaimPrefix()
	var drop []cid.Cid
	t := newTxn(store.store)
	more := false
	err := scanStore(ctx, store.store, prefix, func(k string, _ []byte) error {
		c, err := cidOfBlockKey(prefix, k)
		if err != nil {
			return err
		}
		if store.staged[c] > 0 {
			return nil
		}
		if len(drop) == reclaimBatch {
			more = true
			return nil
		}
		owned, err := store.store.Has(ctx, store.ownedKey(c))
		if err == nil && !owned {
			owned, err = store.isIndexed(ctx, c)
		}
		if err != nil {
			return err
		}
		if !owned {
			drop = append(drop, c)
		}
		t.del(ds.RawKey(k))
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	if len(drop) > 0 {
		if err := store.dagService.RemoveMany(ctx, drop); err != nil {
			return 0, false, fmt.Errorf("error removing unowned state blocks: %w", err)
		}
	}
	return len(drop), more, t.commit(ctx)
}

// commitAtomic commits t so that a crash at any point leaves all of its
// writes or none, whatever batches the store has: they are recorded under
// one key first, with a sequence number, then applied in one batch, in no
// particular order, and only then is that number recorded as applied; open
// applies a record that is not. A batch that fails is given up the same way, so on a store whose
// batches are atomic an error leaves none of the writes. It writes past the
// journaling store, recording the journal's marks with the rest.
func (store *Datastore) commitAtomic(ctx context.Context, t *txn) error {
	if len(t.writes) == 0 {
		return nil
	}
	seq, err := readMillis(ctx, store.raw, store.appliedKey())
	if err != nil {
		return err
	}
	redo := &pb.Redo{Writes: make([]*pb.Write, 0, len(t.order)+1), Seq: seq + 1}
	for _, k := range t.order {
		v := t.writes[k]
		marks, err := store.journal.marks(ctx, k, v)
		if err != nil {
			return err
		}
		for _, m := range marks {
			redo.Writes = append(redo.Writes, &pb.Write{Key: []byte(m)})
		}
		redo.Writes = append(redo.Writes, &pb.Write{Key: []byte(k), Value: v, Delete: v == nil})
	}
	data, err := proto.Marshal(redo)
	if err != nil {
		return err
	}
	rk := store.namespace.ChildString(redoKey)
	if err := store.raw.Put(ctx, rk, data); err != nil {
		return err
	}
	err = store.raw.Sync(ctx, rk)
	if err == nil {
		err = store.applyRedo(ctx, redo)
	}
	if err != nil {
		return errors.Join(err, store.raw.Put(ctx, store.appliedKey(), binary.AppendUvarint(nil, redo.Seq)))
	}
	return nil
}

func (store *Datastore) appliedKey() ds.Key { return store.namespace.ChildString(appliedKey) }

func (store *Datastore) applyRedo(ctx context.Context, redo *pb.Redo) error {
	b, err := batchOf(ctx, store.raw)
	if err != nil {
		return err
	}
	for _, w := range redo.GetWrites() {
		if w.GetDelete() {
			err = b.Delete(ctx, ds.RawKey(string(w.GetKey())))
		} else {
			v := w.GetValue()
			if v == nil {
				v = []byte{}
			}
			err = b.Put(ctx, ds.RawKey(string(w.GetKey())), v)
		}
		if err != nil {
			return err
		}
	}
	if err := b.Commit(ctx); err != nil {
		return err
	}
	return store.raw.Put(ctx, store.appliedKey(), binary.AppendUvarint(nil, redo.GetSeq()))
}

// replay applies the atomic commit a crash interrupted, if any.
func (store *Datastore) replay(ctx context.Context) error {
	data, err := store.raw.Get(ctx, store.namespace.ChildString(redoKey))
	if errors.Is(err, ds.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var redo pb.Redo
	if err := proto.Unmarshal(data, &redo); err != nil {
		return fmt.Errorf("error reading the interrupted commit: %w", err)
	}
	applied, err := readMillis(ctx, store.raw, store.appliedKey())
	if err != nil || redo.GetSeq() <= applied {
		return err
	}
	return store.applyRedo(ctx, &redo)
}

// A stateBase is the trees the journal is applied to: the state's elements
// and pending removals as they were when the journal was last emptied.
type stateBase struct {
	elems, pending cid.Cid
}

func (store *Datastore) baseKey() ds.Key { return store.namespace.ChildString(baseKey) }

func encodeBase(b stateBase) []byte {
	var out []byte
	for _, c := range []cid.Cid{b.elems, b.pending} {
		var cb []byte
		if c.Defined() {
			cb = c.Bytes()
		}
		out = append(binary.AppendUvarint(out, uint64(len(cb))), cb...)
	}
	return out
}

func decodeBase(v []byte) (stateBase, error) {
	var cs [2]cid.Cid
	for i := range cs {
		n, k := binary.Uvarint(v)
		if k <= 0 || uint64(len(v)-k) < n {
			return stateBase{}, errors.New("error decoding the state base")
		}
		if n > 0 {
			c, err := cid.Cast(v[k : k+int(n)])
			if err != nil {
				return stateBase{}, err
			}
			cs[i] = c
		}
		v = v[k+int(n):]
	}
	if len(v) != 0 {
		return stateBase{}, errors.New("error decoding the state base")
	}
	return stateBase{elems: cs[0], pending: cs[1]}, nil
}

func (store *Datastore) loadBase(ctx context.Context) (stateBase, error) {
	v, err := store.store.Get(ctx, store.baseKey())
	if errors.Is(err, ds.ErrNotFound) {
		return stateBase{}, nil
	}
	if err != nil {
		return stateBase{}, err
	}
	return decodeBase(v)
}

// setBase stages a move of the base to b in r's txn, taking the new trees'
// references before dropping the old ones'.
func (r *refs) setBase(ctx context.Context, from, to stateBase) error {
	if from == to {
		return nil
	}
	for _, c := range []cid.Cid{to.elems, to.pending} {
		if err := r.acquire(ctx, c); err != nil {
			return err
		}
	}
	for _, c := range []cid.Cid{from.elems, from.pending} {
		if err := r.release(ctx, c); err != nil {
			return err
		}
	}
	r.t.put(r.store.baseKey(), encodeBase(to))
	return nil
}
