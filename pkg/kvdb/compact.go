package kvdb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// Compact folds this replica's whole state into a snapshot and drops every
// local block the snapshot covers, returning how many it dropped.
//
// A snapshot is a delta like any other: it has a dot of this incarnation and
// links the current heads, plus every parent a merged block links that this
// replica has not merged yet, so a replica reading it can still walk to
// those. What it carries is a state (see state.go): every live element with
// its own dot and priority, the pending removals, and this replica's causal
// context. Merging it is the same join as merging a delta (see merge.go), so
// it needs no coordination: snapshots made concurrently, from stale views,
// or of partly-walked histories all converge, and a removal the snapshot has
// seen stays removed against any branch that still carries the element.
//
// The state's trees are the last snapshot's with the records the journal
// names applied, so a snapshot writes only the tree nodes on the paths of
// what changed, and shares the rest with the snapshots before it. A reader
// fetches every node it does not hold before it merges anything, so a
// snapshot is never half-applied.
//
// The vector is bounded by time: an incarnation is forgotten once nothing
// merged about it is needed any more (see timebound.go), and compacting
// first forgets what has expired.
func (store *Datastore) Compact(ctx context.Context) (int, error) {
	return store.compact(ctx, false)
}

// compact compacts; forced, it writes a snapshot even when every block held
// is a head snapshot, to replace one that has grown old.
func (store *Datastore) compact(ctx context.Context, force bool) (int, error) {
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return 0, ErrClosed
	}
	// A rebase puts back what this replica wrote before the judge's state
	// can refute it: nothing is carried in a snapshot until that state is
	// walked.
	if store.rebuilding != 0 {
		store.compactDue = true
		store.mu.Unlock()
		return 0, nil
	}
	if _, err := store.expireLocked(ctx); err != nil {
		store.mu.Unlock()
		return 0, err
	}
	head, theirs, err := store.compactLocked(ctx, force)
	if err == nil && theirs != nil {
		err = store.carried(ctx)
	}
	if err != nil || theirs == nil {
		store.mu.Unlock()
		if _, rerr := store.reclaim(ctx); err == nil {
			err = rerr
		}
		return 0, err
	}
	covered, err := store.purge(ctx, theirs)
	var ancient []cid.Cid
	if err == nil {
		ancient, err = store.purgeAncient(ctx)
	}
	store.mu.Unlock()
	n, derr := store.dropTaken(ctx, covered)
	old, aerr := store.dropTaken(ctx, ancient)
	n += old
	_, rerr := store.reclaim(ctx)
	if err = errors.Join(err, derr, aerr, rerr); err != nil {
		return n, err
	}
	if err := store.broadcastHeads(ctx, []Head{head}); err != nil {
		return n, fmt.Errorf("error broadcasting snapshot head: %w", err)
	}
	return n, nil
}

// compactLocked writes and merges the snapshot. It returns no context when
// there is nothing to fold: the only block held is the snapshot that is the
// only head. The caller holds store.mu.
func (store *Datastore) compactLocked(ctx context.Context, force bool) (Head, *causalContext, error) {
	if store.closed {
		return Head{}, nil, ErrClosed
	}
	hs, height := store.heads.List()
	// Elements with no block to carry them, put back after a rebase, are
	// carried by the snapshot like any others.
	if len(hs) == 0 && store.liveCount == 0 {
		return Head{}, nil, nil
	}
	if !force {
		if folded, err := store.nothingToFold(ctx, hs); err != nil || folded {
			return Head{}, nil, err
		}
	}

	links := append([]Head(nil), hs...)
	missing := store.missingPrefix()
	err := scanStore(ctx, store.store, missing, func(k string, v []byte) error {
		d, err := parseDotSegment(strings.TrimPrefix(k, missing.String()+"/"))
		if err != nil {
			return err
		}
		l, err := decodeLink(d, v)
		if err != nil {
			return err
		}
		if _, isHead := store.heads.Get(l.cid); !isHead {
			links = append(links, Head{Cid: l.cid, Dot: d, At: l.at, TTL: l.ttl})
		}
		return nil
	})
	if err != nil {
		return Head{}, nil, err
	}

	ov, state, err := store.buildState(ctx)
	if err != nil {
		return Head{}, nil, err
	}
	// The compaction is a session (see gc.go): what the roots reach it
	// reuses, and what it writes is an intent first, until the merge below
	// publishes it.
	sess := store.gc.compaction
	store.gcMu.Lock()
	store.beginSession(sess)
	store.gcMu.Unlock()
	defer func() {
		if err := store.endSession(ctx, sess); err != nil {
			store.logger.Warnf("error removing what a compaction wrote: %s", err)
		}
	}()
	store.gcMu.Lock()
	fresh, err := ov.fresh([]cid.Cid{state}, func(c cid.Cid) (bool, error) {
		if !store.gc.member(keyOf(c)) {
			return false, nil
		}
		sess.claim(&store.gc, c, nil)
		return true, nil
	})
	store.gcMu.Unlock()
	if err != nil {
		return Head{}, nil, err
	}
	written := make([]cid.Cid, len(fresh))
	for i, nd := range fresh {
		written[i] = nd.Cid()
	}
	if _, err := store.intend(ctx, sess, written, nil); err != nil {
		return Head{}, nil, err
	}
	if err := store.dagService.AddMany(ctx, fresh); err != nil {
		return Head{}, nil, fmt.Errorf("error writing the snapshot's state: %w", err)
	}

	// Its own entry expired, with nothing of its own left to refresh it, this
	// incarnation writes no more, as idleRetire does for a write: a peer
	// whose clock runs ahead may have forgotten it already, though this
	// replica forgets it only a margin later.
	if k := store.cc.inc[store.replica]; store.counter != 0 && (k == nil || k.expires <= store.clock()) {
		if err := store.drawIncarnation(); err != nil {
			return Head{}, nil, err
		}
	}
	d, prev := store.nextDot()
	root := store.stamped(&pb.Delta{
		Snapshot: true,
		Dot:      d.toPB(),
		Prev:     prev,
		Priority: height + 1,
		Parents:  parentsOf(links),
		State:    state.Bytes(),
	})
	sr, err := decodeStateRoot(ov.added[state])
	if err != nil {
		return Head{}, nil, err
	}
	carryState(root, sr, fresh)
	nd, err := makeNode(root, links)
	if err != nil {
		return Head{}, nil, err
	}
	if err := store.writeBlocks(ctx, block{kind: kindSnapshot, dot: d, at: root.At, ttl: root.Ttl}, nd); err != nil {
		return Head{}, nil, err
	}

	_, theirs, err := store.mergeSnapshot(ctx, nd.Cid(), root, ov, sess)
	if err != nil {
		store.discardBlocks(ctx, nd.Cid())
		return Head{}, nil, fmt.Errorf("error merging snapshot: %w", err)
	}
	store.gcMu.Lock()
	store.gc.recent.nodes = ov.added
	store.gcMu.Unlock()
	store.counter = d.counter
	store.lastWrite = store.times.up()
	store.compactDue = false
	return Head{Cid: nd.Cid(), Height: root.Priority, Dot: d, At: root.At, TTL: root.Ttl}, theirs, nil
}

