package backend

import (
	"fmt"
	"time"
)

// reconcile walks the open index and repairs the one inconsistency this design
// can produce: a job whose record was written and whose queue item was not.
//
// Enqueue writes the record first and pushes second, and the two cannot be one
// entry because queue.Push does its own Batch. A crash in between leaves a
// record with no item — a job that is Open forever and that nothing will ever
// run. This is what notices. The other order would leave an item with no
// record, which is v1's orphan: unidentifiable, unrunnable, and only visible as
// a queue that never drains.
//
// THE RE-QUEUE'S ENTIRE SAFETY IS Push's OWN DEDUPLICATION. Pushing an id the
// queue already holds — ready, delayed, backing off or claimed — is a no-op, so
// this cannot touch a running job. The age gate is not part of that safety; it
// is what keeps the pass from issuing a Push, and therefore a barrier, per open
// job on every run.
//
// THE OTHER REPAIR IS A DELETE, AND IT IS NOT SAFE THE SAME WAY. Dropping the
// open-index row of a job that has finished is a write with no deduplication
// behind it and nothing to undo it, so it is the one thing this pass does that
// takes a barrier of its own — see mend, which says why the barrier is on that
// branch rather than on this listing.
//
// This is v1's re-announce made correct. v1's swept every job it had, on a
// timer, with a package-global bound on how many (MaxReAnnounceJobs), and it
// re-queued jobs another cluster was already running — its own cross-cluster
// check was fail-open and then undone by this very sweeper.
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

// mend is one row of that walk, and it is ONE SECTION per job rather than one
// for the whole pass: the listing may be every open job in the cloud, and
// holding every Cancel and every claim off for the length of it would make a
// repair pass a stall.
//
// THE SECTION IS WHAT MAKES THE DELETE SAFE AGAINST THIS PROCESS. Retry reopens
// a finished job by writing the record and this very row back in one entry;
// released between the read and the delete, this would drop the row a retry had
// just written and leave a job that reads Open and that nothing walking the
// open index will ever look at again — MaxOpenPerRepo stops counting it, and no
// later pass repairs it, because no later pass can see it. The push has the
// same shape one state over: decided on a record read as open, it would put an
// item back for a job a cancel finished in between.
//
// AND THE SECTION WAS NOT ENOUGH. The argument that it was said that a record
// and its open row move in the SAME entry — they do, and
// TestARecordAndItsOpenRowMoveInOneEntry checks it — so that no applied prefix
// of the log shows a row whose record is finished unless the row is genuinely
// orphaned. THE TWO HALVES OF THAT SENTENCE CAME FROM DIFFERENT PREFIXES. The
// row's presence is established by reconcile's listing, once, behind
// reconcile's own barrier; the record is read here, per row, arbitrarily later.
// A node that regains leadership mid-walk holds entries that are committed and
// not applied, so it can read a job as finished while the retry that reopened
// it is already ordered ahead of anything this pass will write — and
// raft.BatchOp has no compare-and-swap, so the delete lands on the row that
// retry wrote. TestMendDoesNotDropAnOpenRowItReadAtAnOlderPrefix is that
// measured rather than argued.
//
// SO THE DELETE BRANCH TAKES A BARRIER AND READS AGAIN, and it is the only
// branch that does:
//
//   - THE FIRST READ IS TRIAGE AND DECIDES NOTHING. A row it finds live goes to
//     the age gate below; a row it finds finished is read a second time by
//     reread(), which takes the barrier and reads UNDER THIS SAME HOLDING, and
//     it is THAT read the delete is decided on. A test of this
//     therefore has to be released at the SECOND read: armed at the first, the
//     retry it lets go is caught by the re-read and the section is never
//     reached, which is how PAIR 4 came to pass two thousand runs against a
//     mend with no section at all.
//   - THE PUSH NEEDS NO BARRIER OF ITS OWN. Its safety is queue.Push's
//     deduplication, which reads the queue's own index behind the queue's own
//     barrier, and an item pushed for a job that has since finished is dropped
//     by begin on the next claim and acked there. A stale read here costs a
//     wasted claim cycle, never a wrong write.
//   - AND THE COST IS WHY IT IS THIS BRANCH AND NOT THE ENTRY POINT. A barrier
//     is a replicated log entry. Taken before the first read — which is what
//     withJob does for every other transition, and why mend is the one caller
//     of withJobUnbarriered — it is one per open job per pass, forever; taken
//     here it is one per row that LOOKS orphaned, which in steady state is
//     none. barrier_test.go measures both: a
//     pass over sixteen live open jobs still costs one, and a pass over four
//     orphaned rows costs one plus four.
func (s *Service) mend(repositoryKey, id string, now time.Time, cutoff time.Duration) error {
	return s.withJobUnbarriered(id, func(j *held) error {
		// An index row whose record is gone, or whose record has finished, is
		// the row that is wrong — but only if that is still true of a prefix
		// this node has caught up to. Deleting it is what keeps MaxOpenPerRepo
		// counting real work rather than history.
		if !j.found() || j.rec().terminal() {
			if err := j.reread(); err != nil {
				return err
			}
		}

		if !j.found() || j.rec().terminal() {
			// THE REPOSITORY COMES IN AND THE ID DOES NOT. This is the one
			// write in the package whose key the handle cannot compose on its
			// own: the record that named the repository is gone, so what is
			// left is the row reconcile listed. The half that says WHICH JOB is
			// still the section's subject — section.go names this the
			// exception and population_test.go enumerates its one caller.
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
