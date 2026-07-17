package kvdb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"

	dshelp "github.com/ipfs/boxo/datastore/dshelp"
)

// Use this to detect if we need to run migrations.
var version uint64 = 2

func (store *Datastore) versionKey() ds.Key {
	return store.namespace.ChildString(store.opts.crdtOpts.Namespaces.VersionKey)
}

func (store *Datastore) getVersion(ctx context.Context) (uint64, error) {
	versionK := store.versionKey()
	data, err := store.store.Get(ctx, versionK)
	if err != nil {
		if err == ds.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}

	v, n := binary.Uvarint(data)
	if n <= 0 {
		return v, errors.New("error decoding version")
	}
	return v - 1, nil
}

func (store *Datastore) setVersion(ctx context.Context, v uint64) error {
	versionK := store.versionKey()
	buf := make([]byte, binary.MaxVarintLen64)
	n := binary.PutUvarint(buf, v+1)
	if n == 0 {
		return errors.New("error encoding version")
	}

	return store.store.Put(ctx, versionK, buf[0:n])
}

// applyMigrations runs any migrations needed to bring the datastore from its
// on-disk version up to the current version, sequentially (0->1->2->...):
// each step's migration function runs and then bumps the stored version by
// exactly one, so that a crash partway through leaves the datastore at a
// consistent, resumable version rather than skipping steps.
func (store *Datastore) applyMigrations(ctx context.Context) error {
	v, err := store.getVersion(ctx)
	if err != nil {
		return err
	}

	if v == 0 {
		if err := store.migrate0to1(ctx); err != nil {
			return err
		}
		if err := store.setVersion(ctx, 1); err != nil {
			return err
		}
		v = 1
	}

	if v == 1 {
		if err := store.migrate1to2(ctx); err != nil {
			return err
		}
		if err := store.setVersion(ctx, 2); err != nil {
			return err
		}
		v = 2
	}

	store.logger.Infof("CRDT database format v%d", version)
	return nil
}

// migrate0to1 re-sets all the values and priorities of previously tombstoned
// elements to deal with the aftermath of
// https://github.com/ipfs/go-ds-crdt/issues/238. This bug caused that the
// values/priorities of certain elements was wrong depending on tombstone
// arrival order.
func (store *Datastore) migrate0to1(ctx context.Context) error {
	// 1. Find keys for which we have tombstones
	// 2. Loop them
	// 3. Find/set best value for them

	s := store.set
	tombsPrefix := s.keyPrefix(tombsNs) // /ns/tombs
	q := query.Query{
		Prefix:   tombsPrefix.String(),
		KeysOnly: true,
	}

	rStore := store.store
	var wStore ds.Write = store.store
	var err error
	batchingDs, batching := wStore.(ds.Batching)
	if batching {
		wStore, err = batchingDs.Batch(ctx)
		if err != nil {
			return err
		}
	}

	results, err := rStore.Query(ctx, q)
	if err != nil {
		return err
	}
	//nolint:errcheck
	defer results.Close()

	// Results are not going to be ordered per key (I tested). Therefore,
	// we can keep a list of keys in memory to avoid findingBestValue for
	// every tombstone block entry, or we can repeat the operation every
	// time there is a tombstone for the same key.  Given this is a one
	// time operation that only affects tombstoned keys, we opt to
	// de-duplicate.

	var total int
	doneKeys := make(map[string]struct{})
	for r := range results.Next() {
		if r.Error != nil {
			return r.Error
		}

		// Switch from /ns/tombs/key/block to /key
		dskey := ds.NewKey(
			strings.TrimPrefix(r.Key, tombsPrefix.String()))
		// Switch from /key/block to /key
		key := dskey.Parent().String()
		if _, ok := doneKeys[key]; ok {
			continue
		}
		doneKeys[key] = struct{}{}

		valueK := s.valueKey(key)
		v, p, err := s.findBestValue(ctx, key, nil)
		if err != nil {
			return fmt.Errorf("error finding best value for %s: %w", key, err)
		}

		if v == nil {
			if err = wStore.Delete(ctx, valueK); err != nil {
				return err
			}

			if err = wStore.Delete(ctx, s.priorityKey(key)); err != nil {
				return err
			}
		} else {
			if err = wStore.Put(ctx, valueK, v); err != nil {
				return err
			}
			if err = s.setPriority(ctx, wStore, key, p); err != nil {
				return err
			}
		}
		total++
	}

	if batching {
		err := wStore.(ds.Batch).Commit(ctx)
		if err != nil {
			return err
		}
	}

	s.logger.Debugf("Migration v0 to v1 finished (%d elements affected)", total)
	return nil
}

