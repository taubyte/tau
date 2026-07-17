package kvdb

// Fault-injection coverage for migrations.go's error branches (Item H).

import (
	"context"
	"errors"
	"testing"

	dshelp "github.com/ipfs/boxo/datastore/dshelp"
	dag "github.com/ipfs/boxo/ipld/merkledag"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
)

// TestGetVersionDecodeError checks getVersion's malformed-data error
// branch (as opposed to ds.ErrNotFound, which decodes to version 0).
func TestGetVersionDecodeError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()

	// An empty value fails binary.Uvarint decoding (n <= 0).
	if err := fd.Datastore.Put(ctx, d.versionKey(), []byte{}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.getVersion(ctx); err == nil {
		t.Fatal("expected getVersion to fail decoding a malformed version value")
	}
}

// TestGetVersionStoreError checks getVersion's non-NotFound Get error
// branch, and that applyMigrations propagates it.
func TestGetVersionStoreError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()

	fd.SetFail(failAlways("Get"))
	if _, err := d.getVersion(ctx); !errors.Is(err, errFault) {
		t.Fatalf("expected errFault from getVersion, got %v", err)
	}
	if err := d.applyMigrations(ctx); !errors.Is(err, errFault) {
		t.Fatalf("expected applyMigrations to propagate the getVersion failure, got %v", err)
	}
}

// TestMigrate0to1QueryError checks migrate0to1's initial tombstone-scan
// Query error branch.
func TestMigrate0to1QueryError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)

	fd.SetFail(failAlways("Query"))
	if err := d.migrate0to1(context.Background()); !errors.Is(err, errFault) {
		t.Fatalf("expected errFault, got %v", err)
	}
}

// TestMigrate0to1DeleteError checks migrate0to1's write-side error branch
// for the common real-world case where a tombstoned key has no surviving
// element (the value/priority keys must be deleted): a fully deleted key
// gives migrate0to1 a tombstone entry to process.
func TestMigrate0to1DeleteError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()

	k := ds.NewKey("mig0to1-key")
	if err := d.Put(ctx, k, []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(ctx, k); err != nil {
		t.Fatal(err)
	}

	fd.SetFail(failAlways("BatchDelete"))
	if err := d.migrate0to1(ctx); !errors.Is(err, errFault) {
		t.Fatalf("expected errFault from the value Delete, got %v", err)
	}
}

// TestMigrate0to1BatchError checks migrate0to1's own initial Batch() error
// branch (distinct from the tombstone-scan Query tested above).
func TestMigrate0to1BatchError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)

	fd.SetFail(failAlways("Batch"))
	if err := d.migrate0to1(context.Background()); !errors.Is(err, errFault) {
		t.Fatalf("expected errFault, got %v", err)
	}
}

