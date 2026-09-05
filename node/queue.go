package node

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/pkg/raft"
)

// queue.go backs `taubyte:handler/queue` on this node, over pkg/queue — the
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

// queues is the node's Queues: one raft cluster, one Queue per handle.
type queues struct {
	node *Node

	mu      sync.Mutex
	group   *queue.Group
	cluster raft.Cluster
	open    map[string]*queue.Queue
}

var _ wasmtier.Queues = (*queues)(nil)

// queueDomain is the raft namespace every declared queue lives in. It is not
// the build service's: a project's work and this cloud's own builds are
// different lifetimes, different sizes and different blast radii.
const queueDomain = "/tau/queues/v1"

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

// queue opens the one a grant names, once, and keeps it.
func (q *queues) queue(grant invoke.Grant) (*queue.Queue, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if held, ok := q.open[grant.Handle]; ok {
		return held, nil
	}

	if q.group == nil {
		if q.node.opts.raft == nil {
			return nil, ErrNoRaft
		}
		cluster, err := q.node.opts.raft(queueDomain)
		if err != nil {
			return nil, fmt.Errorf("node: opening %s: %w", queueDomain, err)
		}
		q.cluster = cluster
		q.node.closers = append(q.node.closers, closerFunc(func() { cluster.Close() }))

		group, err := queue.NewGroup(cluster)
		if err != nil {
			return nil, err
		}
		q.group = group
	}

	opened, err := q.group.Open(configOf(grant))
	if err != nil {
		return nil, fmt.Errorf("node: opening the queue %q: %w", grant.Label, err)
	}

	if q.open == nil {
		q.open = map[string]*queue.Queue{}
	}
	q.open[grant.Handle] = opened
	return opened, nil
}

// The setting keys services/worker writes, which are the vocabulary's own
// attribute paths.
const (
	settingVisibility   = "visibility_timeout"
	settingDelay        = "delay"
	settingMaxAttempts  = "max_attempts"
	settingRetryEvery   = "retry/interval"
	settingRetryFactor  = "retry/factor"
	settingRetryCeiling = "retry/ceiling"
	settingDeadLetter   = "dead_letter/name"
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

// Depth is what a queue holds, for an operator asking. It opens nothing that is
// not already open.
func (q *queues) Depth(handle string) (ready, claimed int, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	held, open := q.open[handle]
	if !open {
		return 0, 0, false
	}
	ready, claimed = held.Depth()
	return ready, claimed, true
}

// Close releases the cluster this node opened. The queues themselves hold no
// state — everything is in the store, which is the property that lets a process
// die mid-claim and the claim expire.
func (q *queues) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.open = nil
	q.group = nil
	return nil
}

// sweepEvery is how often a node returns expired claims to the ready set.
//
// IT IS NOT A TIMER ON A CLAIM. pkg/queue expires a claim by reading the clock
// when something looks, so nothing is lost if no node sweeps — a claim simply
// stays invisible until the next claim or sweep notices. The sweep is what
// makes "the consumer died" recover promptly rather than at the next push.
const sweepEvery = 30 * time.Second
