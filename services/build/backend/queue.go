package backend

import (
	"context"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/raft"
	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/services/build/backend/section"
)

// queueHandler is the connect adapter for QueueService. Every method on it is
// three lines — authorize, lead, delegate — and the body is a plain method with
// no connect types in it.
//
// THAT SPLIT IS WHAT MAKES THIS PACKAGE TESTABLE. rpc.RemotePeer reads a
// context value under a key private to package rpc, and connect.Request's peer
// field is unexported with no setter, so NO TEST OUTSIDE p2p/rpc CAN FABRICATE
// A CALLER IDENTITY. A handler that requires one is therefore unreachable by a
// direct call, and a package whose whole logic lived in its handlers would be
// reachable only over the wire.
type queueHandler struct{ s *Service }

func (h *queueHandler) Enqueue(ctx context.Context, req *connect.Request[buildv1.PushEvent]) (*connect.Response[buildv1.JobRef], error) {
	if err := h.s.allow(ctx, ActEnqueue, req.Msg.GetRepositoryKey()); err != nil {
		return nil, err
	}
	id, err := h.s.enqueue(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&buildv1.JobRef{Id: id}), nil
}

// enqueue durably records a push and returns its job id.
//
// IT IS IDEMPOTENT, AND CORRECTLY SO rather than tolerantly: a job is a
// function of (repository, ref, commit) and nothing else, so two pushes landing
// on one triple have nothing different to build. The job id IS that
// idempotency — deterministic, so queue.Push's own id deduplication and this
// method's are one mechanism — and there is no separate dedup index for the two
// to disagree about.
//
// THE TWO DELIVERIES OVERLAP. A webhook is redelivered when the sender did not
// see the response, which is the response still being computed, so the second
// call arrives WHILE the first is in flight rather than after it. That is why
// the decision and the write it justifies are one section (create) and why the
// only thing between them is a barrier: read the table, ask auth, write what
// the FIRST read said, and a delivery that overtakes another puts a finished
// job back to Open with its attempts rewound and mints it a second queue item —
// one push, built twice.
//
// Every argument here originates in an unauthenticated POST to the frontend
// (build.proto: "the only one whose contents a STRANGER authored"), so this is
// the last place the three are looked at before they become a hash and three
// key segments.
func (s *Service) enqueue(ctx context.Context, p *buildv1.PushEvent) (string, error) {
	key, ok := parseRepositoryKey(p.GetRepositoryKey())
	if !ok {
		return "", errInvalidArgument("repository key %q is not canonical", p.GetRepositoryKey())
	}
	// The frontend already decided branch eligibility, before Enqueue was
	// called (build.proto). This is not a second policy filter — it is the
	// shape check that keeps a nonsense ref from becoming a job.
	if !validRef(p.GetRef()) {
		return "", errInvalidArgument("ref %q is not a branch ref this service will build", p.GetRef())
	}
	if !validCommitSHA(p.GetCommitSha()) {
		return "", errInvalidArgument("commit %q is not a full object name", p.GetCommitSha())
	}

	if err := s.lead(); err != nil {
		return "", err
	}

	id := jobID(key, p.GetRef(), p.GetCommitSha())

	// A push this service already has is answered without asking auth anything.
	found, err := s.resume(id)
	if err != nil {
		return "", err
	}
	if found {
		return id, nil
	}

	if err := s.admit(key.String()); err != nil {
		return "", err
	}

	// A repository nobody registered with this cloud is refused BEFORE anything
	// is queued. It is the admission check on stranger-authored input that
	// costs nothing extra, because the project id has to be resolved anyway.
	projectID, err := s.repos.Project(ctx, key.String())
	if err != nil {
		return "", s.projectError(key.String(), err)
	}
	if projectID == "" {
		return "", errFailedPrecondition("repository %q belongs to no project", key)
	}

	return id, s.create(id, key.String(), p.GetRef(), p.GetCommitSha(), projectID)
}

