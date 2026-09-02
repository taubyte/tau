package backend

import (
	"context"
	"slices"

	"connectrpc.com/connect"

	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
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
// takes no section: there is nothing for a concurrent transition to make stale,
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

	rec, found, err := s.readRecord(id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errNotFound("no job %s", id)
	}

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
// record, the withdrawal and the stop — are ONE SECTION, which is one closure
// over one handle, and that is what makes the paragraph below true rather than
// nearly true. The stop was the last of the four to become a write with a door:
// it reaches no store, so every enumeration walked past it, and carried out
// after its section it kills a build a later retry registered (section.go).
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

	return s.withJob(id, func(j *held) error {
		if !j.found() {
			return errNotFound("no job %s", id)
		}
		rec := j.rec()
		if rec.status() == buildv1.JobStatus_JobStatusCancelled {
			// Cancelling a cancelled job is the outcome the caller asked for —
			// and is where a withdrawal whose entry did not land is finished
			// off, the same way Enqueue's push repairs a record whose push was
			// lost.
			return j.withdraw()
		}
		if rec.terminal() {
			return errFailedPrecondition("job %s already finished as %s", id, rec.status())
		}

		// THE RECORD FIRST, THE WITHDRAWAL SECOND, and the two cannot be one
		// entry because the queue does its own Batch. A crash between them
		// leaves a cancelled job with an item, which the next claim drops on
		// reading the record; the other order leaves an open job with no item,
		// which reconcile would push back into the queue — a build somebody
		// asked to stop.
		if err := j.finish(buildv1.JobStatus_JobStatusCancelled, "cancelled"); err != nil {
			return err
		}
		if err := j.withdraw(); err != nil {
			return err
		}
		return j.stopRun()
	})
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
	return s.withJob(id, func(j *held) error {
		if !j.found() {
			return errNotFound("no job %s", id)
		}
		rec := j.rec()
		if !rec.terminal() {
			return errFailedPrecondition("job %s is %s, not finished", id, rec.status())
		}

		rec.Status = int32(buildv1.JobStatus_JobStatusOpen)
		rec.UpdatedAt = s.now().Unix()
		rec.ClaimedUntil = 0
		rec.Holder = ""
		rec.Reason = ""

		// THE RETENTION ROW WAS WRITTEN AT THE MOMENT THE JOB FINISHED, which
		// is the timestamp the record carried on the way IN — so dropRetained
		// reads it off the record this section READ rather than off the one
		// being written a line above. Spelled by hand it had to be saved before
		// the mutation, and read afterwards it deleted a key nothing wrote and
		// left the real row behind: a job that reads Open and is also listed as
		// retained.
		if err := j.writes().record(rec).open().dropRetained().commit("reopening"); err != nil {
			return err
		}

		if err := j.push(); err != nil {
			return refused(err, "queue push")
		}
		return nil
	})
}
