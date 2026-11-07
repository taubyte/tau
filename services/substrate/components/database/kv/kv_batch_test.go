package kv

import (
	"context"
	"testing"

	"github.com/taubyte/tau/core/kvdb"
)

// countingKV counts the calls a remote database would turn into network round
// trips.
type countingKV struct {
	kvdb.KVDB
	puts, deletes, commits int
}

func (c *countingKV) Put(context.Context, string, []byte) error { c.puts++; return nil }
func (c *countingKV) Delete(context.Context, string) error      { c.deletes++; return nil }
func (c *countingKV) List(context.Context, string) ([]string, error) {
	return nil, nil
}
func (c *countingKV) Batch(context.Context) (kvdb.Batch, error) { return &countingBatch{c}, nil }

type countingBatch struct{ c *countingKV }

func (b *countingBatch) Put(string, []byte) error { return nil }
func (b *countingBatch) Delete(string) error      { return nil }
func (b *countingBatch) Commit() error            { b.c.commits++; return nil }

// A write and its size record are one batch, not two writes: each is a network
// call to a hoarder and a replicated write.
func TestPutAndDeleteAreOneBatch(t *testing.T) {
	db := &countingKV{}
	k := &kv{name: "test", database: db, maxSize: 1 << 20}

	if err := k.Put(t.Context(), "/k", []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := k.Delete(t.Context(), "/k"); err != nil {
		t.Fatal(err)
	}
	if db.puts != 0 || db.deletes != 0 || db.commits != 2 {
		t.Fatalf("puts=%d deletes=%d commits=%d, want 0, 0, 2", db.puts, db.deletes, db.commits)
	}
}