// resume answers a push this service already has, and IS that whole answer: the
// read that finds the job and the item it puts back are one section, so a
// cancel that withdrew the item between them cannot have it pushed straight
// back for a job nobody will ever build.
//
// It is a saved round trip and not a decision about the job — create reads the
// same row again at the moment it writes — but the push it makes is a decision,
// and that is why the read is behind the lock.
func (s *Service) resume(id string) (bool, error) {
	sec := jobLock.Enter()
	defer sec.Leave()

	if err := s.lead(); err != nil {
		return false, err
	}
	r, err := s.record(sec, id)
	if err != nil || !r.Found() {
		return false, err
	}
	return true, s.requeue(r)
}

// jobLock makes a job record's read and the entry that read decides ONE
// decision, wherever in this package the two appear.
//
// EVERY TRANSITION OF A JOB IS READ-MODIFY-WRITE. Enqueue's idempotency, the
// claim, the settle, a cancel, a retry, the dead-letter drain and reconcile all
// read the record, decide on what it said, and write it back — and a read is
// only true until something else writes.
//
// NOTHING OFF THIS PROCESS CAN WRITE ONE. A write commits on the leader alone,
// so another node deciding this same job has to take leadership first and this
// node's Batch then does not commit — and every section takes a Barrier of its
// own before the read its writes are decided on, so a node that has just won an
// election cannot decide on entries it has not applied. begin's and
// drainDeadLetter's lead(), settle's ack, and the lead() the four rpc-side
// transitions already made are those barriers. mend's is the one that is not
// before the section's FIRST read: it triages on a read that decides nothing
// and takes its barrier on the branch that deletes an open-index row, because
// taken at the top it would be one replicated entry per open job per pass
// forever. reconcile.go says why that placement is the safe one and
// barrier_test.go measures what both placements cost.
//
// AND NOTHING IN THIS PROCESS STOPPED IT EITHER. Cancel and Retry answer on rpc
// goroutines while the claim loop runs on its own, and raft ORDERS their two
// entries without noticing that the second was decided on a read taken before
// the first — and the store has no compare-and-swap for them to arbitrate with.
// So they take a lock, which is the same three-part argument pkg/v2/queue makes
// for its own claim, over the same store, for the same reason.
//
// WHAT IT DOES NOT COVER, said out loud so it is not mistaken for more. It is
// one process's third of an exclusion whose other two thirds are leadership and
// the barrier, so it is the mechanism only while all three hold:
//
//   - every write of a record goes through Store.Batch, which refuses off the
//     leader. A writer that went around it — a cache, a kvdb, a direct Set —
//     would be outside this entirely, which is why Store names no Set.
//   - nothing claims, settles or reconciles off the leader. step() refuses on a
//     follower and so does every queue write under it. A backend that ran work
//     somewhere the leader is not would need the STORE to arbitrate.
//   - the store keeps one writer. Two nodes that both believe they lead cannot
//     both commit, because the stale one's entry never reaches quorum, so this
//     lock is never asked to arbitrate across processes. A store without that
//     property needs a CONDITIONAL WRITE instead, and raft.BatchOp has exactly
//     two operations, Set and Delete — there is nothing to condition on.
//   - the queue's own claim and sweep are outside it. Both are pkg/v2/queue's
//     transitions rather than this table's: they are decided under the queue's
//     group lock, on no job record. The record write a claim pairs with is
//     begin's, and that is a section.
//
// It is the package's rather than a field on Service because what it protects
// is a job table, not a Service: nothing stops two Services from being
// constructed over one store, and runner_test.go's follower is exactly that.
// Two locks there would be two unsynchronised writers of one table.
//
// LOCK ORDER IS jobLock, THEN pkg/v2/queue's GROUP LOCK, never the reverse. A
// transition holds this across the queue write that pairs with its record write
// — a push, a withdrawal, an ack — and pkg/v2/queue calls nothing in this
// package, so the order cannot invert.
//
// THAT ORDER IS ALSO WHY THE OUTCOME OF A LOST PAIRING IS HARD TO REPRODUCE.
// pkg/v2/queue takes its group lock before it touches the store, so an ack
// already holds it by the time anything a test can hook has run, and a racing
// push queues up behind rather than landing in front. The harm needs the racer
// to win that lock; the fact does not.
//
// WHICH IS WHY THE SECTION HAS AN IDENTITY, and why the identity is a value
// this package cannot make for itself. backend/section hands one out only to a
// caller that has ENTERED — there is no accessor that answers "which section is
// running" with something spendable — a read of a job record carries the
// holding it was taken under, and every write below takes that reading. An ack
// made on a decision taken in a section that has closed is then REFUSED, not
// made — so a defect that could not be raced into a visible outcome has one
// anyway: an error, and a delivery that stays claimed and comes back. That is
// also the only thing in this package that can see the difference between "a
// lock is held" and "the lock that decided this is held", which is the
// difference every static guard over this file's source has failed on.
//
// THE PAIRING IS THE RULE AND NOT AN ILLUSTRATION, and the sentence above was
// once true of every transition except the one that ran the builds. run acked
// its deliveries with no lock held, at both of its exits, and the decision each
// ack carried out had been taken in a section that had already closed. That is
// not a lost update between two record writes; it is a queue write and a record
// write drifting apart. queue.Push is a no-op for an id the queue already
// holds, and queue.Ack deletes the very index row that dedup reads — so a
// retry that landed while a build was running made a push that did nothing, and
// the ack after the build destroyed the item it had been deduplicated against.
// The caller had been told its retry was accepted; the job had no queue item at
// all, and once settle had dropped its open row, nothing for reconcile to walk
// either. Both acks are now inside the section whose read decided them
// (runner.go) and both CARRY that read, and population_test.go checks it from
// the WRITE side too, because a table keyed by the reading function cannot see
// a write made by a function that reads nothing.
//
// WHAT IS DELIBERATELY OUTSIDE, so that the rule is not read as "everything":
// the build, and the round trip to auth. A section that held either would hold
// this lock for an hour, or across a network call to another service — and the
// whole point of it is that it is only ever held across writes to one store.
var jobLock section.Lock

