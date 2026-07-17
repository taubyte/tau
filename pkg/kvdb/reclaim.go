package kvdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
)

// This file implements receiver-side reclamation of compacted history: when
// a replica merges a snapshot delta (see Compact, compact.go) produced by
// another replica, and it has now merged every sibling snapshot node of
// that compaction generation, it purges its own local copy of the DAG
// history the generation covers -- exactly what Compact does on the
// compacting replica itself, just triggered by a merge instead of by a
// direct Compact() call. See compact.go's package doc for the full design
// rationale (the "Known limitation" section this feature replaces).

// reclaimNamespace returns the namespace all reclaim bookkeeping keys live
// under: <namespace>/<Reclaim>.
func (store *Datastore) reclaimNamespace() ds.Key {
	return store.namespace.ChildString(store.opts.crdtOpts.Namespaces.Reclaim)
}

// reclaimCounterKey is /<ns>/rc/c/<hex(id)>: a varint count of how many
// sibling snapshot nodes of generation id have been processed (via
// processNode's allowReclaim path) on this replica so far.
func (store *Datastore) reclaimCounterKey(id []byte) ds.Key {
	return store.reclaimNamespace().ChildString("c").ChildString(hex.EncodeToString(id))
}

// reclaimDoneKey is /<ns>/rc/d/<hex(id)>: a marker recording that
// generation id's covered history has already been reclaimed locally.
func (store *Datastore) reclaimDoneKey(id []byte) ds.Key {
	return store.reclaimNamespace().ChildString("d").ChildString(hex.EncodeToString(id))
}

// snapshotGenerationID deterministically derives a compaction generation id
// from the covered heads it collapses: the first 16 bytes of sha256 over
// the sorted (bytewise) covered-head CID bytes. No clock/rand is involved,
// so independently recomputing it (e.g. to derive the id of a legacy
// snapshot that predates this metadata, see ReclaimCompacted) from the same
// covered heads always yields the same id.
func snapshotGenerationID(coveredHeads []cid.Cid) []byte {
	bs := make([][]byte, len(coveredHeads))
	for i, c := range coveredHeads {
		bs[i] = c.Bytes()
	}
	sort.Slice(bs, func(i, j int) bool { return bytes.Compare(bs[i], bs[j]) < 0 })

	h := sha256.New()
	for _, b := range bs {
		h.Write(b)
	}
	sum := h.Sum(nil)
	return sum[:16]
}

// incrReclaimGeneration atomically increments and returns the sibling
// counter for compaction generation id, guarded by reclaimMux (the
// read-modify-write is not otherwise atomic against the underlying
// go-datastore).
func (store *Datastore) incrReclaimGeneration(ctx context.Context, id []byte) (uint64, error) {
	store.reclaimMux.Lock()
	defer store.reclaimMux.Unlock()

	key := store.reclaimCounterKey(id)
	var count uint64
	data, err := store.store.Get(ctx, key)
	switch {
	case err == nil:
		count, _ = binary.Uvarint(data)
	case errors.Is(err, ds.ErrNotFound):
		count = 0
	default:
		return 0, err
	}

	count++
	buf := make([]byte, binary.MaxVarintLen64)
	n := binary.PutUvarint(buf, count)
	if err := store.store.Put(ctx, key, buf[:n]); err != nil {
		return 0, err
	}
	return count, nil
}

