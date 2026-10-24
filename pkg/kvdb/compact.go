package kvdb

import (
	"context"
	"fmt"
	"strings"

	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"google.golang.org/protobuf/proto"
)

// Compact folds this replica's whole state into a snapshot and drops every
// local block the snapshot covers, returning how many it dropped.
//
// A snapshot is a delta like any other: it has a dot of this incarnation and
// links the current heads, plus every parent a merged block links that this
// replica has not merged yet, so a replica reading it can still walk to
// those. What it carries is a state: every live element with its own dot and
// priority, the pending removals, and this replica's causal context.
// Merging it is the same join as merging a delta (see merge.go), so it needs
// no coordination: snapshots made concurrently, from stale views, or of
// partly-walked histories all converge, and a removal the snapshot has seen
// stays removed against any branch that still carries the element.
//
// Elements that do not fit in the root block go to chunk blocks the root
// names; a reader fetches them all before it merges anything, so a snapshot
// is never half-applied.
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
	if _, err := store.expireLocked(ctx); err != nil {
		store.mu.Unlock()
		return 0, err
	}
	head, theirs, err := store.compactLocked(ctx, force)
	if err != nil || theirs == nil {
		store.mu.Unlock()
		return 0, err
	}
	n, err := store.purge(ctx, theirs)
	if err == nil {
		var old int
		old, err = store.purgeAncient(ctx)
		n += old
	}
	store.mu.Unlock()
	if err != nil {
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

	var elems []*pb.Element
	err := scanStore(ctx, store.store, store.set.elemsPrefix(), func(k string, v []byte) error {
		el, err := store.set.parseElem(k, v)
		if err != nil {
			return err
		}
		elems = append(elems, &pb.Element{Key: el.key, Dot: el.dot.toPB(), Priority: el.priority, Value: el.value, At: el.at, Ttl: el.ttl})
		return nil
	})
	if err != nil {
		return Head{}, nil, err
	}
	var pending []*pb.Element
	err = scanStore(ctx, store.store, store.set.pendingPrefix(), func(k string, v []byte) error {
		r, err := store.set.parsePending(k, v)
		if err != nil {
			return err
		}
		pending = append(pending, &pb.Element{Key: r.key, Dot: r.dot.toPB(), At: r.at, Ttl: r.ttl})
		return nil
	})
	if err != nil {
		return Head{}, nil, err
	}

	links := append([]Head(nil), hs...)
	missing := store.missingPrefix()
	err = scanStore(ctx, store.store, missing, func(k string, v []byte) error {
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

	d, prev := store.nextDot()
	root := store.stamped(&pb.Delta{
		Snapshot:   true,
		Dot:        d.toPB(),
		Prev:       prev,
		Priority:   height + 1,
		Parents:    parentsOf(links),
		Context:    store.cc.toPB(),
		Tombstones: pending,
	})
	rest := store.fillRoot(root, elems)

	var nodes []ipld.Node
	var chunkCids []cid.Cid
	for _, part := range store.chunk(rest) {
		// The chunk carries the snapshot's dot so that no two snapshots
		// ever share a chunk block: the index holds one dot per block.
		nd, err := makeNode(&pb.Delta{Elements: part, Dot: d.toPB(), At: root.At, Ttl: root.Ttl}, nil)
		if err != nil {
			return Head{}, nil, err
		}
		nodes = append(nodes, nd)
		chunkCids = append(chunkCids, nd.Cid())
		root.Chunks = append(root.Chunks, nd.Cid().Bytes())
	}
	nd, err := makeNode(root, links)
	if err != nil {
		return Head{}, nil, err
	}
	nodes = append(nodes, nd)
	if err := store.writeBlocks(ctx, block{kind: kindSnapshot, dot: d, at: root.At, ttl: root.Ttl}, nodes...); err != nil {
		return Head{}, nil, err
	}

	if _, _, err := store.mergeSnapshot(ctx, nd.Cid(), root, chunkCids, elems); err != nil {
		store.discardBlocks(ctx, append(chunkCids, nd.Cid())...)
		return Head{}, nil, fmt.Errorf("error merging snapshot: %w", err)
	}
	store.counter = d.counter
	store.lastWrite = store.times.up()
	return Head{Cid: nd.Cid(), Height: root.Priority, Dot: d, At: root.At, TTL: root.Ttl}, contextFromPB(root.Context), nil
}

// nothingToFold reports whether every block the store holds belongs to a
// snapshot that is a head. Folding concurrent snapshots into one more would
// free nothing, and replicas that each compacted would otherwise answer every
// snapshot they merge with another one.
func (store *Datastore) nothingToFold(ctx context.Context, hs []Head) (bool, error) {
	headDots := map[dot]struct{}{}
	for _, h := range hs {
		headDots[h.Dot] = struct{}{}
	}
	work := false
	err := scanStore(ctx, store.store, store.blocksPrefix(), func(_ string, v []byte) error {
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

// fillRoot puts as many elements in the root as fit under MaxBatchDeltaSize
// and returns the rest.
func (store *Datastore) fillRoot(root *pb.Delta, elems []*pb.Element) []*pb.Element {
	size := proto.Size(root)
	for i, e := range elems {
		es := proto.Size(e) + 4
		if size+es > store.opts.MaxBatchDeltaSize && i > 0 {
			root.Elements = elems[:i]
			return elems[i:]
		}
		size += es
	}
	root.Elements = elems
	return nil
}

// chunk splits elements into parts of at most MaxBatchDeltaSize each; an
// element bigger than that is a part of its own.
func (store *Datastore) chunk(elems []*pb.Element) [][]*pb.Element {
	var parts [][]*pb.Element
	var cur []*pb.Element
	size := 0
	for _, e := range elems {
		es := proto.Size(e) + 4
		if len(cur) > 0 && size+es > store.opts.MaxBatchDeltaSize {
			parts = append(parts, cur)
			cur, size = nil, 0
		}
		cur = append(cur, e)
		size += es
	}
	if len(cur) > 0 {
		parts = append(parts, cur)
	}
	return parts
}