// create writes a job that does not exist yet: the record, its project index
// row and its open index row, in ONE entry — and then the item it is to be
// claimed by.
//
// THE READ IS TAKEN HERE, not by the caller, and behind a barrier — the caller
// has just been on a round trip to auth, and a node that has become leader
// during it holds committed entries it has not applied. What comes back is
// therefore the table as it is at the moment of the write, and a delivery that
// finds the job already there requeues THAT record rather than overwriting it
// with a fresh one.
func (s *Service) create(id, repositoryKey, ref, commitSHA, projectID string) error {
	sec := jobLock.Enter()
	defer sec.Leave()

	if err := s.lead(); err != nil {
		return err
	}
	existing, err := s.record(sec, id)
	if err != nil {
		return err
	}
	if existing.Found() {
		return s.requeue(existing)
	}

	now := s.now()
	rec := jobRecord{
		ID:            id,
		Status:        int32(buildv1.JobStatus_JobStatusOpen),
		RepositoryKey: repositoryKey,
		Ref:           ref,
		CommitSHA:     commitSHA,
		ProjectID:     projectID,
		CreatedAt:     now.Unix(),
		UpdatedAt:     now.Unix(),
	}
	value, err := rec.encode()
	if err != nil {
		return errInternal("encoding job %s: %v", id, err)
	}

	// ONE Batch: the record, the project index row and the open index row are
	// one replicated entry, so a reader never sees a job that is in one of the
	// three and not the others.
	if err := s.commit(existing, []raft.BatchOp{
		set(jobRecordKey(id), value),
		set(projectIndexKey(projectID, id), nil),
		set(openIndexKey(rec.RepositoryKey, id), nil),
	}); err != nil {
		return refused(err, "writing job %s", id)
	}

	// AND THE ITEM, ON THE SAME READ. It is a push rather than a requeue
	// because there is nothing to consult: the read found nothing, this entry
	// wrote the record Open, and the only thing left is the item it is to be
	// claimed by. requeue asks the record what state it is in, and the answer
	// would be one this function just wrote — a reading carried onto a record
	// nothing read, which is an identity nobody took.
	if err := s.push(existing, id); err != nil {
		return refused(err, "queue push")
	}
	return nil
}

