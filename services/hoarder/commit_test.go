package hoarder

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/streams/command"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
)

// countingHandle counts commits and makes each slow enough for writes to pile up.
type countingHandle struct {
	kvdb.KVDB
	commits atomic.Int64
}

func (c *countingHandle) Batch(ctx context.Context) (kvdb.Batch, error) {
	b, err := c.KVDB.Batch(ctx)
	return &countingBatch{Batch: b, c: c}, err
}

type countingBatch struct {
	kvdb.Batch
	c *countingHandle
}

func (b *countingBatch) Commit() error {
	b.c.commits.Add(1)
	time.Sleep(5 * time.Millisecond)
	return b.Batch.Commit()
}

func batchBody(key, value string) command.Body {
	return command.Body{hoarderSpecs.BodyOps: []interface{}{
		map[string]interface{}{hoarderSpecs.BodyKVOp: hoarderSpecs.KVPut, hoarderSpecs.BodyKey: key, hoarderSpecs.BodyValue: []byte(value)},
	}}
}

func groupWriteOf(body command.Body) *groupWrite {
	return newGroupWrite(body, body[hoarderSpecs.BodyOps].([]interface{}))
}

// Concurrent batches must share commits: one per batch held every waiter behind
// a separate DAG block, head update and broadcast.
func TestKVBatch_ConcurrentWritesShareCommits(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	inner, err := srv.dbFactory.New(logger, "group", 5)
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	handle := &countingHandle{KVDB: inner}

	const writers = 64
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("k-%d", i)
			if _, err := srv.kvBatch(ctx, handle, "group", batchBody(key, "v")); err != nil {
				t.Errorf("batch %s: %v", key, err)
			}
		}()
	}
	wg.Wait()

	if n := handle.commits.Load(); n >= writers/2 {
		t.Fatalf("%d writers took %d commits, want them grouped", writers, n)
	}
	for i := 0; i < writers; i++ {
		if v, err := inner.Get(ctx, fmt.Sprintf("k-%d", i)); err != nil || string(v) != "v" {
			t.Fatalf("k-%d = %q, %v", i, v, err)
		}
	}
}

// A refused write fails by itself; the others in its commit land.
func TestCommitGroup_RefusalIsPerWrite(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	handle, err := srv.dbFactory.New(logger, "refusal", 5)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()

	bad := newGroupWrite(command.Body{}, []interface{}{
		map[string]interface{}{hoarderSpecs.BodyKVOp: hoarderSpecs.KVPut, hoarderSpecs.BodyKey: "/bad", hoarderSpecs.BodyValue: []byte("x")},
		map[string]interface{}{hoarderSpecs.BodyKVOp: "nope", hoarderSpecs.BodyKey: "/worse"},
	})
	good := groupWriteOf(batchBody("/good", "y"))
	srv.commitGroup(ctx, handle, "refusal", []*groupWrite{bad, good})

	if bad.err == nil {
		t.Fatal("malformed write accepted")
	}
	if good.err != nil {
		t.Fatalf("good write failed with its neighbour: %v", good.err)
	}
	if v, err := handle.Get(ctx, "/bad"); err == nil && v != nil {
		t.Fatalf("refused write landed: %q", v)
	}
	if v, err := handle.Get(ctx, "/good"); err != nil || string(v) != "y" {
		t.Fatalf("good write = %q, %v", v, err)
	}
}

// Two writes of one key in a commit: the later one wins, whatever its bytes.
func TestCommitGroup_LaterWriteOfKeyWins(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	handle, err := srv.dbFactory.New(logger, "last", 5)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()

	w1, w2 := groupWriteOf(batchBody("/k", "zzz-earlier-and-larger")), groupWriteOf(batchBody("/k", "aaa"))
	srv.commitGroup(ctx, handle, "last", []*groupWrite{w1, w2})
	if w1.err != nil || w2.err != nil {
		t.Fatal(w1.err, w2.err)
	}
	if v, err := handle.Get(ctx, "/k"); err != nil || string(v) != "aaa" {
		t.Fatalf("/k = %q, %v, want the later write", v, err)
	}
}

// A K=2 copy must land while this node's own commit waits for a co-owner: that
// co-owner's commit is waiting for its copy to land here, so a shared queue
// deadlocks the two.
func TestKVBatch_CopyDoesNotWaitBehindCommit(t *testing.T) {
	srv := newTestService(t)
	handle, err := srv.dbFactory.New(logger, "cycle", 5)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()

	// The instance's committer is busy, as it is while waiting for a co-owner.
	q := srv.commitQueueOf("cycle")
	q.lock.Lock()
	q.running = true
	q.lock.Unlock()

	body := batchBody("copied", "v")
	body[hoarderSpecs.BodyNoBarrier] = true
	done := make(chan error, 1)
	go func() {
		_, err := srv.kvBatch(t.Context(), handle, "cycle", body)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("copy waited behind the instance's in-flight commit")
	}
}

// A write that outlived its caller must fail unapplied, so a backlog drains at
// once instead of commit by commit.
func TestCommitGroup_ExpiredWriteIsNotApplied(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	handle, err := srv.dbFactory.New(logger, "stale", 5)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()

	w := groupWriteOf(batchBody("old", "v"))
	w.queued = time.Now().Add(-maxQueueWait - time.Second)
	fresh := groupWriteOf(batchBody("new", "v"))
	srv.commitGroup(ctx, handle, "stale", []*groupWrite{w, fresh})

	if w.err == nil {
		t.Fatal("expired write did not fail")
	}
	if fresh.err != nil {
		t.Fatalf("fresh write failed: %v", fresh.err)
	}
	if v, err := handle.Get(ctx, "old"); err == nil && len(v) > 0 {
		t.Fatal("expired write was applied")
	}
}
