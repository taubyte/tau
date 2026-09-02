package backend

import (
	"context"
	"errors"
	"slices"
	"time"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/raft"
	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/pkg/v2/queue"
)

// jobHandler is the connect adapter for JobService. As in queue.go, each method
// is authorize, lead, delegate — the bodies are plain methods below, which is
// where this package's coverage comes from.
type jobHandler struct{ s *Service }

func (h *jobHandler) Get(ctx context.Context, req *connect.Request[buildv1.JobRef]) (*connect.Response[buildv1.Job], error) {
	if err := h.s.allow(ctx, ActRead, req.Msg.GetId()); err != nil {
		return nil, err
	}
	job, err := h.s.job(req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(job), nil
}

func (h *jobHandler) List(ctx context.Context, req *connect.Request[buildv1.ProjectRef], stream *connect.ServerStream[buildv1.JobRef]) error {
	if err := h.s.allow(ctx, ActRead, req.Msg.GetId()); err != nil {
		return err
	}
	return h.s.list(req.Msg.GetId(), stream.Send)
}

func (h *jobHandler) Cancel(ctx context.Context, req *connect.Request[buildv1.JobRef]) (*connect.Response[buildv1.Empty], error) {
	if err := h.s.allow(ctx, ActCancel, req.Msg.GetId()); err != nil {
		return nil, err
	}
	if err := h.s.cancel(req.Msg.GetId()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&buildv1.Empty{}), nil
}

func (h *jobHandler) Retry(ctx context.Context, req *connect.Request[buildv1.JobRef]) (*connect.Response[buildv1.Empty], error) {
	if err := h.s.allow(ctx, ActRetry, req.Msg.GetId()); err != nil {
		return nil, err
	}
	if err := h.s.retry(req.Msg.GetId()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&buildv1.Empty{}), nil
}

// job assembles one Job: the record, plus every log and asset row across every
// attempt.
//
// IT IS THE ONE READ OF A RECORD THAT DECIDES NO WRITE, so it is the one that
// takes no jobLock: there is nothing for a concurrent transition to make stale,
// and serializing every Get behind every build's bookkeeping would buy a
// consistency this shape does not promise anyway — the rows are listed after
// the record is read, and one that vanished between the two is skipped below.
//
// THE MAPS ARE A READ SHAPE. They are never a stored blob — one entry, one key
// (root AGENTS.md rule 1) — because a map re-serialized on each write is a
// read-modify-write, and two writers of it lose entries. Rows are read in
// attempt order, so a later attempt's asset for a resource overwrites an
// earlier one, which is what a reader of a retried job wants.
func (s *Service) job(id string) (*buildv1.Job, error) {
	if err := requireJobID(id); err != nil {
		return nil, err
	}
	if err := s.lead(); err != nil {
		return nil, err
	}

	r, err := s.record(unsectioned, id)
	if err != nil {
		return nil, err
	}
	if !r.found {
		return nil, errNotFound("no job %s", id)
	}
	rec := r.rec

	job := &buildv1.Job{
		Id:            rec.ID,
		Status:        rec.status(),
		RepositoryKey: rec.RepositoryKey,
		Ref:           rec.Ref,
		CommitSha:     rec.CommitSHA,
		Timestamp:     rec.CreatedAt,
		Attempt:       int32(rec.Attempt),
		Logs:          map[string]string{},
		AssetCid:      map[string]string{},
	}

	keys := s.store.Keys(attemptPrefix(id))
	slices.Sort(keys)
	for _, key := range keys {
		row, err := parseAttemptKey(id, key)
		if err != nil {
			return nil, errInternal("%v", err)
		}
		value, exists := s.store.Get(key)
		if !exists {
			// Listed and then gone is a store that changed under the listing,
			// not a fault: skip it rather than fail a read.
			continue
		}
		if row.kind == logSegment {
			job.Logs[row.key] = string(value)
		} else {
			job.AssetCid[row.key] = string(value)
		}
	}
	return job, nil
}

// list streams the ids of a project's jobs.
//
// emit is a closure rather than a *connect.ServerStream because
// connect.ServerStream has no exported constructor, so a test that took one
// could not build it — the streaming LOGIC is testable this way and rpc_test.go
// proves the wire once.
//
// It is unbounded and unpaginated, as v1's was. The difference from v1 is that
// v1 materialized the whole result in memory before answering; this hands each
// id to the transport as it finds it.
func (s *Service) list(projectID string, emit func(*buildv1.JobRef) error) error {
	if projectID == "" {
		return errInvalidArgument("project id is required")
	}
	if err := s.lead(); err != nil {
		return err
	}

	keys := s.store.Keys(projectIndexPrefix(projectID))
	slices.Sort(keys)
	for _, key := range keys {
		id, err := idFromIndexKey(key)
		if err != nil {
			return errInternal("%v", err)
		}
		if err := emit(&buildv1.JobRef{Id: id}); err != nil {
			return err
		}
	}
	return nil
}

// cancel writes the terminal record, withdraws the job's queue item and, if
// this node is the one running the job, stops it. All four — the read, the
// record, the withdrawal and the stop — are ONE SECTION under jobLock, and that
// is what makes the paragraph below true rather than nearly true.
//
// THE RECORD IS STILL WHAT CANCELS THE JOB. The withdrawal is bookkeeping on
// top of it, never the mechanism, and a delivery that outran it is dropped —
// but only because the claimer's re-read and the entry that claims on it are
// one section with THIS one (runner.go's begin). Ordered the other way round
// the claimer had already re-read, and its entry put the record back to Claimed
// over the Cancelled this one had just written: a build that ran to completion,
// reported Success, and advanced the ref anchor to the commit the caller
// cancelled. Serialized, the claimer either reads the Cancelled record and
// drops the delivery without starting, or has already registered its run for
// the stop below.
//
// A job running on ANOTHER backend node needs no forwarding either — its run
// settles against a record that is already terminal and publishes nothing, and
// it is bounded by the claim deadline regardless.
//
// v1 needed three hops for this: the HTTP handler read which monkey was
// assigned, called that monkey's own p2p service, and monkey then called
// patrick back with the accumulated logs so patrick could write the status. It
// also polled for a claim record thirty times at two seconds, blocking the
// request for up to a minute, and then still could not cancel a job that had
// not been claimed.
func (s *Service) cancel(id string) error {
	if err := requireJobID(id); err != nil {
		return err
	}

	at := jobLock.Lock()
	defer jobLock.Unlock()

	if err := s.lead(); err != nil {
		return err
	}

	r, err := s.record(at, id)
	if err != nil {
		return err
	}
	if !r.found {
		return errNotFound("no job %s", id)
	}
	if r.rec.status() == buildv1.JobStatus_JobStatusCancelled {
		// Cancelling a cancelled job is the outcome the caller asked for — and
		// is where a withdrawal whose entry did not land is finished off, the
		// same way Enqueue's push repairs a record whose push was lost.
		return s.withdraw(r)
	}
	if r.rec.terminal() {
		return errFailedPrecondition("job %s already finished as %s", id, r.rec.status())
	}

	// THE RECORD FIRST, THE WITHDRAWAL SECOND, and the two cannot be one entry
	// because the queue does its own Batch. A crash between them leaves a
	// cancelled job with an item, which the next claim drops on reading the
	// record; the other order leaves an open job with no item, which reconcile
	// would push back into the queue — a build somebody asked to stop.
	if err := s.finish(r, buildv1.JobStatus_JobStatusCancelled, "cancelled"); err != nil {
		return err
	}
	if err := s.withdraw(r); err != nil {
		return err
	}

	s.stopRun(id)
	return nil
}

// withdraw takes a cancelled job's item out of the queue.
//
// LEAVING IT WAS NOT FREE. finish() drops the open index row, so the repository
// is told it has nothing outstanding while a delivery for it is still queued —
// and that item counts against MaxQueueDepth, and costs the builder a whole
// claim cycle before it reaches a job somebody still wants. On a leader that
// runs no builds — a voter rather than a builder, which WithRunner says is a
// deployment's choice — nothing ever claims, so nothing ever drops it.
//
// The two refusals are the cases where there is nothing to withdraw. An item
// the queue does not hold is one already acked, or one whose push never landed.
// An item a CONSUMER holds is refused by the queue on purpose: that claim is
// the mutual exclusion, and taking it away would let a retry of this same job
// be claimed beside the run still going.
//
// SWALLOWING ErrClaimed IS ONLY HONEST IF THAT ITEM ALWAYS LEAVES, and it does,
// by one of three exits — none of which needs anybody to come back to it:
//
//   - the holder acks. On this node the stopRun a line below is what makes that
//     prompt, and it reaches the run because begin registered it inside the
//     same section this one is running in. On another node the run finishes,
//     acks, and settles against a record that is already terminal.
//   - the holder dies, the claim lapses at its deadline, and the redelivery is
//     dropped on reading the record: begin refuses a job it finds terminal and
//     run acks it without starting it.
//   - the redeliveries run out and the item reaches the dead letter, where the
//     drain finds a terminal record, writes nothing, and acks.
//
// Before the section existed the first exit was the claim: stopRun could be
// called while the run had been decided on and not yet registered, so it was a
// no-op and the build ran to its own completion. It is the registration inside
// begin, not this call, that closed that.
func (s *Service) withdraw(r reading) error {
	// THE CHECK IS SPELLED OUT HERE rather than behind a helper like push and
	// ack, because the three outcomes below must not swallow it: "the section
	// that decided this had already been left" is not an item the queue does
	// not hold, and answering it Unavailable would send the caller round the
	// cloud looking for a node where the code is different.
	if err := r.live(); err != nil {
		return err
	}
	err := s.work.Remove(r.rec.ID)
	if err == nil || errors.Is(err, queue.ErrNotFound) || errors.Is(err, queue.ErrClaimed) {
		return nil
	}
	return errUnavailable("withdrawing job %s from the queue: %v", r.rec.ID, err)
}

// retry re-queues a finished job.
//
// A NON-TERMINAL JOB IS A FailedPrecondition. v1's retry returned success
// without doing anything at all when the job was still in /jobs/ — it looked up
// the id, found it, and answered {"retry": jid} — so "retry" on a running job
// was a lie the caller could not detect.
//
// The accumulated attempt rows are KEPT: the record's attempt counter keeps
// going up, so the new run writes under a higher attempt/ segment and the
// previous run's logs stay readable. Push after an Ack is a genuinely new queue
// item whose delivery attempts start from one, which is exactly why the
// attempt/ segment is driven by the record's counter and not by the queue's.
func (s *Service) retry(id string) error {
	if err := requireJobID(id); err != nil {
		return err
	}

	// The reopening is ONE SECTION with the read that decided it, down to and
	// including the push: reconcile deletes the open-index row of a job whose
	// record it read as finished, and released early it would delete the very
	// row written a line below — leaving a job that reads Open and that nothing
	// walking the open index will ever look at again.
	at := jobLock.Lock()
	defer jobLock.Unlock()

	if err := s.lead(); err != nil {
		return err
	}

	r, err := s.record(at, id)
	if err != nil {
		return err
	}
	if !r.found {
		return errNotFound("no job %s", id)
	}
	if !r.rec.terminal() {
		return errFailedPrecondition("job %s is %s, not finished", id, r.rec.status())
	}
	rec := r.rec

	// The retention row was written at the moment the job FINISHED, which is
	// the timestamp the record still carries — so it has to be read before the
	// record is reopened. Reading it afterwards deletes a key nothing wrote and
	// leaves the real row behind, which is a job that reads Open and is also
	// listed as retained.
	finishedAt := time.Unix(rec.UpdatedAt, 0)

	rec.Status = int32(buildv1.JobStatus_JobStatusOpen)
	rec.UpdatedAt = s.now().Unix()
	rec.ClaimedUntil = 0
	rec.Holder = ""
	rec.Reason = ""
	value, err := rec.encode()
	if err != nil {
		return errInternal("encoding job %s: %v", id, err)
	}

	if err := s.commit(r, []raft.BatchOp{
		set(jobRecordKey(id), value),
		set(openIndexKey(rec.RepositoryKey, id), nil),
		del(terminalIndexKey(finishedAt, id)),
	}); err != nil {
		return refused(err, "reopening job %s", id)
	}

	if err := s.push(r, id); err != nil {
		return refused(err, "queue push")
	}
	return nil
}

// finish writes a terminal record in one Batch: the record, the open index row
// removed, the retention index row written, plus whatever else belongs in the
// same entry — on success that is the ref anchor, so "this job succeeded" and
// "this is now the commit to diff from" cannot end up disagreeing. A job is
// never in a state where it is finished but still listed as open, or listed as
// open but has no record.
func (s *Service) finish(r reading, status buildv1.JobStatus, reason string, extra ...raft.BatchOp) error {
	now := s.now()
	rec := r.rec
	rec.Status = int32(status)
	rec.UpdatedAt = now.Unix()
	rec.ClaimedUntil = 0
	rec.Holder = ""
	rec.Reason = reason

	value, err := rec.encode()
	if err != nil {
		return errInternal("encoding job %s: %v", rec.ID, err)
	}

	ops := append([]raft.BatchOp{
		set(jobRecordKey(rec.ID), value),
		del(openIndexKey(rec.RepositoryKey, rec.ID)),
		set(terminalIndexKey(now, rec.ID), nil),
	}, extra...)
	if err := s.commit(r, ops); err != nil {
		return refused(err, "finishing job %s", rec.ID)
	}
	return nil
}

// stopRun cancels a run this node is holding. It is a no-op for a job running
// somewhere else or not running at all, which is the common case and not an
// error: the record write is what cancels those.
//
// IT IS CALLED INSIDE cancel's SECTION, and that is what makes it reach a run
// this node just claimed rather than being a no-op against a window. begin
// registers the run inside the claim's section, so the two orders are the only
// two: this one first, and the claim's re-read finds a cancelled record and
// never starts; the claim first, and the registration is already here.
func (s *Service) stopRun(id string) {
	s.runningMu.Lock()
	cancel, ok := s.running[id]
	s.runningMu.Unlock()
	if ok {
		cancel()
	}
}
