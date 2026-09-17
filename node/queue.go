package node

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/pkg/raft"
)

// queue.go backs `taubyte:queue/producer` on this node, over pkg/queue — the
// same replicated work queue the build service already runs its jobs through.
//
// # One raft namespace for every queue a project declares
//
// pkg/queue's key layout is `queue/<name>/…`, so many queues share one store
// without seeing each other, and the name carries the deployment. A raft
// cluster per queue would be a consensus group per resource, which is a
// leader election and a log per declared queue.
//
// # A write off the leader is refused and NOT forwarded
//
// That is pkg/queue's rule and services/build states the reason at length: a
// follower consumes nothing, and forwarding would be a second write path with
// its own failure modes. What reaches the guest is `not-writable`, which is an
// arm a caller retries — the gateway will place the next request somewhere
// else, and somewhere else is eventually the leader.
//
// WHAT WOULD REMOVE THE RETRY is placement that knows about leadership, which
// is a routing change rather than a queue one.
//
// # The cluster is formed in the background, and a send before it is is refused
//
// raft.New BLOCKS FOR UP TO ITS BOOTSTRAP TIMEOUT — thirty seconds — waiting
// for peers to appear so that a cloud does not bootstrap a single-node cluster
// it later has to heal. That is right at boot and wrong on a request path: the
// first `queue.send` in a cloud paid it, held every other caller behind the
// same lock, and timed the invocation out.
//
// So the group is opened once, off the request path, and a send that arrives
// before it exists is told `unavailable` — an arm the vocabulary already has
// and which a caller retries. Nothing waits.

// queues is the node's Queues: one raft cluster, and a Queue opened per call
// over it.
type queues struct {
	node *Node

	mu sync.Mutex

	// forming is closed when the cluster has been opened or has failed to
	// open, so a caller can tell "not yet" from "not ever" without waiting.
	forming chan struct{}

	group   *queue.Group
	cluster raft.Cluster
	formErr error
}

// queueDomain is the raft namespace every declared queue lives in. It is not
// the build service's: a project's work and this cloud's own builds are
// different lifetimes, different sizes and different blast radii.
const queueDomain = "/tau/queues/v1"

// ErrQueuesForming is a send that arrived before this node's consensus for
// queues had formed. It is a RETRY and not a failure, which is why it reaches a
// guest as `unavailable` rather than as a missing queue.
var ErrQueuesForming = errors.New("node: the queue cluster is still forming")

// form opens the raft cluster this node's queues live in, once, in the
// background. The node starts it; nothing calls it on a request path.
func (q *queues) form() {
	q.mu.Lock()
	if q.forming != nil || q.node.opts.raft == nil {
		q.mu.Unlock()
		return
	}
	forming := make(chan struct{})
	q.forming = forming
	q.mu.Unlock()

	go func() {
		defer close(forming)

		cluster, err := q.node.opts.raft(queueDomain)
		if err != nil {
			q.mu.Lock()
			q.formErr = fmt.Errorf("node: opening %s: %w", queueDomain, err)
			q.mu.Unlock()
			return
		}
		group, err := queue.NewGroup(cluster)

		q.mu.Lock()
		q.cluster, q.group, q.formErr = cluster, group, err
		q.mu.Unlock()
	}()
}

// ready is the group, or why there is not one yet. It never blocks.
func (q *queues) ready() (*queue.Group, error) {
	if q.node.opts.raft == nil {
		return nil, ErrNoRaft
	}

	q.mu.Lock()
	forming, group, err := q.forming, q.group, q.formErr
	q.mu.Unlock()

	if forming == nil {
		// The node was never started, so nothing has begun forming a cluster.
		return nil, ErrQueuesForming
	}
	select {
	case <-forming:
	default:
		return nil, ErrQueuesForming
	}

	if err != nil {
		return nil, err
	}
	if group == nil {
		// Closed underneath us.
		return nil, ErrQueuesForming
	}
	return group, nil
}

// Send pushes one item.
func (q *queues) Send(ctx context.Context, grant invoke.Grant, id string, data []byte) error {
	held, err := q.queue(grant)
	if err != nil {
		return err
	}

	return wrapNotLeader(held.Push(id, data))
}

// wrapNotLeader turns raft's refusal into the tier's arm, so a guest is told to
// RETRY rather than to give up — see the file doc for why nothing forwards.
func wrapNotLeader(err error) error {
	if err != nil && errors.Is(err, raft.ErrNotLeader) {
		return fmt.Errorf("%w: %s", wasmtier.ErrNotWritable, err)
	}
	return err
}

// queue opens the one a grant names.
//
// IT IS OPENED PER CALL AND NOT CACHED, which is what pkg/queue's own doc asks
// for: "a Queue holds no state of its own" and "opening the same name twice is
// harmless", because the two share the group's lock and are one writer. A cache
// here would PIN THE FIRST CALLER'S CONFIG — the handle carries the resource
// id and an id does not change when its `max_attempts` does, so a node would
// keep enforcing the number from before the redeploy until it restarted.
func (q *queues) queue(grant invoke.Grant) (*queue.Queue, error) {
	group, err := q.ready()
	if err != nil {
		return nil, err
	}

	opened, err := group.Open(configOf(grant))
	if err != nil {
		return nil, fmt.Errorf("node: opening the queue %q: %w", grant.Label, err)
	}

	return opened, nil
}

