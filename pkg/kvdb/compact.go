package kvdb

import (
	"context"
	"fmt"

	cid "github.com/ipfs/go-cid"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// Compact rewrites a named DAG's live state as one or more small "snapshot"
// generation blocks and purges the DAG history that is now redundant with
// them, returning the number of DAG blocks purged. Compacting the default
// (unnamed) DAG is Compact(ctx, "").
//
// # What it does
//
// Compact walks the named DAG's history locally (never touching the
// network) starting from its current heads, exactly like PurgeDAG. For every
// key touched by that history it computes the same "best value" that
// findBestValue would (highest priority, ties broken by the greatest value)
// and, separately, which of the key's tombstones must be carried forward
// (see below). It then builds one or more new DAG blocks -- "snapshot"
// nodes, marked as such via Delta.IsSnapshot -- containing exactly that
// live state, linked to all the heads it covers, and merges them locally
// (replacing the covered heads with the new snapshot head(s)). Finally it
// purges the old DAG blocks and their set entries, and broadcasts the new
// snapshot head(s).
//
// A fresh replica that later fetches this DAG only ever needs the snapshot
// block(s): it never descends into a snapshot node's links (see
// processNode/repairDAG), which is what makes the old history safe to
// purge. Per-element priorities are preserved exactly (see Item G3), so a
// snapshot changes nothing about which value wins a concurrent write for a
// still-being-replicated key.
//
// # Two-generation tombstone rule
//
// A tombstone cannot simply be dropped once its target's block is purged:
// a lagging replica that has not yet processed the original history may
// still be holding the (soon to be dead) element and needs the tombstone to
// eventually reach it. So a tombstone is carried into a snapshot generation
// if either (a) the block of the element it targets is being purged in this
// very Compact call (the lagging replica may hold that element and needs
// the kill now), or (b) that element's marker is still present locally at
// all (it is alive -- possibly reintroduced by a different dagName sharing
// the key -- and the kill must not be lost). A tombstone matching neither
// condition already did its job in a previous generation and is dropped.
//
// A carried tombstone's marker is written with an alias pointing at the
// snapshot block that carries it (see putTombs), mirroring a re-homed
// element's marker (S2): in the common carry case (a), the tombstone's
// target id is the very id being purged by THIS SAME Compact call, so
// without an alias the purge below would immediately delete the marker it
// just (re)wrote, and a later-arriving element for that id -- exactly the
// kind of divergent write Compact must now tolerate -- would wrongly
// resurrect the key. A plain (non-carried) tombstone is unaffected: it
// keeps the alias-less, nil-valued marker it has always had.
//
// # Coordination-free
//
// Compact needs no coordination with anything else writing to this
// dagName, same as every other operation this package exposes: concurrent
// Puts, Deletes, and Compacts -- whether they observe the same view or
// have diverged (different, unsynced sets of prior operations), and
// regardless of the order replicas eventually exchange them in -- converge
// to exactly the state the uncompacted history would have produced. This
// holds because every element keeps its ORIGINAL id for its entire life,
// including across compaction: a snapshot changes which block hosts an
// element's storage (recorded as an alias on its marker, see putElems) but
// never changes the element's identity, so a tombstone that targets an id
// it observed before compaction ran keeps covering that exact element
// afterwards, in every generation, on every replica -- compaction is pure
// representation GC, invisible to the CRDT semantics. Local
// Put/Delete/Batch.Commit calls on this same Datastore serialize against
// Compact via compactMux (see addDAGNode) as an implementation detail, not
// because concurrent remote writes would be unsafe.
//
// A few things remain worth noting, though none of them are correctness
// requirements:
//
//   - Upgrade ordering. All replicas that may ever see this dagName's
//     history should run a version of this package that understands
//     snapshot deltas (this one) before Compact is called anywhere. This is
//     the same rollout rule as any wire-format addition: an old-code
//     replica does not know to avoid descending into a snapshot node's
//     links, and would try to fetch purged blocks (failing) while also
//     mis-attributing every element's priority to the snapshot's own
//     height.
//   - Compact when reasonably synced. Compaction works correctly regardless
//     of how far behind other replicas are: a replica that is very far
//     behind still converges correctly on reconnect (the snapshot resupplies
//     live values and carries the tombstones it needs). Running it while
//     replicas are reasonably caught up is purely an efficiency
//     consideration -- it maximizes how much history gets folded away in one
//     pass, rather than repeating the exercise generation after generation.
//
// # Purged-block bookkeeping trade-off
//
// Unlike PurgeDAG, Compact deliberately *keeps* the processed-block markers
// for the CIDs it purges (instead of deleting them). They are tiny, and
// their presence means that a stale rebroadcast of now-purged history (from
// a lagging replica that has not yet reconnected) is recognized as already
// processed and skipped, rather than triggering a doomed attempt to fetch a
// block that no longer exists.
//
// # Receiver-side reclamation
//
// Compact itself only reclaims space on the replica that runs it, and a
// fresh replica never downloads the purged history at all. A long-lived
// replica that had already merged the covered history before the snapshot
// arrived, however, starts out keeping its local copy of that history (set
// markers and blocks): merging a snapshot does not, by itself, purge
// anything on the receiving side.
//
// That residue is what Options.ReclaimOnSnapshot (default: on) reclaims
// automatically. Every snapshot delta Compact produces carries generation
// metadata -- snapshotTotal (the number of sibling snapshot nodes Compact
// split this generation into) and snapshotId (an id shared by all of them,
// deterministically derived from their covered heads). When a replica
// merges one of these siblings (processNode, triggered from the normal DAG
// walk -- never from a local publish, and never from Compact's own
// processing of the nodes it just wrote, which would be a redundant
// double-reclaim), it increments a per-generation counter. Once that
// counter reaches snapshotTotal -- every sibling has now been merged, so
// every live key's snapshot marker is in place -- it purges its own copy of
// the covered history exactly as Compact would (reclaimCovered), and
// records a done marker so the generation is never reclaimed twice. This
// counter-driven wait is what avoids a visibility gap: reclaiming before
// every sibling has merged could transiently drop a key whose surviving
// value lives in a not-yet-merged sibling.
//
// This is a best-effort, soft-failure feature: any error along that path
// (bumping the counter, walking, purging, writing the done marker) is
// logged and otherwise swallowed -- it never fails the merge that triggered
// it and never marks the datastore dirty. The main gap this leaves is a
// crash between the merge being recorded and the counter increment, which
// permanently loses one count for that generation (accepted as a
// consequence of not making local Compact/merge state part of the same
// transaction). Legacy snapshots produced by a pre-metadata version of this
// package (empty snapshotId/snapshotTotal) are never auto-reclaimed either.
//
// Datastore.ReclaimCompacted is the manual/recovery path for all of the
// above: crash-missed generations, ReclaimOnSnapshot=false deployments, and
// legacy snapshots (for which it derives the same id Compact would have,
// from the snapshot node's covered-heads links). It is safe to call at any
// time and is idempotent (already-reclaimed generations are skipped via the
// same done marker).
func (store *Datastore) Compact(ctx context.Context, dagName string) (int, error) {
	store.compactMux.Lock()
	defer store.compactMux.Unlock()

	coveredHeads, maxHeight, err := store.heads.ListDAG(ctx, dagName)
	if err != nil {
		return 0, fmt.Errorf("error listing heads: %w", err)
	}
	if len(coveredHeads) == 0 {
		return 0, nil
	}

	headCIDs := make([]cid.Cid, len(coveredHeads))
	for i, h := range coveredHeads {
		headCIDs[i] = h.Cid
	}

	dagCIDSet, setKeys, _, err := store.walkProcessedDAG(ctx, headCIDs)
	if err != nil {
		return 0, fmt.Errorf("error walking DAG: %w", err)
	}

	newPriority := maxHeight + 1

	// Computed in a single pass over the elems/tombs namespaces (grouped by
	// key, in deterministic key order) rather than one datastore Query per
	// touched key -- see compactSnapshotState's doc comment for why that
	// matters.
	elements, tombstones, err := store.set.compactSnapshotState(ctx, setKeys, dagCIDSet)
	if err != nil {
		return 0, fmt.Errorf("error computing snapshot state: %w", err)
	}

	// snapshotID identifies this compaction generation: deterministic (no
	// clock/rand) so that independently re-running Compact over the exact
	// same covered heads always derives the same id. Shared by
	// ReclaimCompacted (R6) for legacy-snapshot derivation.
	snapshotID := snapshotGenerationID(headCIDs)

	snapshotDeltas := store.buildSnapshotDeltas(dagName, newPriority, snapshotID, elements, tombstones)

	// Apply all snapshot nodes locally before purging anything: the merge
	// path (findBestValue et al., inside set.Merge) may still need to
	// read the old blocks while processing them.
	ng := &crdtNodeGetter{NodeGetter: store.dagService}
	snapHeads := make([]Head, 0, len(snapshotDeltas))
	for _, d := range snapshotDeltas {
		node, err := store.putBlock(ctx, coveredHeads, d)
		if err != nil {
			return 0, fmt.Errorf("error writing snapshot block: %w", err)
		}

		snapHead := Head{Cid: node.Cid()}
		snapHead.Height = newPriority
		snapHead.DAGName = dagName

		if _, err := store.processNode(ctx, ng, snapHead, d, node, false); err != nil {
			return 0, fmt.Errorf("error processing snapshot block: %w", err)
		}
		snapHeads = append(snapHeads, snapHead)
	}

	// Purge the old history now that it has been fully folded into the
	// snapshot(s). The new snapshot blocks are not part of dagCIDSet, so
	// this cannot touch the markers just written above.
	for key, kind := range setKeys {
		if err := store.set.purgeKeyBlocks(ctx, key, dagCIDSet, kind&dagWalkElem != 0, kind&dagWalkTomb != 0); err != nil {
			return 0, fmt.Errorf("error purging blocks for key %q: %w", key, err)
		}
	}

	dagCIDs := make([]cid.Cid, 0, len(dagCIDSet))
	for c := range dagCIDSet {
		dagCIDs = append(dagCIDs, c)
	}

	// Unlike PurgeDAG, we intentionally do NOT delete the processed-block
	// markers for the purged CIDs here -- see the doc comment above.
	if err := store.dagService.RemoveMany(ctx, dagCIDs); err != nil {
		return 0, fmt.Errorf("error removing purged blocks: %w", err)
	}

	// Heads were already replaced by the snapshot head(s) while
	// processing the snapshot nodes above; nothing left to delete there.

	if err := store.broadcastHeads(ctx, snapHeads); err != nil {
		return 0, fmt.Errorf("error broadcasting snapshot heads: %w", err)
	}

	return len(dagCIDs), nil
}

// buildSnapshotDeltas packs elements and tombstones into one or more
// snapshot Deltas (Delta.IsSnapshot() == true), greedily splitting into
// sibling deltas whenever adding the next item would push a delta's
// marshaled size past MaxBatchDeltaSize. Every returned delta independently
// carries snapshot=true, dagName, priority and snapshotId: all of them are
// meant to be linked to the exact same covered heads and become siblings
// heads that collapse together on the next regular write to the DAG.
// Once every sibling is known, snapshotTotal (len(result)) is stamped on all
// of them -- this happens after the size-based split, so the extra few
// bytes it adds can in principle push a delta slightly over
// MaxBatchDeltaSize; that cap is already soft (see the single-oversized-item
// case below), so this is not worth a re-split.
//
// Always returns at least one (possibly empty) delta, so that a pure
// head-collapse -- no live elements, no tombstones to carry -- still
// produces the covered-heads bookkeeping needed for convergence.
func (store *Datastore) buildSnapshotDeltas(dagName string, priority uint64, snapshotID []byte, elements, tombstones []*pb.Element) []Delta {
	newSnapshot := func() Delta {
		d := store.newDelta()
		d.SetDagName(dagName)
		d.SetPriority(priority)
		d.SetSnapshot(true)
		d.SetSnapshotMeta(0, snapshotID) // total filled in once every sibling is known.
		return d
	}

	var result []Delta
	cur := newSnapshot()
	var curElems, curTombs []*pb.Element

	appendItem := func(isTomb bool, e *pb.Element) {
		if isTomb {
			curTombs = append(curTombs, e)
		} else {
			curElems = append(curElems, e)
		}
		cur.SetElements(curElems)
		cur.SetTombstones(curTombs)

		// Split off a new sibling once the current one is over
		// budget, as long as there is more than one item in it (a
		// single oversized item cannot be split further, so it is
		// left as-is, same as the existing MaxBatchDeltaSize handling
		// for regular batches).
		if cur.Size() > store.opts.MaxBatchDeltaSize && len(curElems)+len(curTombs) > 1 {
			if isTomb {
				curTombs = curTombs[:len(curTombs)-1]
			} else {
				curElems = curElems[:len(curElems)-1]
			}
			cur.SetElements(curElems)
			cur.SetTombstones(curTombs)
			result = append(result, cur)

			cur = newSnapshot()
			curElems, curTombs = nil, nil
			if isTomb {
				curTombs = append(curTombs, e)
			} else {
				curElems = append(curElems, e)
			}
			cur.SetElements(curElems)
			cur.SetTombstones(curTombs)
		}
	}

	for _, e := range elements {
		appendItem(false, e)
	}
	for _, t := range tombstones {
		appendItem(true, t)
	}

	result = append(result, cur)

	total := uint32(len(result))
	for _, d := range result {
		d.SetSnapshotMeta(total, snapshotID)
	}

	return result
}
