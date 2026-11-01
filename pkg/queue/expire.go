package queue

import (
	"time"

	"github.com/taubyte/tau/pkg/raft"
)

// A dead letter's default name (Name+[DeadLetterSuffix]) has no consumer, so buried
// items would stay forever in a log held in memory on every voter. The queue that
// buries owns the expiry: an age and a size, as a log's retention. Only ready items
// are dropped; a claimed item comes back ready when its claim expires.

// Expire drops the oldest ready items until the queue is within a retention
// budget, and reports how many it dropped.
//
// age drops an item that became deliverable longer ago than that; size keeps
// the newest items whose payloads fit in that many bytes and drops the rest.
// Zero means the budget is absent; both absent keeps everything.
//
// All drops go into one entry at the end, so a pass that stops early applied and
// reports nothing. The newest item survives a size budget it cannot fit in: a
// budget too small for one item is a misconfiguration, not a reason to empty the
// queue. An age budget does drop it.
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

	// Newest first, so the size budget is spent on what a reader most likely wants.
	// Ready keys sort by deliverable time, which for a buried item is its burial.
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
	// One entry for the whole pass: every drop applies or none does.
	if err := q.group.store.Batch(ops, q.cfg.ApplyTimeout); err != nil {
		return 0, err
	}
	return dropped, nil
}

// Dead is this queue's dead letter, opened on the same group, so a caller does not
// rebuild the derived name by hand.
func (q *Queue) Dead() (*Queue, error) {
	return q.group.Open(Config{
		Name:         q.cfg.DeadLetter,
		ApplyTimeout: q.cfg.ApplyTimeout,

		// A dead letter's own dead letter is a second hop nothing drains.
		DeadLetter: q.cfg.DeadLetter + DeadLetterSuffix,
	})
}