// maybeReclaimOnSnapshot is the R4 auto-trigger, called at the end of
// processNode for a just-merged snapshot delta when allowReclaim and
// Options.ReclaimOnSnapshot are both set.
//
// It increments this generation's sibling counter and, once every sibling
// has been merged (count >= total) and the generation is not already marked
// done, reclaims the generation's covered history (R5) and writes the done
// marker.
//
// Waiting for count==total (well, >=, see below) is what prevents the
// multi-sibling visibility gap: purging covered history before all
// siblings have merged could transiently delete a key whose surviving
// snapshot element lives in a not-yet-merged sibling (a value disappearing
// and reappearing, with a spurious deleteHook/putHook flap). By the time
// every sibling has merged, every live key's snapshot marker is already in
// place, so the purge's recomputed value is correct.
//
// A crash between markProcessed (in processNode) and the counter increment
// here loses one count, so the generation's counter never reaches total and
// this auto path never fires for it -- accepted as a soft-feature trade-off;
// ReclaimCompacted (R6) recovers it. Using >= (not ==) means an over-count
// (e.g. reprocessing after a crash that happened before markProcessed, so
// the same node is merged and counted twice) still safely fires reclaim
// exactly once, because the done marker short-circuits every later call.
//
// All failures here are soft: they are logged and otherwise ignored. This
// function must never cause processNode to fail or mark the datastore
// dirty -- a missed reclaim is a space, not a correctness, problem, and
// ReclaimCompacted is the retry path.
func (store *Datastore) maybeReclaimOnSnapshot(ctx context.Context, delta Delta, node ipld.Node) {
	total, id := delta.SnapshotMeta()
	if total == 0 || len(id) == 0 {
		// Legacy snapshot (predates this metadata): nothing to auto-fire
		// on. ReclaimCompacted can still recover it.
		return
	}

	count, err := store.incrReclaimGeneration(ctx, id)
	if err != nil {
		store.logger.Warnf("reclaim: error incrementing sibling counter for generation %x: %s", id, err)
		return
	}
	if count < uint64(total) {
		return
	}

	doneKey := store.reclaimDoneKey(id)
	done, err := store.store.Has(ctx, doneKey)
	if err != nil {
		store.logger.Warnf("reclaim: error checking done marker for generation %x: %s", id, err)
		return
	}
	if done {
		return
	}

	links := node.Links()
	covered := make([]cid.Cid, len(links))
	for i, l := range links {
		covered[i] = l.Cid
	}

	n, err := store.reclaimCovered(ctx, covered)
	if err != nil {
		store.logger.Warnf("reclaim: error reclaiming covered history for generation %x: %s", id, err)
		return
	}

	if err := store.store.Put(ctx, doneKey, nil); err != nil {
		store.logger.Warnf("reclaim: error writing done marker for generation %x: %s", id, err)
		return
	}

	store.logger.Debugf("reclaim: reclaimed %d blocks for generation %x", n, id)
}

// reclaimCovered purges the local copy of the DAG history covered by a
// snapshot generation -- its blocks and set entries -- exactly as Compact
// does for its own covered history, but scoped to whatever subset of it
// this replica has actually processed (see walkProcessedDAG).
//
// On a fresh replica that never merged any of the covered history, the walk
// finds nothing and this is a no-op returning (0, nil): reclamation only
// ever removes local residue, it never needs to fetch anything.
//
// Like Compact, this intentionally keeps the processed-block markers for
// the CIDs it removes (rather than deleting them): they are tiny, and their
// presence means a stale rebroadcast of now-purged history is recognized as
// already processed and skipped, rather than triggering a doomed fetch of a
// block that no longer exists locally.
func (store *Datastore) reclaimCovered(ctx context.Context, covered []cid.Cid) (int, error) {
	dagCIDSet, setKeys, _, err := store.walkProcessedDAG(ctx, covered)
	if err != nil {
		return 0, fmt.Errorf("error walking DAG: %w", err)
	}
	if len(dagCIDSet) == 0 {
		return 0, nil
	}

	for key, kind := range setKeys {
		if err := store.set.purgeKeyBlocks(ctx, key, dagCIDSet, kind&dagWalkElem != 0, kind&dagWalkTomb != 0); err != nil {
			return 0, fmt.Errorf("error purging blocks for key %q: %w", key, err)
		}
	}

	dagCIDs := make([]cid.Cid, 0, len(dagCIDSet))
	for c := range dagCIDSet {
		dagCIDs = append(dagCIDs, c)
	}

	if err := store.dagService.RemoveMany(ctx, dagCIDs); err != nil {
		return 0, fmt.Errorf("error removing reclaimed blocks: %w", err)
	}

	return len(dagCIDs), nil
}

