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

// The `sync` queue. The state machine (claim, visibility timeout, ack, attempts,
// backoff, dead letter) is pkg/queue's; this adds the namespace, the declaration and
// the pass that moves idle queues along (retention.go). Writes off the leader are
// refused with the leader's name. A Queue is opened per call and never cached by id,
// which would pin the config from before a redeploy.

// work is the queue half of this service.
type work struct {
	service *server

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

// claim hands out up to max items one at a time in a loop, keeping pkg/queue's
// one-entry-per-claim exclusion, in one round trip for the caller.
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

// depth is what a queue and its dead letter hold, whether or not this node ever
// opened them, so "never opened" cannot read as "empty".
func (w *work) depth(d declared) (Depth, error) {
	opened, err := w.open(d)
	if err != nil {
		return Depth{}, err
	}
	ready, claimed, err := opened.Depth()
	if err != nil {
		return Depth{}, err
	}

	out := Depth{Ready: uint64(ready), Claimed: uint64(claimed)}
	dead, err := opened.Dead()
	if err != nil {
		return out, nil
	}
	buried, _, err := dead.Depth()
	if err != nil {
		return Depth{}, err
	}
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
	case errors.Is(err, queue.ErrFull):
		return fmt.Errorf("%w: %s", ErrQueueFull, err)
	default:
		return err
	}
}

// index records that this namespace holds a queue, so the lifecycle pass can find
// one nobody claims from. A log's head is its index; pkg/queue escapes a queue's name
// into one private-encoded segment, so a key per queue, derived from the prefix and
// written once, is simpler than un-escaping.
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
func queuesIn(cluster raft.Cluster) ([]Spec, error) {
	keys, err := cluster.Keys(indexRoot)
	if err != nil {
		return nil, err
	}

	out := make([]Spec, 0, len(keys))
	for _, key := range keys {
		spec, ok := specFrom(strings.TrimPrefix(key, indexRoot))
		if ok && spec.Type == queueType {
			out = append(out, spec)
		}
	}
	return out, nil
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
