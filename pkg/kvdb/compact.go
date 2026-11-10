package kvdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"github.com/taubyte/tau/pkg/pebbleds"
)

// Compact folds the whole state into a snapshot and drops the local blocks it
// covers, returning how many. A snapshot is a delta carrying a state (see
// state.go), merged by the same join as a delta, so snapshots made
// concurrently or from stale views converge without coordination. Its trees
// share every unchanged node with the snapshot before, and a reader fetches
// all it lacks before merging, so a snapshot is never half-applied.
func (store *Datastore) Compact(ctx context.Context) (int, error) {
	return store.compact(ctx, false)
}

// Forced, it writes a snapshot even when the only blocks held are head
// snapshots, to replace an old one.
func (store *Datastore) compact(ctx context.Context, force bool) (int, error) {
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return 0, ErrClosed
	}
	// Nothing is carried in a snapshot until a rebase's judge state is walked.
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

// It returns no context when the only block held is the snapshot that is the
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
		d, err := store.replicas.parseDotSegment(strings.TrimPrefix(k, missing.String()+"/"))
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
	// A compaction is a gc session: it reuses what the roots reach, and what it
	// writes is an intent until the merge below publishes it.
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
	if err := store.addBlocks(ctx, fresh); err != nil {
		return Head{}, nil, fmt.Errorf("error writing the snapshot's state: %w", err)
	}

	// With its own entry expired and nothing left to refresh it, this incarnation
	// stops writing, as idleRetire does: a peer whose clock runs ahead may have
	// forgotten it.
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

