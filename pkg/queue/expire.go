package queue

import (
	"time"

	"github.com/taubyte/tau/pkg/raft"
)

// expire.go bounds what a queue keeps, and it exists because the dead letter
// had nothing bounding it at all.
//
// # The failure path was the unbounded one
//
// [Queue.bury] moves an exhausted item into the dead-letter queue and NOTHING
// EVER TOOK IT OUT AGAIN. A dead letter is deliberately a queue like any other,
// so draining one is a consumer's job — but the default dead letter is
// Name+[DeadLetterSuffix], which is not a name anybody declared, so it has no
// consumer and never will. Every item a workload permanently fails therefore
// stayed in the store forever, and this store is a replicated log whose state
// machine is held in memory on every voter.
//
// So the queue that owns the burial owns the expiry. It is the same drop-oldest
// pair a log's retention uses — an age and a size — and it means the same
// thing, so an author who learned one learned both.
//
// # Ready items only
//
// A CLAIMED ITEM IS SOMEBODY'S WORK IN FLIGHT and retention does not get to end
// it. If its consumer is gone the claim expires by itself and the item comes
// back ready, where this can see it on the next pass — so nothing is exempt
// forever, and nothing is taken out from under a consumer that is still running.

// Expire drops the oldest ready items until the queue is within a retention
// budget, and reports how many it dropped.
//
// age drops an item that became deliverable longer ago than that; size keeps
// the newest items whose payloads fit in that many bytes and drops the rest.
// Zero means the budget is absent, and both absent is a queue that keeps
// everything — the same "absent means forever" the vocabulary writes for a log.
//
// WHAT IT REPORTS IS WHAT IT APPLIED. Every drop this pass decides on goes into
// one entry at the end, so a pass that stops early applied nothing and reports
// nothing — a count of the operations it had built up by then would be a number
// no store ever saw.
//
// THE NEWEST ITEM SURVIVES A SIZE BUDGET IT CANNOT FIT IN, for the reason
// node/log.go keeps its newest record: a budget too small to hold one item is a
// misconfiguration, and answering it by emptying the queue discards work that
// was just accepted. An AGE budget does drop it, because an item older than the
// bound is what the bound is about.
func (q *Queue) Expire(age time.Duration, size int64) (int, error) {
	if age <= 0 && size <= 0 {
		return 0, nil
	}

	q.group.mu.Lock()
	defer q.group.mu.Unlock()

	if err := q.prepare(); err != nil {
		return 0, err
	}

	now := q.group.now()
	cutoff := now.Add(-age)

	// NEWEST FIRST, so the size budget is spent on what a reader is most likely
	// to want and the walk carries a running total rather than a second pass.
	// Ready keys sort by the time they become deliverable, which for a buried
	// item is when it was buried.
	keys := q.sorted(q.keys.readyPrefix())

	var held int64
	var ops []raft.BatchOp
	dropped := 0
	for i := len(keys) - 1; i >= 0; i-- {
		key := keys[i]
		due, err := q.keys.parse(key)
		if err != nil {
			return 0, err
		}
		raw, exists := q.group.store.Get(key)
		if !exists {
			// The listing and the read are not one operation. A key that went
			// between them is a key somebody else already removed.
			continue
		}
		rec, err := decodeRecord(key, raw)
		if err != nil {
			return 0, err
		}
		held += int64(len(rec.Data))

		beyond := (age > 0 && due.at.Before(cutoff)) ||
			(size > 0 && held > size && i < len(keys)-1)
		if !beyond {
			continue
		}

		ops = append(ops, del(key), del(q.keys.index(rec.ID)))
		dropped++
	}

	if len(ops) == 0 {
		return 0, nil
	}
	// ONE ENTRY FOR THE WHOLE PASS, which is what keeps an item from being in
	// neither place: either every drop this pass decided on is applied or none
	// of it is.
	if err := q.group.store.Batch(ops, q.cfg.ApplyTimeout); err != nil {
		return 0, err
	}
	return dropped, nil
}

// Dead is this queue's dead letter, opened on the same group.
//
// IT IS HERE BECAUSE NOTHING ELSE CAN NAME ONE. The default dead letter is a
// name derived from this queue's, and a caller that built that name by hand
// would be reproducing [Config.withDefaults] somewhere it cannot see it. The
// value it answers is a queue like any other: drain it, measure it, expire it.
func (q *Queue) Dead() (*Queue, error) {
	return q.group.Open(Config{
		Name:         q.cfg.DeadLetter,
		ApplyTimeout: q.cfg.ApplyTimeout,

		// A DEAD LETTER INHERITS NOTHING ELSE. Its own dead letter would be a
		// second hop nothing drains, and it is explicitly refused: a queue that
		// dead-lettered into itself would redeliver an exhausted item forever,
		// which is what [ErrSelfDeadLetter] names.
		DeadLetter: q.cfg.DeadLetter + DeadLetterSuffix,
	})
}
