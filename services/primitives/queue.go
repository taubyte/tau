package primitives

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/pkg/raft"
)

// queue.go is the `sync` kind at `type: queue` — work handed from one workload
// to another, where a claim is exclusive and an unacknowledged item comes back.
//
// # The state machine is pkg/queue's and this service does not re-derive it
//
// Claim, visibility timeout, ack, attempt counting, backoff and the dead letter
// are implemented, tested and driven end to end in pkg/queue. Writing a second
// copy of those transitions here would be a second place the rules live, for no
// behaviour change. What this service adds is the three things a package with
// no lifecycle cannot have: the namespace the queues run in, the declaration
// they are configured from, and the pass that moves them along when nobody is
// claiming — see retention.go.
//
// # A write off the leader is refused and NOT forwarded
//
// That is pkg/queue's rule, and node/queue.go states the reason: a follower
// consumes nothing, and forwarding would be a second write path with its own
// failure modes. What is different here is that the refusal NAMES the leader,
// so a caller has somewhere to go rather than only something to try again.
//
// # Nothing is opened per queue, and the config is never cached
//
// A Queue value holds no state — everything is in the store — so opening one
// per call costs a struct. Caching by id would PIN THE FIRST CALLER'S CONFIG:
// an id does not change when its `max_attempts` does, so a node would keep
// enforcing the number from before the redeploy until it restarted.

// work is the queue half of this service.
type work struct {
	service *Service

	mu    sync.Mutex
	group *queue.Group

	// indexed is which queues this process has already written into the
	// namespace's own index. It is a cache of a write, not of a decision: the
	// value is derived from the prefix, so a node that forgot would write the
	// identical key again.
	indexed map[string]bool
}

// Delivery is one handing-out of one item, in this service's vocabulary rather
// than pkg/queue's — so a consumer reaching the service over the wire and one
// reaching it in-process hold the same value.
type Delivery struct {
	ID       string
	Data     []byte
	Attempt  uint32
	Deadline time.Time

	// Token identifies THIS delivery and must round-trip verbatim. A settle
	// carrying a token the queue no longer holds is refused, which is the only
	// thing stopping a consumer whose claim expired from acknowledging work that
	// has since been given to somebody else.
	Token string
}

// Depth is what a queue holds. It is observability and never a decision: it
// reads local state without a barrier, so on a follower it lags.
type Depth struct {
	Ready   uint64
	Claimed uint64

	// Dead is what the queue's dead letter holds. It is reported beside the
	// other two because a dead letter is not a declared resource — nothing can
	// name it — so this is the only way anybody sees one at all.
	Dead uint64
}

// open answers the queue a declaration describes.
func (w *work) open(d declared) (*queue.Queue, error) {
	group, err := w.ready()
	if err != nil {
		return nil, err
	}

	cfg := d.delivery
	cfg.ApplyTimeout = applyTimeout
	opened, err := group.Open(cfg)
	if err != nil {
		return nil, fmt.Errorf("primitives: opening the queue %s: %w", d.spec.Name, err)
	}
	return opened, nil
}

// send pushes one item.
func (w *work) send(d declared, id string, data []byte) error {
	if strings.TrimSpace(id) == "" {
		return ErrNoItemID
	}
	if len(data) > MaxItem {
		return tooLarge("that item", len(data), MaxItem)
	}

	opened, err := w.open(d)
	if err != nil {
		return err
	}
	if err := w.refusal(opened.Push(id, data)); err != nil {
		return err
	}

	// THE INDEX IS WRITTEN AFTER THE ITEM AND NOT BEFORE IT. It exists so the
	// lifecycle pass can find a queue nobody is claiming from, and a queue that
	// has never taken an item has nothing to sweep — so a failed push must not
	// leave the namespace naming a queue that was never written to.
	w.index(d.spec)
	return nil
}

// claim hands out up to max items.
//
// ONE AT A TIME, IN A LOOP, because that is pkg/queue's exclusion: every claim
// takes the group's lock and writes one entry, and a batch that handed out
// several in one entry would be a second transition rule beside the tested one.
// What the loop buys the caller is one round trip instead of max of them.
func (w *work) claim(d declared, max int) ([]Delivery, error) {
	if max <= 0 {
		max = 1
	}
	if max > maxClaim {
		max = maxClaim
	}

	opened, err := w.open(d)
	if err != nil {
		return nil, err
	}

	out := make([]Delivery, 0, max)
	for len(out) < max {
		delivered, err := opened.Claim()
		if errors.Is(err, queue.ErrNoItem) {
			// NOTHING DELIVERABLE IS AN EMPTY ANSWER AND NOT A REFUSAL. The
			// queue may still hold items — delayed, backing off, or claimed by
			// somebody else — and a consumer acts on "nothing for you right now"
			// by asking again, which is not what it does with an error.
			break
		}
		if err != nil {
			if len(out) > 0 {
				// WHAT WAS ALREADY HANDED OUT IS HANDED OUT. Those claims are
				// committed and their visibility timers are running; dropping
				// them on the floor would make the caller wait out every one.
				return out, nil
			}
			return nil, w.refusal(err)
		}
		out = append(out, Delivery{
			ID:       delivered.ID,
			Data:     delivered.Data,
			Attempt:  delivered.Attempt,
			Deadline: delivered.Deadline,
			Token:    delivered.Token,
		})
	}

	w.index(d.spec)
	return out, nil
}