// TestMigrate0to1SurvivorWriteErrors checks migrate0to1's "surviving value"
// write branches (Put value / setPriority), as opposed to
// TestMigrate0to1DeleteError's "no survivor" case: a key with two versions,
// tombstoned only for the first, so the second is the recomputed best
// value.
func TestMigrate0to1SurvivorWriteErrors(t *testing.T) {
	setup := func(t *testing.T) (*Datastore, *faultyDatastore, string) {
		t.Helper()
		fd := newFaultyDatastore(ds.NewMapDatastore())
		d := newTestDatastore(t, fd)
		ctx := context.Background()
		k := ds.NewKey("mig0to1-survivor-key")
		if err := d.Put(ctx, k, []byte("v1")); err != nil {
			t.Fatal(err)
		}
		res, err := d.store.Query(ctx, query.Query{Prefix: d.set.elemsPrefix(k.String()).String(), KeysOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		var firstID string
		for e := range res.Next() {
			if e.Error != nil {
				t.Fatal(e.Error)
			}
			firstID = ds.NewKey(e.Key).Name()
		}
		_ = res.Close()
		if firstID == "" {
			t.Fatal("could not find the first element marker")
		}
		if err := d.Put(ctx, k, []byte("v2")); err != nil {
			t.Fatal(err)
		}
		// Hand-craft a tombstone for just the first version, as Rmv
		// would produce for a partially-superseded key, without going
		// through the normal single-key-delete Delete() API (which
		// would tombstone every version).
		tombKey := d.set.tombsPrefix(k.String()).ChildString(firstID)
		if err := fd.Datastore.Put(ctx, tombKey, nil); err != nil {
			t.Fatal(err)
		}
		return d, fd, k.String()
	}

	t.Run("value Put fails", func(t *testing.T) {
		d, fd, key := setup(t)
		fd.SetFail(func(op string, k ds.Key) error {
			if op == "BatchPut" && k.String() == d.set.valueKey(key).String() {
				return errFault
			}
			return nil
		})
		if err := d.migrate0to1(context.Background()); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("setPriority fails", func(t *testing.T) {
		d, fd, key := setup(t)
		valueK := d.set.valueKey(key)
		fd.SetFail(func(op string, k ds.Key) error {
			if op == "BatchPut" && k.String() != valueK.String() {
				return errFault
			}
			return nil
		})
		if err := d.migrate0to1(context.Background()); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})
}

// TestMigrate1to2QueryError checks migrate1to2's marker-scan Query error
// branch.
func TestMigrate1to2QueryError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)

	fd.SetFail(failAlways("Query"))
	if err := d.migrate1to2(context.Background()); !errors.Is(err, errFault) {
		t.Fatalf("expected errFault, got %v", err)
	}
}

// TestMigrate1to2WriteError checks migrate1to2's marker-backfill Put error
// branch: a legacy (empty-value) marker whose block IS fetchable, so the
// migration reaches the write, which then fails.
func TestMigrate1to2WriteError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()

	k := ds.NewKey("mig1to2-write-key")
	if err := d.Put(ctx, k, []byte("v")); err != nil {
		t.Fatal(err)
	}

	res, err := d.store.Query(ctx, query.Query{Prefix: d.set.elemsPrefix(k.String()).String(), KeysOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var markerKey ds.Key
	for e := range res.Next() {
		if e.Error != nil {
			t.Fatal(e.Error)
		}
		markerKey = ds.NewKey(e.Key)
	}
	_ = res.Close()
	if markerKey.String() == "" {
		t.Fatal("could not find the element marker to blank out")
	}
	if err := fd.Datastore.Put(ctx, markerKey, nil); err != nil {
		t.Fatal(err)
	}

	fd.SetFail(func(op string, key ds.Key) error {
		if op == "BatchPut" && key.String() == markerKey.String() {
			return errFault
		}
		return nil
	})
	if err := d.migrate1to2(ctx); !errors.Is(err, errFault) {
		t.Fatalf("expected errFault, got %v", err)
	}
}

// TestMigrate1to2UnmarshalError checks migrate1to2's other tolerated
// (skip-and-continue, not fatal) failure mode: a legacy marker whose block
// IS fetchable but whose data is not a valid marshaled Delta. The migration
// must still complete successfully, leaving that one marker empty.
func TestMigrate1to2UnmarshalError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()

	k := ds.NewKey("mig1to2-unmarshal-key")
	if err := d.Put(ctx, k, []byte("v")); err != nil {
		t.Fatal(err)
	}

	// A real, fetchable block whose data is not a valid Delta (an
	// incomplete protobuf varint tag).
	badNode := dag.NodeWithData([]byte{0x80})
	if err := badNode.SetCidBuilder(dag.V1CidPrefix()); err != nil {
		t.Fatal(err)
	}
	if err := d.dagService.Add(ctx, badNode); err != nil {
		t.Fatal(err)
	}

	// A second, synthetic (empty-value/legacy) element marker for the
	// same key, whose id points at badNode instead of the key's actual
	// delta block -- exactly the shape migrate1to2 scans for, without
	// disturbing the real marker Put already created.
	markerKey := d.set.elemsPrefix(k.String()).ChildString(dshelp.MultihashToDsKey(badNode.Cid().Hash()).String())
	if err := fd.Datastore.Put(ctx, markerKey, nil); err != nil {
		t.Fatal(err)
	}

	if err := d.migrate1to2(ctx); err != nil {
		t.Fatalf("expected migrate1to2 to tolerate an unmarshalable block, got %v", err)
	}
	val, err := d.store.Get(ctx, markerKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(val) != 0 {
		t.Fatalf("expected the marker to remain empty after a failed unmarshal, got %v", val)
	}
}