// buildState makes the state's trees in an overlay over the DAG: the base's
// with the journal's records applied, and the context's. The caller holds
// store.mu.
func (store *Datastore) buildState(ctx context.Context) (*overlay, cid.Cid, error) {
	ov := newOverlay(store.dagService)
	b := &store.build
	b.reset()
	elems := store.tree(ov, store.base.elems).Edit()
	err := scanBytes(ctx, store.store, store.journal.elemsPrefix(), func(k, _ []byte) error {
		rest := k[len(store.journal.jElems):]
		key, d, err := b.elemMark(rest)
		if err != nil {
			return err
		}
		b.look = append(append(b.look[:0], store.journal.elems...), rest...)
		found := false
		err = store.view(ctx, b.look, func(v []byte) error {
			found = true
			el, err := decodeMarker(v)
			if err != nil {
				return err
			}
			el.key, el.dot = key, d
			return elems.Put(ctx, b.keep(appendElemTreeKey(b.buf(), key, d)), b.keep(appendElemValue(b.buf(), el)))
		})
		if errors.Is(err, ds.ErrNotFound) && !found {
			b.look = appendElemTreeKey(b.look[:0], key, d)
			return elems.Delete(ctx, b.look)
		}
		return err
	})
	if err != nil {
		return nil, cid.Undef, err
	}
	pending := store.tree(ov, store.base.pending).Edit()
	err = scanBytes(ctx, store.store, store.journal.pendingPrefix(), func(k, _ []byte) error {
		rest := k[len(store.journal.jPending):]
		bk, key, d, err := b.pendingMark(rest)
		if err != nil {
			return err
		}
		b.look = append(append(b.look[:0], store.journal.pending...), rest[counterDigits+1:]...)
		put := false
		err = store.view(ctx, b.look, func(v []byte) error {
			at, ttl, err := decodeStamps(v)
			if err != nil {
				return err
			}
			if r := (removal{key: key, dot: d, at: at, ttl: ttl}); bucket(r.expires()) == bk {
				put = true
				return pending.Put(ctx, b.keep(appendPendingTreeKey(b.buf(), bk, key, d)), b.keep(appendPendingValue(b.buf(), r)))
			}
			return nil
		})
		if err != nil && !errors.Is(err, ds.ErrNotFound) {
			return err
		}
		if put {
			return nil
		}
		b.look = appendPendingTreeKey(b.look[:0], bk, key, d)
		return pending.Delete(ctx, b.look)
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

// nothingToFold reports whether every block held belongs to a head snapshot
// and no merge left a state they lack. Folding them again frees nothing, and
// replicas would answer every snapshot with another.
func (store *Datastore) nothingToFold(ctx context.Context, hs []Head) (bool, error) {
	if store.compactDue {
		return false, nil
	}
	headDots := map[dot]struct{}{}
	for _, h := range hs {
		headDots[h.Dot] = struct{}{}
	}
	prefix := store.blocksPrefix()
	err := scanBytes(ctx, store.store, prefix, func(k, v []byte) error {
		if store.isTaken(prefix, stringOf(k)) {
			return nil
		}
		b, err := store.replicas.decodeBlock(v)
		if err != nil {
			return err
		}
		if _, isHead := headDots[b.dot]; b.kind == kindDelta || !isHead {
			return errFound
		}
		return nil
	})
	if errors.Is(err, errFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	missing, err := anyUnder(ctx, store.store, store.missingPrefix())
	return !missing, err
}

type stateBuild struct {
	arena, look, key []byte
	replicas         *internTable
}

const keptBuild = 1 << 20

func (b *stateBuild) reset() {
	if cap(b.arena) > keptBuild {
		b.arena = nil
	}
	b.arena = b.arena[:0]
}

// buf is where the next kept bytes are appended; keep claims them.
func (b *stateBuild) buf() []byte { return b.arena }

func (b *stateBuild) keep(x []byte) []byte {
	start := len(b.arena)
	b.arena = x
	return x[start:len(x):len(x)]
}

// rest is <key segment>/<dot segment>. The key is in b's scratch until the
// next mark.
func (b *stateBuild) elemMark(rest []byte) (string, dot, error) {
	i := bytes.IndexByte(rest, '/')
	if i < 1 {
		return "", dot{}, fmt.Errorf("bad journal key %q", rest)
	}
	key, err := b.keySegment(rest[:i])
	if err != nil {
		return "", dot{}, err
	}
	d, err := b.replicas.parseDotSegment(stringOf(rest[i+1:]))
	return key, d, err
}

// rest is <bucket>/<dot segment>/<key segment>.
func (b *stateBuild) pendingMark(rest []byte) (uint64, string, dot, error) {
	if len(rest) < counterDigits+2 || rest[counterDigits] != '/' {
		return 0, "", dot{}, fmt.Errorf("bad journal key %q", rest)
	}
	bk, err := strconv.ParseUint(stringOf(rest[:counterDigits]), 10, 64)
	if err != nil {
		return 0, "", dot{}, err
	}
	tail := rest[counterDigits+1:]
	i := bytes.IndexByte(tail, '/')
	if i < 1 {
		return 0, "", dot{}, fmt.Errorf("bad journal key %q", rest)
	}
	d, err := b.replicas.parseDotSegment(stringOf(tail[:i]))
	if err != nil {
		return 0, "", dot{}, err
	}
	key, err := b.keySegment(tail[i+1:])
	return bk, key, d, err
}

func (b *stateBuild) keySegment(seg []byte) (string, error) {
	if len(seg) == 0 || seg[0] != 'k' {
		return "", fmt.Errorf("bad key segment %q", seg)
	}
	var err error
	if b.key, err = segEncoding.AppendDecode(b.key[:0], seg[1:]); err != nil {
		return "", fmt.Errorf("bad key segment %q: %w", seg, err)
	}
	return stringOf(b.key), nil
}

// The value is lent to fn for the call.
func (store *Datastore) view(ctx context.Context, k []byte, fn func(v []byte) error) error {
	if s, ok := below(store.store).(*pebbleds.Store); ok {
		return s.View(k, fn)
	}
	v, err := store.store.Get(ctx, ds.RawKey(string(k)))
	if err != nil {
		return err
	}
	return fn(v)
}
