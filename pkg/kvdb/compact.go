package kvdb

import (
	"context"
	"fmt"
	"strings"

	cid "github.com/ipfs/go-cid"
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
// What it does not bound is the vector: one entry per incarnation that ever
// wrote. Folding an entry away would need every holder to agree no delta of
// that incarnation is still in flight, and that is an agreement step this
// package does not take.
func (store *Datastore) Compact(ctx context.Context) (int, error) {
	store.mu.Lock()
	head, theirs, err := store.compactLocked(ctx)
	if err != nil || theirs == nil {
		store.mu.Unlock()
		return 0, err
	}
	n, err := store.purge(ctx, theirs)
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
func (store *Datastore) compactLocked(ctx context.Context) (Head, *causalContext, error) {
	hs, height := store.heads.List()
	if len(hs) == 0 {
		return Head{}, nil, nil
	}
	if folded, err := store.nothingToFold(ctx, hs); err != nil || folded {
		return Head{}, nil, err
	}

	var elems []*pb.Element
	err := scanStore(ctx, store.store, store.set.elemsPrefix(), func(k string, v []byte) error {
		el, err := store.set.parseElem(k, v)
		if err != nil {
			return err
		}
		elems = append(elems, &pb.Element{Key: el.key, Dot: el.dot.toPB(), Priority: el.priority, Value: el.value})
		return nil
	})
	if err != nil {
		return Head{}, nil, err
	}
	var pending []*pb.Element
	err = scanStore(ctx, store.store, store.set.pendingPrefix(), func(k string, _ []byte) error {
		key, d, err := store.set.parsePending(k)
		if err != nil {
			return err
		}
		pending = append(pending, &pb.Element{Key: key, Dot: d.toPB()})
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
		c, err := cid.Cast(v)
		if err != nil {
			return err
		}
		if _, isHead := store.heads.Get(c); !isHead {
			links = append(links, Head{Cid: c, Dot: d})
		}
		return nil
	})
	if err != nil {
		return Head{}, nil, err
	}

	d, prev := store.nextDot()
	root := &pb.Delta{
		Snapshot:   true,
		Dot:        d.toPB(),
		Prev:       prev,
		Priority:   height + 1,
		Parents:    parentsOf(links),
		Context:    store.cc.toPB(),
		Tombstones: pending,
	}
	rest := store.fillRoot(root, elems)

	var blocks []blockAt
	var chunkCids []cid.Cid
	for _, part := range store.chunk(rest) {
		// The chunk carries the snapshot's dot so that no two snapshots
		// ever share a chunk block: the index holds one dot per block.
		nd, err := makeNode(&pb.Delta{Elements: part, Dot: d.toPB()}, nil)
		if err != nil {
			return Head{}, nil, err
		}
		blocks = append(blocks, blockAt{nd, kindChunk})
		chunkCids = append(chunkCids, nd.Cid())
		root.Chunks = append(root.Chunks, nd.Cid().Bytes())
	}
	nd, err := makeNode(root, links)
	if err != nil {
		return Head{}, nil, err
	}
	blocks = append(blocks, blockAt{nd, kindSnapshot})
	if err := store.writeBlocks(ctx, d, blocks...); err != nil {
		return Head{}, nil, err
	}

	if _, _, err := store.mergeSnapshot(ctx, nd.Cid(), root, chunkCids, elems); err != nil {
		store.discardBlocks(ctx, append(chunkCids, nd.Cid())...)
		return Head{}, nil, fmt.Errorf("error merging snapshot: %w", err)
	}
	store.counter = d.counter
	return Head{Cid: nd.Cid(), Height: root.Priority, Dot: d}, contextFromPB(root.Context), nil
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
		kind, d, err := decodeBlock(v)
		if err != nil {
			return err
		}
		if _, isHead := headDots[d]; kind == kindDelta || !isHead {
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
