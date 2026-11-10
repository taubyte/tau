package migration

import (
	"context"
	"fmt"
	"strings"

	"github.com/ipfs/boxo/datastore/dshelp"
	"github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	ipld "github.com/ipfs/go-ipld-format"
	"github.com/multiformats/go-multihash"
)

const blocksPrefix = "/blocks/"

// scrubAndSweep deletes what this pass proved safe to delete:
//
//   - Per instance: the /crdt/<hash> namespace goes once every key verified
//     read-back AND (for storage) every locally-held file CID shows stash
//     claims at the fleet target — a stash ack alone proves one copy, not the
//     replica target.
//   - Blocks: only those reachable from the namespaces scrubbed (their CRDT
//     and file DAGs), minus everything reachable from every namespace left —
//     foreign ones included. Nothing else in /blocks is ever touched.
//
// A pass over the delete threshold deletes nothing.
func (m *Migrator) scrubAndSweep(ctx context.Context, report *Report, passHashes []string) {
	if ctx.Err() != nil {
		return
	}

	// Bytes gate: live stash claims per locally-held file CID, fleet target.
	allCids := make([]string, 0)
	seen := make(map[string]struct{})
	for _, rep := range report.Instances {
		for _, c := range rep.fileCids {
			if _, ok := seen[c]; !ok {
				seen[c] = struct{}{}
				allCids = append(allCids, c)
			}
		}
	}
	claims := map[string]int{}
	target := 1
	claimsOk := true
	if len(allCids) > 0 {
		var err error
		if claims, target, err = m.hoarder.StashStatus(allCids...); err != nil {
			// Can't prove replication — keep every byte holder unscrubbed.
			logger.Errorf("stash status failed with: %s — keeping local bytes", err)
			claimsOk = false
		}
	}

	scrub := make(map[string][]string) // hash → its raw keys
	for hash, rep := range report.Instances {
		if !rep.Verified || rep.Err != "" {
			continue
		}
		if len(rep.fileCids) > 0 {
			if !claimsOk {
				continue
			}
			pending := 0
			for _, c := range rep.fileCids {
				if claims[c] < target {
					pending++
				}
			}
			if rep.FilesAwaitingRepl = pending; pending > 0 {
				continue
			}
		}
		keys, err := m.rawKeys(ctx, crdtPrefix+hash+"/")
		if err != nil {
			rep.Err = fmt.Sprintf("scrub: %s", err)
			continue
		}
		scrub[hash] = keys
	}

	if len(scrub) == 0 || ctx.Err() != nil {
		return
	}

	// Candidates are collected before anything is deleted, so the walk still
	// finds the scrubbed namespaces' heads.
	dag := offlineDAG(m.node.Store())
	candidates := make(map[string]struct{})
	nKeys := 0
	for hash, keys := range scrub {
		nKeys += len(keys)
		if err := m.markHeads(ctx, dag, hash, candidates); err != nil {
			logger.Errorf("collecting sweep candidates failed with: %s — deleting nothing this pass", err)
			return
		}
		for _, cidStr := range report.Instances[hash].fileCids {
			if c, err := cid.Decode(cidStr); err == nil {
				m.markReachable(ctx, dag, c, candidates)
			}
		}
	}

	remaining := make([]string, 0, len(passHashes))
	for _, h := range passHashes {
		if _, ok := scrub[h]; !ok {
			remaining = append(remaining, h)
		}
	}
	keep, err := m.keepSet(ctx, remaining)
	if err != nil {
		logger.Errorf("building sweep keep-set failed with: %s — deleting nothing this pass", err)
		return
	}
	doomed := make([]string, 0, len(candidates))
	for k := range candidates {
		if _, ok := keep[k]; !ok {
			doomed = append(doomed, "/blocks"+dshelp.MultihashToDsKey(multihash.Multihash(k)).String())
		}
	}

	if err := m.overThreshold(ctx, len(doomed)); err != nil {
		logger.Errorf("data migration stopped, nothing deleted: %s — the replayed data stays on this node and later boots skip it", err)
		report.Err = err.Error()
		report.thresholdHit = true
		return
	}
	logger.Infof("data migration deleting %d namespace(s), %d key(s), %d block(s)", len(scrub), nKeys, len(doomed))

	keptOne := false
	for hash, keys := range scrub {
		rep := report.Instances[hash]
		// The instance's data now lives hoarder-side; a crash mid-scrub leaves
		// keys the next pass re-verifies (already remote) and finishes.
		if err := m.deleteKeys(ctx, keys); err != nil {
			rep.Err = fmt.Sprintf("scrub: %s", err)
			keptOne = true
			continue
		}
		report.SweptKeys += len(keys)
		rep.Scrubbed = true
	}
	// A namespace left behind may still reach candidates, so no block goes.
	// shortcut: the scrubbed namespaces' blocks then stay orphaned; per-namespace candidate sets would free them.
	if keptOne {
		return
	}

	if err := m.deleteKeys(ctx, doomed); err != nil {
		logger.Errorf("block sweep failed with: %s", err)
		return
	}
	report.SweptBlocks = len(doomed)
}

