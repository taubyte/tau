package hoarder

import (
	"context"
	"fmt"
	"path"
	"sync"
	"time"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	"github.com/taubyte/tau/utils/maps"
)

// A commit costs one DAG block, one head update and one broadcast, whether it
// carries 1 op or 256, so concurrent batches of an instance are merged into one
// commit. These bound a commit so it stays under the CRDT's 1 MiB delta limit,
// above which a batch is split and no longer atomic.
const (
	maxCommitOps   = 256
	maxCommitBytes = 512 << 10
)

// maxQueueWait is how long a write may wait for its commit. Its caller has given
// up well before this, so committing it would only delay the writes behind it.
const maxQueueWait = 20 * time.Second

// copyQueueSuffix names the queue of an instance's K=2 copies. They must not wait
// behind this node's commits: those wait for a co-owner's ack, and that co-owner's
// commits wait for this node's copies to land, so sharing a queue deadlocks.
const copyQueueSuffix = "/copies"

// groupWrite is one batch waiting for its instance's next commit.
type groupWrite struct {
	body   command.Body
	rawOps []interface{}
	size   int
	resp   cr.Response
	err    error
	done   chan struct{}
	queued time.Time
}

func newGroupWrite(body command.Body, rawOps []interface{}) *groupWrite {
	w := &groupWrite{body: body, rawOps: rawOps, done: make(chan struct{}), queued: time.Now()}
	for _, ro := range rawOps {
		if v, err := maps.ByteArray(maps.SafeInterfaceToStringKeys(ro), hoarderSpecs.BodyValue); err == nil {
			w.size += len(v)
		}
	}
	return w
}

type commitQueue struct {
	lock    sync.Mutex
	pending []*groupWrite
	running bool
}

func (srv *Service) commitQueueOf(hash string) *commitQueue {
	srv.ldr.lock.Lock()
	defer srv.ldr.lock.Unlock()
	q, ok := srv.ldr.commitQueues[hash]
	if !ok {
		q = new(commitQueue)
		srv.ldr.commitQueues[hash] = q
	}
	return q
}

// enqueueWrite adds w to the instance's queue, starting its committer if idle.
func (srv *Service) enqueueWrite(handle kvdb.KVDB, hash string, w *groupWrite) {
	key := hash
	if nb, _ := maps.Bool(w.body, hoarderSpecs.BodyNoBarrier); nb {
		key += copyQueueSuffix
	}
	q := srv.commitQueueOf(key)
	q.lock.Lock()
	q.pending = append(q.pending, w)
	start := !q.running
	q.running = true
	q.lock.Unlock()
	if start {
		go srv.runCommits(handle, hash, q)
	}
}

// runCommits drains the queue one commit at a time, so writes that arrive
// during a commit share the next one. It exits when the queue is empty.
func (srv *Service) runCommits(handle kvdb.KVDB, hash string, q *commitQueue) {
	for {
		q.lock.Lock()
		group := takeGroup(q)
		if len(group) == 0 {
			q.running = false
			q.lock.Unlock()
			return
		}
		q.lock.Unlock()
		srv.commitGroup(srv.node.Context(), handle, hash, group)
	}
}

// takeGroup removes the next commit's writes from q; the first is always taken,
// however large. Called with q.lock held.
func takeGroup(q *commitQueue) []*groupWrite {
	ops, size, n := 0, 0, 0
	for n < len(q.pending) {
		w := q.pending[n]
		if n > 0 && (ops+len(w.rawOps) > maxCommitOps || size+w.size > maxCommitBytes) {
			break
		}
		ops += len(w.rawOps)
		size += w.size
		n++
	}
	group := q.pending[:n:n]
	q.pending = q.pending[n:]
	return group
}

// commitGroup applies the writes as one commit, then makes their K=2 copy as
// one push. Each write is validated alone: a refused one fails by itself and
// leaves nothing in the commit.
func (srv *Service) commitGroup(ctx context.Context, handle kvdb.KVDB, hash string, group []*groupWrite) {
	defer func() {
		for _, w := range group {
			close(w.done)
		}
	}()

	mu := srv.writeLock(hash)
	mu.Lock()
	used := srv.usageOf(hash)
	var ops []batchOp
	var pending int64
	accepted := make([]*groupWrite, 0, len(group))
	for _, w := range group {
		if time.Since(w.queued) > maxQueueWait {
			w.err = fmt.Errorf("write waited over %s for its commit", maxQueueWait)
			continue
		}
		out, delta, resp, err := srv.checkBatch(ctx, handle, used, w.body, w.rawOps, pending)
		if resp != nil || err != nil {
			w.resp, w.err = resp, err
			continue
		}
		ops = append(ops, out...)
		pending += delta
		accepted = append(accepted, w)
	}
	err := commitOps(ctx, handle, lastPerKey(ops))
	if err == nil && used.known {
		used.bytes += pending
	}
	mu.Unlock()

	if err != nil {
		for _, w := range accepted {
			w.err = err
		}
		return
	}

	copiedTo, err := srv.replicateGroup(ctx, hash, accepted)
	for _, w := range accepted {
		w.resp, w.err = acked(copiedTo), err
	}
}

// commitOps writes ops through the kvdb's native batch.
func commitOps(ctx context.Context, handle kvdb.KVDB, ops []batchOp) error {
	if len(ops) == 0 {
		return nil
	}
	batch, err := handle.Batch(ctx)
	if err != nil {
		return fmt.Errorf("opening batch failed with: %w", err)
	}
	for _, op := range ops {
		if op.del {
			err = batch.Delete(op.key)
		} else {
			err = batch.Put(op.key, op.value)
		}
		if err != nil {
			return err
		}
	}
	if err = batch.Commit(); err != nil {
		return fmt.Errorf("batch commit failed with: %w", err)
	}
	return nil
}

// lastPerKey keeps the last write of each key. Ops of one commit share a CRDT
// priority, where the larger value would win instead of the later write.
func lastPerKey(ops []batchOp) []batchOp {
	last := make(map[string]int, len(ops))
	for i, op := range ops {
		last[path.Clean("/"+op.key)] = i
	}
	kept := make([]batchOp, 0, len(last))
	for i, op := range ops {
		if last[path.Clean("/"+op.key)] == i {
			kept = append(kept, op)
		}
	}
	return kept
}

// replicateGroup pushes the accepted writes to a co-owner as one batch. Writes
// that are themselves K=2 copies are not copied again.
func (srv *Service) replicateGroup(ctx context.Context, hash string, accepted []*groupWrite) (string, error) {
	var ops []interface{}
	var base command.Body
	for _, w := range accepted {
		if nb, _ := maps.Bool(w.body, hoarderSpecs.BodyNoBarrier); nb {
			continue
		}
		ops = append(ops, w.rawOps...)
		base = w.body
	}
	if base == nil {
		return "", nil
	}
	body := make(command.Body, len(base))
	for k, v := range base {
		body[k] = v
	}
	delete(body, hoarderSpecs.BodyMaxSize)
	body[hoarderSpecs.BodyOps] = ops
	return srv.replicateWrite(ctx, hash, body)
}