// migrate1to2 backfills the priority into every element marker
// (/namespace/s/<key>/<id>) that was written with an empty value, i.e. by
// pre-v2 code (see Item B: putElems now stores the element's effective
// priority in the marker value so findBestValue can avoid fetching the
// delta block from the DAG for every non-max-priority candidate).
//
// For each empty marker, the block CID is derived from the marker's id (the
// same decoding findBestValue uses) and its delta is fetched from the DAG
// service -- which, at migration time, is expected to be entirely local
// (this is a single-node, offline operation; no network fetch should be
// needed). If a block cannot be fetched (e.g. it was already pruned), the
// marker is left empty: findBestValue's runtime fallback still handles
// empty markers correctly, so this is not fatal to the migration, only to
// the optimization for that one marker.
func (store *Datastore) migrate1to2(ctx context.Context) error {
	s := store.set
	elemsPrefix := s.keyPrefix(elemsNs) // /ns/s
	q := query.Query{
		Prefix:   elemsPrefix.String(),
		KeysOnly: false,
	}

	rStore := store.store
	var wStore ds.Write = store.store
	var err error
	batchingDs, batching := wStore.(ds.Batching)
	if batching {
		wStore, err = batchingDs.Batch(ctx)
		if err != nil {
			return err
		}
	}

	results, err := rStore.Query(ctx, q)
	if err != nil {
		return err
	}
	//nolint:errcheck
	defer results.Close()

	ng := crdtNodeGetter{NodeGetter: store.dagService}

	var total, skipped int
	for r := range results.Next() {
		if r.Error != nil {
			return r.Error
		}

		if len(r.Value) > 0 {
			// already carries a priority (should not normally
			// happen at v1, but be defensive and leave it alone).
			continue
		}

		// The marker key is /ns/s/<key...>/<id>: the id is always
		// the last path component, regardless of what the key
		// itself looks like (it may contain "/").
		id := ds.NewKey(r.Key).Name()
		mhash, err := dshelp.DsKeyToMultihash(ds.NewKey(id))
		if err != nil {
			return fmt.Errorf("error decoding block id from marker %s: %w", r.Key, err)
		}
		blockCid := cid.NewCidV1(cid.DagProtobuf, mhash)

		cctx, cancel := s.withDAGTimeout(ctx)
		_, deltaBytes, err := ng.GetDelta(cctx, blockCid)
		cancel()
		if err != nil {
			store.logger.Warnf("migration v1 to v2: could not fetch block %s for marker %s, leaving marker empty: %s", blockCid, r.Key, err)
			skipped++
			continue
		}

		delta := store.newDelta()
		if err := delta.Unmarshal(deltaBytes); err != nil {
			store.logger.Warnf("migration v1 to v2: could not unmarshal block %s for marker %s, leaving marker empty: %s", blockCid, r.Key, err)
			skipped++
			continue
		}

		if err := wStore.Put(ctx, ds.NewKey(r.Key), encodePriority(delta.GetPriority())); err != nil {
			return err
		}
		total++
	}

	if batching {
		if err := wStore.(ds.Batch).Commit(ctx); err != nil {
			return err
		}
	}

	store.logger.Infof("Migration v1 to v2 finished (%d markers backfilled, %d skipped)", total, skipped)
	return nil
}
