package backend

import (
	"fmt"
	"time"
)

// reconcile walks the open index for a job whose record was written and whose
// queue item was not (a crash between the two writes). Re-queueing is safe by
// queue.Push's own dedup; the age gate keeps the pass from a Push and barrier per
// open job. Dropping a finished job's open row is a delete with nothing to undo it,
// so it takes a barrier (mend).
func (s *Service) reconcile() error {
	if err := s.lead(); err != nil {
		return err
	}

	cutoff := s.cfg.Queue.VisibilityTimeout + s.cfg.BuildTimeout
	now := s.now()

	for _, key := range s.store.Keys(openIndexRoot()) {
		// BOTH halves, not just the id: mend deletes this row, and an entry
		// whose key it was merely handed cannot say what it deletes.
		repositoryKey, id, err := parseOpenIndexKey(key)
		if err != nil {
			return errInternal("%v", err)
		}
		if err := s.mend(repositoryKey, id, now, cutoff); err != nil {
			return err
		}
	}
	return nil
}

// mend is one row of the walk, one section per job so the pass never stalls
// Cancel and claims. The listing and the per-row read come from different
// prefixes, and a node regaining leadership can read a retried job as finished, so
// the delete branch takes a barrier and reads again (reread), deciding on that
// read; the first read is triage. The push needs no barrier: Push dedups, and a
// stale item is dropped by begin. Barrier on this branch costs one per
// orphan-looking row, not one per open job (barrier_test.go).
func (s *Service) mend(repositoryKey, id string, now time.Time, cutoff time.Duration) error {
	return s.withJobUnbarriered(id, func(j *held) error {
		// A row whose record is gone or finished is wrong, if still true at a caught-up
		// prefix.
		if !j.found() || j.rec().terminal() {
			if err := j.reread(); err != nil {
				return err
			}
		}

		if !j.found() || j.rec().terminal() {
			// The repository comes from the listed row since the record is gone; the job is
			// still the section's subject.
			if err := j.writes().dropOrphanedOpen(repositoryKey).commit("dropping the stale open row of"); err != nil {
				return err
			}
			return nil
		}

		if now.Sub(time.Unix(j.rec().UpdatedAt, 0)) < cutoff {
			return nil
		}
		if err := j.push(); err != nil {
			return fmt.Errorf("re-queueing job %s: %w", id, err)
		}
		return nil
	})
}