// overThreshold refuses a pass whose deletions look like a misclassification.
func (m *Migrator) overThreshold(ctx context.Context, blocks int) error {
	if blocks <= MaxDeleteBlocks {
		return nil
	}
	total, err := m.rawKeys(ctx, blocksPrefix)
	if err != nil {
		return fmt.Errorf("delete threshold: counting blocks: %w", err)
	}
	if limit := max(MaxDeleteBlocks, int(MaxDeleteFraction*float64(len(total)))); blocks > limit {
		return fmt.Errorf("delete threshold: would delete %d of %d blocks (limit %d)", blocks, len(total), limit)
	}
	return nil
}

// keepSet marks every block reachable from the still-present namespaces:
// their CRDT DAG heads and — since file bytes are separate DAGs only named by
// metadata values — every file CID their metadata records. Over-keeping is
// safe; under-keeping never is, so unresolved namespaces contribute both.
func (m *Migrator) keepSet(ctx context.Context, remaining []string) (map[string]struct{}, error) {
	dag := offlineDAG(m.node.Store())
	keep := make(map[string]struct{})

	for _, hash := range remaining {
		if err := m.markHeads(ctx, dag, hash, keep); err != nil {
			return nil, err
		}
		// A service db may be open live, and opening a view writes to it; it
		// names no files.
		if _, err := multihash.FromB58String(hash); err != nil {
			continue
		}

		// File content DAGs named by this namespace's metadata.
		view, err := openLegacyView(m.node.Store(), hash)
		if err != nil {
			return nil, err
		}
		entries, err := localEntries(ctx, view)
		view.Close()
		if err != nil {
			return nil, err
		}
		for _, cidStr := range fileCidsOf(entries) {
			if c, err := cid.Decode(cidStr); err == nil {
				m.markReachable(ctx, dag, c, keep)
			}
		}
	}
	return keep, nil
}

// markHeads marks a namespace's CRDT DAG, walked from its heads
// (/crdt/<hash>/h/<mh>).
func (m *Migrator) markHeads(ctx context.Context, dag ipld.DAGService, hash string, set map[string]struct{}) error {
	headKeys, err := m.rawKeys(ctx, crdtPrefix+hash+"/h/")
	if err != nil {
		return err
	}
	for _, hk := range headKeys {
		seg := strings.TrimPrefix(hk, crdtPrefix+hash+"/h")
		if c, err := dshelp.DsKeyToCidV1(ds.NewKey(seg), cid.DagProtobuf); err == nil {
			m.markReachable(ctx, dag, c, set)
		}
	}
	return nil
}

// markReachable walks a DAG offline, adding every locally-present block's
// multihash to the keep-set. Missing children are tolerated — what isn't here
// can't be swept.
func (m *Migrator) markReachable(ctx context.Context, dag ipld.DAGService, c cid.Cid, keep map[string]struct{}) {
	key := string(c.Hash())
	if _, ok := keep[key]; ok {
		return
	}
	nd, err := dag.Get(ctx, c)
	if err != nil {
		return
	}
	keep[key] = struct{}{}
	for _, l := range nd.Links() {
		m.markReachable(ctx, dag, l.Cid, keep)
	}
}

// rawKeys lists raw datastore keys under a prefix.
func (m *Migrator) rawKeys(ctx context.Context, prefix string) ([]string, error) {
	res, err := m.node.Store().Query(ctx, query.Query{Prefix: prefix, KeysOnly: true})
	if err != nil {
		return nil, err
	}
	defer res.Close()

	out := make([]string, 0)
	for e := range res.Next() {
		if e.Error != nil {
			return nil, e.Error
		}
		out = append(out, e.Key)
	}
	return out, nil
}

// deleteKeys removes raw keys in bounded batches.
func (m *Migrator) deleteKeys(ctx context.Context, keys []string) error {
	const chunk = 512
	for len(keys) > 0 {
		n := min(chunk, len(keys))
		batch, err := m.node.Store().Batch(ctx)
		if err != nil {
			return err
		}
		for _, k := range keys[:n] {
			if err := batch.Delete(ctx, ds.NewKey(k)); err != nil {
				return err
			}
		}
		if err := batch.Commit(ctx); err != nil {
			return err
		}
		keys = keys[n:]
	}
	return nil
}
