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
