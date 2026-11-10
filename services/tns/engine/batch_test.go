package engine

import (
	"context"
	"testing"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	"github.com/taubyte/tau/services/tns/flat"
	"gotest.tools/v3/assert"
)

// countingDB counts the writes that each become a separate replicated change.
type countingDB struct {
	kvdb.KVDB
	puts, batches int
}

func (c *countingDB) Put(ctx context.Context, key string, v []byte) error {
	c.puts++
	return c.KVDB.Put(ctx, key, v)
}

func (c *countingDB) Batch(ctx context.Context) (kvdb.Batch, error) {
	c.batches++
	return c.KVDB.Batch(ctx)
}

// A config push merges hundreds of keys. Each separate Put is its own change
// for the other nodes; one batch is one change.
func TestMergeWritesOneBatch(t *testing.T) {
	ctx := context.Background()
	inner, err := mock.New().New(nil, "/tns", 0)
	assert.NilError(t, err)
	db := &countingDB{KVDB: inner}
	e, err := New(db, "tns")
	assert.NilError(t, err)
	db.puts, db.batches = 0, 0

	data := map[string]interface{}{}
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		data[k] = map[string]interface{}{"links": k + "-v1"}
	}
	obj, err := flat.New([]string{"http", "websites"}, data)
	assert.NilError(t, err)

	assert.NilError(t, e.Merge(ctx, obj))
	assert.Equal(t, db.puts, 0, "keys written one by one")
	assert.Equal(t, db.batches, 1)

	keys, err := inner.List(ctx, "")
	assert.NilError(t, err)
	assert.Assert(t, len(keys) >= 5, "batch not written: %v", keys)
}