// requeue gives an open job an item to be claimed by, and is a no-op for one
// that is claimed or finished — a job somebody is running does not want a
// second item, and a finished one does not want to be run again.
//
// RECORD FIRST, PUSH SECOND, and the two cannot be fused because queue.Push
// does its own Batch. A crash between them leaves a record with no item, which
// reconcile repairs and which a redelivered webhook already answers correctly.
// The other order leaves an item with no record — an unidentifiable job nobody
// can run — which is exactly v1's orphan.
//
// IT IS INSIDE jobLock, called only by resume and settle, because the push is a
// decision made on the record that was just read: released first, a cancel
// could finish the job and withdraw its item between the read and this, and the
// item would go straight back for a build nobody wants. Push takes the queue's
// group lock under jobLock, which is the one direction that order runs in.
func (s *Service) requeue(r reading) error {
	rec := r.Value()
	if rec.terminal() || rec.status() == buildv1.JobStatus_JobStatusClaimed {
		return nil
	}
	if err := s.push(r, rec.ID); err != nil {
		return refused(err, "queue push")
	}
	return nil
}

// admit is the bound on stranger-authored input. Both limits are per this
// consistency domain, both are cheap listings of keys this service already
// keeps, and both answer CodeResourceExhausted — which a frontend can turn
// into a 503 and a retry rather than into a failed build.
func (s *Service) admit(repositoryKey string) error {
	ready, claimed := s.work.Depth()
	if ready+claimed >= s.cfg.MaxQueueDepth {
		return errResourceExhausted("build queue is full (%d)", s.cfg.MaxQueueDepth)
	}
	if open := len(s.store.Keys(openIndexPrefix(repositoryKey))); open >= s.cfg.MaxOpenPerRepo {
		return errResourceExhausted("repository %q already has %d jobs outstanding", repositoryKey, open)
	}
	return nil
}

// projectError keeps auth's own code where it is meaningful and turns
// everything else into "ask somebody else". A repository this cloud does not
// know is a push to refuse; an auth that cannot be reached is a push to retry,
// and they are the same string.
func (s *Service) projectError(repositoryKey string, err error) error {
	switch connect.CodeOf(err) {
	case connect.CodeNotFound:
		return errFailedPrecondition("repository %q is not registered with this cloud", repositoryKey)
	case connect.CodeInvalidArgument, connect.CodeFailedPrecondition, connect.CodePermissionDenied:
		return err
	default:
		return errUnavailable("resolving the project for %q: %v", repositoryKey, err)
	}
}

// record reads one job record UNDER A NAMED SECTION. It is the only reader of
// jobRecordKey and the only caller of section.Read, so the readings this
// package holds ARE its reads of a job record — and the section each is stamped
// with is what every write decided on it has to match (section.go).
//
// THE READ IS INSIDE THE STAMP rather than beside it: section.Read takes the
// closure, calls it, and stamps what comes back, so there is no instant at
// which this package holds a record it could stamp with some other section.
func (s *Service) record(sec section.Section, id string) (reading, error) {
	return section.Read(sec, func() (jobRecord, bool, error) {
		raw, exists := s.store.Get(jobRecordKey(id))
		if !exists {
			return jobRecord{}, false, nil
		}
		rec, err := decodeJobRecord(jobRecordKey(id), raw)
		if err != nil {
			return jobRecord{}, false, errInternal("%v", err)
		}
		return rec, true, nil
	})
}

func set(key string, value []byte) raft.BatchOp {
	return raft.BatchOp{Set: &raft.SetCommand{Key: key, Value: value}}
}

func del(key string) raft.BatchOp {
	return raft.BatchOp{Delete: &raft.DeleteCommand{Key: key}}
}