// buildState makes the trees of the state this replica holds, in an
// overlay over its DAG: the base's with every record the journal names
// brought up to date, and the context's. It returns the StateRoot's CID.
// The caller holds store.mu.
func (store *Datastore) buildState(ctx context.Context) (*overlay, cid.Cid, error) {
	ov := newOverlay(store.dagService)
	elems := store.tree(ov, store.base.elems).Edit()
	err := scanStore(ctx, store.store, store.journal.elemsPrefix(), func(k string, _ []byte) error {
		key, d, err := store.journal.parseElemMark(k)
		if err != nil {
			return err
		}
		v, err := store.store.Get(ctx, store.set.elemKey(key, d))
		switch {
		case errors.Is(err, ds.ErrNotFound):
			err = elems.Delete(ctx, elemTreeKey(key, d))
		case err == nil:
			var el element
			if el, err = decodeMarker(v); err == nil {
				el.key, el.dot = key, d
				err = elems.Put(ctx, elemTreeKey(key, d), elemValue(el))
			}
		}
		return err
	})
	if err != nil {
		return nil, cid.Undef, err
	}
	pending := store.tree(ov, store.base.pending).Edit()
	err = scanStore(ctx, store.store, store.journal.pendingPrefix(), func(k string, _ []byte) error {
		b, key, d, err := store.journal.parsePendingMark(k)
		if err != nil {
			return err
		}
		v, err := store.store.Get(ctx, store.set.pendingKey(key, d))
		if err != nil && !errors.Is(err, ds.ErrNotFound) {
			return err
		}
		if err == nil {
			at, ttl, err := decodeStamps(v)
			if err != nil {
				return err
			}
			if r := (removal{key: key, dot: d, at: at, ttl: ttl}); bucket(r.expires()) == b {
				return pending.Put(ctx, pendingTreeKey(r), pendingValue(r))
			}
		}
		return pending.Delete(ctx, pendingTreeKeyIn(b, key, d))
	})
	if err != nil {
		return nil, cid.Undef, err
	}
	et, err := elems.Tree(ctx)
	if err != nil {
		return nil, cid.Undef, err
	}
	pt, err := pending.Tree(ctx)
	if err != nil {
		return nil, cid.Undef, err
	}
	cc, err := contextTree(ctx, ov, store.cc)
	if err != nil {
		return nil, cid.Undef, err
	}
	nd, err := stateRoot{elems: et.Root(), pending: pt.Root(), ctx: cc.Root()}.node()
	if err != nil {
		return nil, cid.Undef, err
	}
	return ov, nd.Cid(), ov.Add(ctx, nd)
}

// nothingToFold reports whether every block the store holds belongs to a
// snapshot that is a head, and no merge left a state those snapshots do not
// hold (compactDue). Folding concurrent snapshots into one more would
// free nothing, and replicas that each compacted would otherwise answer every
// snapshot they merge with another one.
func (store *Datastore) nothingToFold(ctx context.Context, hs []Head) (bool, error) {
	if store.compactDue {
		return false, nil
	}
	headDots := map[dot]struct{}{}
	for _, h := range hs {
		headDots[h.Dot] = struct{}{}
	}
	work := false
	prefix := store.blocksPrefix()
	err := scanStore(ctx, store.store, prefix, func(k string, v []byte) error {
		if store.isTaken(prefix, k) {
			return nil
		}
		b, err := decodeBlock(v)
		if err != nil {
			return err
		}
		if _, isHead := headDots[b.dot]; b.kind == kindDelta || !isHead {
			work = true
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if work {
		return false, nil
	}
	missing, err := countPrefix(ctx, store.store, store.missingPrefix())
	return missing == 0, err
}
