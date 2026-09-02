package backend

import (
	"context"
	"slices"
	"time"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/raft"
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

	rec, found, err := s.record(id)
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

// cancel writes the terminal record and, if this node is the one running the
// job, stops it. THAT IS THE WHOLE OF IT.
//
// A queued job needs no special case: the claimer re-reads the record before it
// runs and acks a cancelled one without starting. A job running on ANOTHER
// backend node needs no forwarding either — that node re-reads the record too,
// at its next claim, and its run is bounded by the claim deadline regardless.
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
	if err := s.lead(); err != nil {
		return err
	}

	rec, found, err := s.record(id)
	if err != nil {
		return err
	}
	if !found {
		return errNotFound("no job %s", id)
	}
	if rec.status() == buildv1.JobStatus_JobStatusCancelled {
		// Cancelling a cancelled job is the outcome the caller asked for.
		return nil
	}
	if rec.terminal() {
		return errFailedPrecondition("job %s already finished as %s", id, rec.status())
	}

	if err := s.finish(rec, buildv1.JobStatus_JobStatusCancelled, "cancelled"); err != nil {
		return err
	}

	s.stopRun(id)
	return nil
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
	if err := s.lead(); err != nil {
		return err
	}

	rec, found, err := s.record(id)
	if err != nil {
		return err
	}
	if !found {
		return errNotFound("no job %s", id)
	}
	if !rec.terminal() {
		return errFailedPrecondition("job %s is %s, not finished", id, rec.status())
	}

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

	if err := s.store.Batch([]raft.BatchOp{
		set(jobRecordKey(id), value),
		set(openIndexKey(rec.RepositoryKey, id), nil),
		del(terminalIndexKey(finishedAt, id)),
	}, s.cfg.Queue.ApplyTimeout); err != nil {
		return errUnavailable("reopening job %s: %v", id, err)
	}

	if err := s.work.Push(id, nil); err != nil {
		return errUnavailable("queue push: %v", err)
	}
	return nil
}

// finish writes a terminal record in one Batch: the record, the open index row
// removed, the retention index row written, plus whatever else belongs in the
// same entry — on success that is the ref anchor, so "this job succeeded" and
// "this is now the commit to diff from" cannot end up disagreeing. A job is
// never in a state where it is finished but still listed as open, or listed as
// open but has no record.
func (s *Service) finish(rec jobRecord, status buildv1.JobStatus, reason string, extra ...raft.BatchOp) error {
	now := s.now()
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
	if err := s.store.Batch(ops, s.cfg.Queue.ApplyTimeout); err != nil {
		return errUnavailable("finishing job %s: %v", rec.ID, err)
	}
	return nil
}

// stopRun cancels a run this node is holding. It is a no-op for a job running
// somewhere else or not running at all, which is the common case and not an
// error: the record write is what cancels those.
func (s *Service) stopRun(id string) {
	s.runningMu.Lock()
	cancel, ok := s.running[id]
	s.runningMu.Unlock()
	if ok {
		cancel()
	}
}
