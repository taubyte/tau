package client

import (
	"testing"

	"github.com/taubyte/go-sdk/errno"
)

// A function can open batches and never commit them, on purpose or on an error
// path, and each holds up to maxBatchBytes: an instance keeps a bounded number,
// dropping the oldest so that abandoned batches never block new ones.
func TestOpenBatchesAreCappedOldestFirst(t *testing.T) {
	e := newEnv(t, nil)
	first := e.batchNew()
	e.ok(e.batchPut(first, "k", "v"))

	var last uint32
	for i := 0; i < 10*maxOpenBatches; i++ {
		last = e.batchNew()
		if n := len(e.f.batches); n > maxOpenBatches {
			t.Fatalf("%d batches open", n)
		}
	}

	e.want(errno.ErrorDatabaseNotFound, e.batchPut(first, "k", "v"))
	e.ok(e.batchPut(last, "k", "v"))
	e.ok(e.f.databaseBatchCommit(e.ctx, e.mod, last))
	if v, err := e.get("k"); err != 0 || v != "v" {
		t.Fatalf("got %q, errno %s", v, err)
	}
}

// The per-instance bound multiplies with instances: the node bounds what all
// open batches hold together, and a commit or a drop gives it back.
func TestQueuedBatchBytesAreBoundedPerNode(t *testing.T) {
	saved := maxQueuedBatchBytes
	maxQueuedBatchBytes = 10
	t.Cleanup(func() { maxQueuedBatchBytes = saved })
	base := queuedBatchBytes.Load()

	a, b := newEnv(t, nil), newEnv(t, nil)
	ba, bb := a.batchNew(), b.batchNew()
	a.ok(a.batchPut(ba, "k1", "vvvv")) // 6 bytes
	b.want(errno.ErrorDatabasePutFailed, b.batchPut(bb, "k2", "vvvv"))

	a.ok(a.f.databaseBatchCommit(a.ctx, a.mod, ba))
	if got := queuedBatchBytes.Load(); got != base {
		t.Fatalf("queued %d after commit, want %d", got, base)
	}
	b.ok(b.batchPut(bb, "k2", "vvvv"))

	b.f.Close()
	if got := queuedBatchBytes.Load(); got != base {
		t.Fatalf("queued %d after close, want %d", got, base)
	}
}
