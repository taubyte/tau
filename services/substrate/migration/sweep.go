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
)

const blocksPrefix = "/blocks/"

// scrubAndSweep deletes what this pass proved safe to delete:
//
//   - Per instance: the /crdt/<hash> namespace goes once every key verified
//     read-back AND (for storage) every locally-held file CID shows stash
//     claims at the fleet target — a stash ack alone proves one copy, not the
//     replica target.
//   - Blocks: a mark-and-sweep over /blocks. The keep-set is everything
//     reachable from the namespaces still present (their CRDT DAGs and their
//     file content DAGs); all other blocks on a substrate node are
//     re-fetchable cache by architecture. The sweep only runs while legacy
//     namespaces exist(ed) at pass start, so steady-state nodes never touch
//     their cache.
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
		n, err := m.scrubNamespace(ctx, hash)
		if err != nil {
			rep.Err = fmt.Sprintf("scrub: %s", err)
			continue
		}
		report.SweptKeys += n
		rep.Scrubbed = true
	}

	if ctx.Err() != nil {
		return
	}

	remaining := make([]string, 0, len(passHashes))
	for _, h := range passHashes {
		if rep, ok := report.Instances[h]; ok && rep.Scrubbed {
			continue
		}
		remaining = append(remaining, h)
	}

	keep, err := m.keepSet(ctx, remaining)
	if err != nil {
		logger.Errorf("building sweep keep-set failed with: %s — skipping block sweep", err)
		return
	}
	swept, err := m.sweepBlocks(ctx, keep)
	if err != nil {
		logger.Errorf("block sweep failed with: %s", err)
	}
	report.SweptBlocks = swept
}

// scrubNamespace deletes every raw datastore key of one /crdt/<hash>
// namespace. The instance's data now lives hoarder-side; a crash mid-scrub
// leaves keys the next pass re-verifies (already remote) and finishes.
func (m *Migrator) scrubNamespace(ctx context.Context, hash string) (int, error) {
	keys, err := m.rawKeys(ctx, crdtPrefix+hash+"/")
	if err != nil {
		return 0, err
	}
	if err := m.deleteKeys(ctx, keys); err != nil {
		return 0, err
	}
	return len(keys), nil
}

// keepSet marks every block reachable from the still-present namespaces:
// their CRDT DAG heads and — since file bytes are separate DAGs only named by
// metadata values — every file CID their metadata records. Over-keeping is
// safe; under-keeping never is, so unresolved namespaces contribute both.
func (m *Migrator) keepSet(ctx context.Context, remaining []string) (map[string]struct{}, error) {
	dag := offlineDAG(m.node.Store())
	keep := make(map[string]struct{})

	for _, hash := range remaining {
		// CRDT DAG: walk from the namespace's heads (/crdt/<hash>/h/<mh>).
		headKeys, err := m.rawKeys(ctx, crdtPrefix+hash+"/h/")
		if err != nil {
			return nil, err
		}
		for _, hk := range headKeys {
			seg := strings.TrimPrefix(hk, crdtPrefix+hash+"/h")
			c, err := dshelp.DsKeyToCidV1(ds.NewKey(seg), cid.DagProtobuf)
			if err != nil {
				continue
			}
			m.markReachable(ctx, dag, c, keep)
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

// sweepBlocks deletes every /blocks entry whose multihash is not in the
// keep-set.
func (m *Migrator) sweepBlocks(ctx context.Context, keep map[string]struct{}) (int, error) {
	res, err := m.node.Store().Query(ctx, query.Query{Prefix: blocksPrefix, KeysOnly: true})
	if err != nil {
		return 0, err
	}
	defer res.Close()

	doomed := make([]string, 0)
	for e := range res.Next() {
		if e.Error != nil {
			return 0, e.Error
		}
		mhash, err := dshelp.DsKeyToMultihash(ds.NewKey(strings.TrimPrefix(e.Key, "/blocks")))
		if err != nil {
			continue // foreign key shape — not ours to touch
		}
		if _, ok := keep[string(mhash)]; !ok {
			doomed = append(doomed, e.Key)
		}
	}
	if err := m.deleteKeys(ctx, doomed); err != nil {
		return 0, err
	}
	return len(doomed), nil
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