// settle ends one delivery: done discards the item, and not-done gives it back
// now rather than waiting out the visibility timeout.
func (w *work) settle(d declared, delivery Delivery, done bool) error {
	opened, err := w.open(d)
	if err != nil {
		return err
	}

	handed := queue.Delivery{
		ID: delivery.ID, Data: delivery.Data, Attempt: delivery.Attempt,
		Deadline: delivery.Deadline, Token: delivery.Token,
	}
	if done {
		return w.refusal(opened.Ack(handed))
	}
	return w.refusal(opened.Nack(handed))
}

// depth is what a queue and its dead letter hold.
//
// IT IS NOT GATED ON THIS NODE HAVING SENT TO THE QUEUE, and that gate was a
// real mistake in node/queue.go's ancestor: a dead letter is never opened by
// anybody, so "this node never opened it" and "it is empty" were one answer and
// an assertion that nothing had been buried could not fail.
func (w *work) depth(d declared) (Depth, error) {
	opened, err := w.open(d)
	if err != nil {
		return Depth{}, err
	}
	ready, claimed := opened.Depth()

	out := Depth{Ready: uint64(ready), Claimed: uint64(claimed)}
	dead, err := opened.Dead()
	if err != nil {
		return out, nil
	}
	buried, _ := dead.Depth()
	out.Dead = uint64(buried)
	return out, nil
}

// refusal turns raft's own words into this service's.
//
// THE LEADER'S NAME TRAVELS WITH THE REFUSAL, which is the whole difference
// between "try again somewhere" and "try again there". Everything else passes
// through with pkg/queue's own sentence, because folding a stale delivery into
// a generic failure would tell a consumer its work was applied when it was not.
func (w *work) refusal(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, raft.ErrNotLeader):
		cluster, why := w.service.consensus()
		if why != nil {
			return why
		}
		return w.service.notLeading(cluster)
	case errors.Is(err, queue.ErrStaleDelivery):
		return fmt.Errorf("%w: %s", ErrStaleDelivery, err)
	case errors.Is(err, queue.ErrNotFound):
		return fmt.Errorf("%w: %s", ErrNoSuchDelivery, err)
	default:
		return err
	}
}

// index records that this namespace holds a queue, so the lifecycle pass can
// find one nobody is claiming from.
//
// # Why there is an index here and none for a log
//
// A log's head IS its index — one key per log, at a prefix a walk can find. A
// queue's keys are pkg/queue's, and pkg/queue escapes the whole name into ONE
// segment, so a walk of its keyspace answers escaped names this package would
// have to un-escape by reproducing a private encoding. Writing one key per queue
// is the smaller and more honest thing: it is derived from the prefix, so two
// nodes cannot write it differently, and it is written once and never changed —
// the same rule that makes a log's record keys safe.
func (w *work) index(spec Spec) {
	cluster, err := w.service.consensus()
	if err != nil {
		return
	}

	prefix := spec.Prefix()
	w.mu.Lock()
	already := w.indexed[prefix]
	w.mu.Unlock()
	if already {
		return
	}

	// A FAILED INDEX WRITE IS NOT A FAILED SEND. The item is committed; what is
	// missing is this node's note to itself, and the next call writes it.
	if err := cluster.Set(indexKey(prefix), nil, applyTimeout); err != nil {
		return
	}

	w.mu.Lock()
	if w.indexed == nil {
		w.indexed = map[string]bool{}
	}
	w.indexed[prefix] = true
	w.mu.Unlock()
}

// queuesIn is every queue this namespace has been written to.
func queuesIn(cluster raft.Cluster) []Spec {
	keys := cluster.Keys(indexRoot)

	out := make([]Spec, 0, len(keys))
	for _, key := range keys {
		spec, ok := specFrom(strings.TrimPrefix(key, indexRoot))
		if ok && spec.Type == queueType {
			out = append(out, spec)
		}
	}
	return out
}

// indexRoot is where this service keeps its own note of which queues exist. It
// is a segment neither pkg/queue nor log.go writes under, so the three keyspaces
// in this namespace never meet.
const indexRoot = "opened/"

func indexKey(prefix string) string { return indexRoot + prefix }

// ready is the group, or why there is not one yet. It never blocks.
func (w *work) ready() (*queue.Group, error) {
	cluster, err := w.service.consensus()
	if err != nil {
		return nil, err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.group == nil {
		group, err := queue.NewGroup(cluster, queue.WithClock(w.service.clock))
		if err != nil {
			return nil, fmt.Errorf("primitives: opening the queue group: %w", err)
		}
		w.group = group
	}
	return w.group, nil
}