// ReclaimCompacted is the explicit, manual reclamation entry point: the
// recovery path for compaction generations that the automatic per-merge
// trigger (Options.ReclaimOnSnapshot) missed -- the documented crash window,
// deployments running with ReclaimOnSnapshot set to false, and snapshots
// produced by an older version of this package that never stamped generation
// metadata (legacy snapshots, whose generation id is derived here the same
// way Compact would have: the hash over their covered-heads links).
//
// It walks dagName's current heads locally (like PurgeDAG/Compact) and, for
// every compaction generation found that has not already been reclaimed,
// reclaims that generation's covered history and records it as reclaimed.
// Returns the total number of DAG blocks reclaimed across every generation
// processed. It is safe to call at any time and is idempotent: an
// already-reclaimed generation is skipped.
//
// Like the automatic path, it reclaims a generation only once every one of
// its sibling snapshot nodes is present locally (a generation Compact split
// into N sibling blocks is reclaimed only when all N have been merged here).
// Reclaiming a partially-merged generation would purge covered history whose
// surviving value lives in a not-yet-merged sibling, transiently dropping
// that key. An incomplete generation is left untouched and picked up by a
// later call once its remaining siblings arrive. Legacy snapshots carry no
// sibling count and are reclaimed as found.
//
// It serializes against Compact (holds compactMux): both walk and mutate
// the same local DAG/set state for a dagName, and Compact's own snapshot
// nodes must not be observed half-written.
func (store *Datastore) ReclaimCompacted(ctx context.Context, dagName string) (int, error) {
	store.compactMux.Lock()
	defer store.compactMux.Unlock()

	heads, _, err := store.heads.ListDAG(ctx, dagName)
	if err != nil {
		return 0, fmt.Errorf("error listing heads: %w", err)
	}
	if len(heads) == 0 {
		return 0, nil
	}

	headCIDs := make([]cid.Cid, len(heads))
	for i, h := range heads {
		headCIDs[i] = h.Cid
	}

	_, _, snapshots, err := store.walkProcessedDAG(ctx, headCIDs)
	if err != nil {
		return 0, fmt.Errorf("error walking DAG: %w", err)
	}

	// Group the snapshot nodes the walk found by compaction generation
	// (deriving a legacy snapshot's id from its covered-heads links, exactly
	// as Compact would have). walkProcessedDAG dedups by CID, so each
	// distinct sibling contributes exactly once to found. order preserves
	// first-encounter order for deterministic processing.
	type generation struct {
		id    []byte
		links []cid.Cid
		total uint32
		found uint32
	}
	gens := make(map[string]*generation)
	var order []string
	for _, sn := range snapshots {
		id := sn.id
		if len(id) == 0 {
			id = snapshotGenerationID(sn.links)
		}
		k := string(id)
		g, ok := gens[k]
		if !ok {
			g = &generation{id: id, links: sn.links}
			gens[k] = g
			order = append(order, k)
		}
		g.found++
		// All siblings of a generation carry the same snapshotTotal; take
		// the max defensively in case of a malformed/mixed set.
		if sn.total > g.total {
			g.total = sn.total
		}
	}

	var total int
	for _, k := range order {
		g := gens[k]

		// Wait for every sibling of a (metadata-carrying) generation to be
		// present locally before reclaiming it -- the manual-path analogue
		// of the auto-path counter rule (R4/maybeReclaimOnSnapshot).
		// Reclaiming a generation whose siblings are only partially merged
		// would purge covered history whose surviving value lives in a
		// not-yet-merged sibling, i.e. exactly the visibility gap the
		// counter avoids. An incomplete generation is simply left for a
		// later call (or the auto path) once its remaining siblings arrive.
		// Legacy snapshots (total 0) carry no sibling count and so cannot be
		// gated this way; they are reclaimed as found (their pre-metadata
		// origin predates multi-sibling generations in practice).
		if g.total > 0 && g.found < g.total {
			continue
		}

		doneKey := store.reclaimDoneKey(g.id)
		done, err := store.store.Has(ctx, doneKey)
		if err != nil {
			return total, fmt.Errorf("error checking done marker for generation %x: %w", g.id, err)
		}
		if done {
			continue
		}

		n, err := store.reclaimCovered(ctx, g.links)
		if err != nil {
			return total, fmt.Errorf("error reclaiming generation %x: %w", g.id, err)
		}

		if err := store.store.Put(ctx, doneKey, nil); err != nil {
			return total, fmt.Errorf("error writing done marker for generation %x: %w", g.id, err)
		}

		total += n
	}

	return total, nil
}
