package kvdb

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
)

// A pin keeps what a retired snapshot alone reaches, so this bounds the old
// states held besides the merged ones.
const maxStatePins = 16

var (
	errPinExpired  = errors.New("the state pin has expired")
	errTooManyPins = errors.New("too many states are pinned")
)

// A statePin keeps a state an index entry names, its StateRoot and every block
// under it, for a reader that is not a merge, for a lease and never longer.
// Once the lease ends or it is released, reading through it fails whether or
// not the blocks are gone. It is recorded so what it keeps is never lost track
// of; a restart releases it.
type statePin struct {
	store    *Datastore
	id       uint64
	root     cid.Cid
	expires  time.Duration
	released atomic.Bool
}

func (store *Datastore) pinKey(id uint64) ds.Key {
	b := make([]byte, 0, len(store.pre.pins)+1+counterDigits)
	return dsKey(appendCounter(append(append(b, store.pre.pins...), '/'), id))
}

func (store *Datastore) pinState(ctx context.Context, root cid.Cid, lease time.Duration) (*statePin, error) {
	store.gcMu.Lock()
	defer store.gcMu.Unlock()
	if err := store.drainLocked(ctx); err != nil {
		return nil, err
	}
	if len(store.gc.pins) >= maxStatePins {
		return nil, errTooManyPins
	}
	r := store.gc.roots[keyOf(root)]
	if r == nil || r.kind != stateRootKind {
		return nil, fmt.Errorf("pinning %s: not a state this replica holds", root)
	}
	p := &statePin{store: store, id: store.gc.nextPin, root: root, expires: store.times.up() + lease}
	store.gc.nextPin++
	k := store.pinKey(p.id)
	if err := store.store.Put(ctx, k, root.Bytes()); err != nil {
		return nil, err
	}
	if err := store.store.Sync(ctx, k); err != nil {
		return nil, err
	}
	ch := store.changeRoots()
	ch.addState(root, r.sr)
	ch.apply()
	store.gc.pins[p.id] = p
	return p, nil
}

func (p *statePin) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	if p.released.Load() || p.store.times.up() >= p.expires {
		return nil, errPinExpired
	}
	return p.store.dagService.Get(ctx, c)
}

func (p *statePin) Release(ctx context.Context) error {
	p.store.gcMu.Lock()
	defer p.store.gcMu.Unlock()
	return p.store.unpinLocked(ctx, p)
}

// The caller holds gcMu.
func (store *Datastore) unpinLocked(ctx context.Context, p *statePin) error {
	if p.released.Load() {
		return nil
	}
	t := newTxn(store.store)
	t.del(store.pinKey(p.id))
	ch := store.changeRoots()
	ch.remove(p.root)
	if err := ch.prepare(ctx, t); err != nil {
		return err
	}
	if err := store.commitAtomic(ctx, t); err != nil {
		return err
	}
	ch.apply()
	p.released.Store(true)
	delete(store.gc.pins, p.id)
	return nil
}

// The caller holds gcMu.
func (store *Datastore) releaseExpiredLocked(ctx context.Context) error {
	now := store.times.up()
	for _, p := range store.gc.pins {
		if now >= p.expires {
			if err := store.unpinLocked(ctx, p); err != nil {
				return err
			}
		}
	}
	return nil
}
