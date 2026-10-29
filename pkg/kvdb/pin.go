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

// maxStatePins bounds the states pinned at once: a pin keeps what a
// retired snapshot alone reaches, so the store holds at most that many old
// states besides the ones it has merged.
const maxStatePins = 16

var (
	errPinExpired  = errors.New("the state pin has expired")
	errTooManyPins = errors.New("too many states are pinned")
)

// A statePin keeps a state an index entry names, its StateRoot and every
// block under it, for a reader that is not a merge: one that reads an older
// snapshot while this replica moves on. It lasts a lease, never longer; a
// reader that needs more starts again from a newer snapshot, and nothing
// waits for it. Once the lease is over or the pin released, reading through
// it fails, whether or not the blocks are gone yet. A pin is recorded so
// that what it keeps is never lost track of; a restart releases it, its
// reader gone with the process.
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

// pinState pins the state root, which an indexed snapshot names, for lease.
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

// Get reads a block of the pinned state, while the pin lasts.
func (p *statePin) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	if p.released.Load() || p.store.times.up() >= p.expires {
		return nil, errPinExpired
	}
	return p.store.dagService.Get(ctx, c)
}

// Release ends the pin before its lease does.
func (p *statePin) Release(ctx context.Context) error {
	p.store.gcMu.Lock()
	defer p.store.gcMu.Unlock()
	return p.store.unpinLocked(ctx, p)
}

// unpinLocked releases p, and with it what only p reached. The caller
// holds gcMu.
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

// releaseExpiredLocked releases every pin whose lease is over. The caller
// holds gcMu.
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