// The setting keys services/worker writes, which are the vocabulary's own
// attribute paths.
//
// THEY ARE UNDER `queue/` BECAUSE `sync` IS A TYPED KIND. A queue is `sync` at
// `type: queue`, so every field it declares lives in the block named after the
// type — the shape a database's `sql/` and a storage's `object/` already have.
// Reading them at the root found nothing at all, which is not an error anywhere:
// every queue would open with pkg/queue's defaults and the author's visibility
// timeout, retry budget and dead letter would silently stop taking effect.
const (
	settingVisibility   = "queue/visibility_timeout"
	settingDelay        = "queue/delay"
	settingMaxAttempts  = "queue/max_attempts"
	settingRetryEvery   = "queue/retry/interval"
	settingRetryFactor  = "queue/retry/factor"
	settingRetryCeiling = "queue/retry/ceiling"
	settingDeadLetter   = "queue/dead_letter/name"
)

// configOf reads what the resource declared.
//
// THE NAME IS THE HANDLE, which carries the project, the deployment and the
// resource id — so two deployments of one queue are two queues, and two
// projects using the name `jobs` never meet. pkg/queue escapes it into one key
// segment, so the separators in it are safe.
//
// A DEAD LETTER IS A REFERENCE TO ANOTHER QUEUE, and the compiled value is that
// queue's id. It is scoped the same way, so it lands beside the one it drains.
func configOf(grant invoke.Grant) queue.Config {
	project, environment, _ := splitHandle(grant.Handle)

	cfg := queue.Config{Name: grant.Handle}
	if dead := grant.Settings[settingDeadLetter]; dead != "" {
		cfg.DeadLetter = project + "/" + environment + "/" + dead
	}

	cfg.VisibilityTimeout = duration(grant.Settings[settingVisibility])
	cfg.Delay = duration(grant.Settings[settingDelay])
	cfg.RetryInterval = duration(grant.Settings[settingRetryEvery])
	cfg.RetryCeiling = duration(grant.Settings[settingRetryCeiling])

	if n, err := strconv.ParseUint(grant.Settings[settingMaxAttempts], 10, 32); err == nil {
		cfg.MaxAttempts = uint32(n)
	}
	if f, err := strconv.ParseFloat(grant.Settings[settingRetryFactor], 64); err == nil {
		cfg.RetryFactor = f
	}
	return cfg
}

// Depth is what a queue holds, for an operator asking.
//
// IT IS NOT GATED ON THIS NODE HAVING OPENED IT, and that gate was a real
// mistake: a DEAD LETTER is never opened by anybody — it deliberately has no
// consumer — and an item is buried into it by the source queue writing its
// keys directly. So "this node never opened it" and "it is empty" were one
// answer, and an assertion that nothing had been buried could not fail. That is
// how a workflow step that buried every run through it passed an end-to-end
// test.
//
// `ok` says only whether this node has consensus to read at all. What a queue
// holds is a count of keys under its own prefix, and a queue nobody has written
// to holds nothing, which is the honest answer rather than a refusal to say.
//
// THE CONFIG IS THE NAME AND NOTHING ELSE, because the prefix is derived from
// the name — none of the timing, retry or dead-letter fields moves a key.
func (q *queues) Depth(handle string) (ready, claimed int, ok bool) {
	q.mu.Lock()
	group := q.group
	q.mu.Unlock()

	if group == nil {
		return 0, 0, false
	}
	opened, err := group.Open(queue.Config{Name: handle})
	if err != nil {
		return 0, 0, false
	}
	ready, claimed = opened.Depth()
	return ready, claimed, true
}

// Close releases the cluster this node opened, once it has finished opening —
// tearing one down mid-bootstrap would leave the goroutine writing into a
// closed store. The queues themselves hold no state: everything is in the
// store, which is what lets a process die mid-claim and the claim expire.
func (q *queues) Close() error {
	q.mu.Lock()
	forming := q.forming
	q.mu.Unlock()
	if forming != nil {
		<-forming
	}

	q.mu.Lock()
	cluster := q.cluster
	q.group = nil
	q.cluster = nil
	q.mu.Unlock()

	if cluster != nil {
		cluster.Close()
	}
	return nil
}

// consensus is the raft cluster this node's coordinated state lives in, or why
// there is not one yet. It never blocks — see queue.go for why forming it is a
// background job.
//
// IT IS THE QUEUES' CLUSTER AND THAT IS DELIBERATE. A cron's "what has already
// fired" and a project's work are coordinated state about the same deployment;
// a second cluster would be a second consensus group, a second election and a
// second log for one project.
func (q *queues) consensus() (raft.Cluster, error) {
	if _, err := q.ready(); err != nil {
		return nil, err
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if q.cluster == nil {
		return nil, ErrQueuesForming
	}
	return q.cluster, nil
}
