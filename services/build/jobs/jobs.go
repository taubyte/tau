package jobs

import (
	"context"
	"errors"
	"io"
	"slices"
	"time"

	"connectrpc.com/connect"

	"github.com/libp2p/go-libp2p/core/peer"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// jobHandler is the connect adapter for JobService: each method authorizes, leads
// and delegates to a plain method below.
type jobHandler struct{ s *server }

func (h *jobHandler) Get(ctx context.Context, req *connect.Request[buildv2.JobRef]) (*connect.Response[buildv2.Job], error) {
	if err := h.s.allow(ctx, ActRead, req.Msg.GetId()); err != nil {
		return nil, err
	}
	job, err := h.s.job(req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(job), nil
}

func (h *jobHandler) List(ctx context.Context, req *connect.Request[buildv2.ProjectRef], stream *connect.ServerStream[buildv2.JobRef]) error {
	if err := h.s.allow(ctx, ActRead, req.Msg.GetId()); err != nil {
		return err
	}
	return h.s.list(req.Msg.GetId(), stream.Send)
}

func (h *jobHandler) Cancel(ctx context.Context, req *connect.Request[buildv2.JobRef]) (*connect.Response[buildv2.Empty], error) {
	if err := h.s.allow(ctx, ActCancel, req.Msg.GetId()); err != nil {
		return nil, err
	}
	if err := h.s.cancel(req.Msg.GetId()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&buildv2.Empty{}), nil
}

func (h *jobHandler) Retry(ctx context.Context, req *connect.Request[buildv2.JobRef]) (*connect.Response[buildv2.Empty], error) {
	if err := h.s.allow(ctx, ActRetry, req.Msg.GetId()); err != nil {
		return nil, err
	}
	if err := h.s.retry(req.Msg.GetId()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&buildv2.Empty{}), nil
}

// job assembles one Job in one read; the record carries its logs' and assets'
// cids. The one record read deciding no write, so it takes no section. The maps
// are copied out so the caller's message shares no map with the decoded record.
func (s *server) job(id string) (*buildv2.Job, error) {
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
	return jobOf(rec), nil
}

// jobOf is a job as the wire carries it.
func jobOf(rec jobRecord) *buildv2.Job {
	return &buildv2.Job{
		Id:            rec.ID,
		Status:        rec.status(),
		RepositoryKey: rec.RepositoryKey,
		Ref:           rec.Ref,
		CommitSha:     rec.CommitSHA,
		Timestamp:     rec.CreatedAt,
		Attempt:       int32(rec.Attempt),
		Logs:          merged(rec.Logs, nil),
		AssetCid:      merged(rec.Assets, nil),
		Environment:   rec.Environment,
		Children:      slices.Clone(rec.Children),
		Reason:        rec.Reason,
	}
}

// list streams the ids of a project's jobs, unbounded and unpaginated, handing
// each to emit as it is found. emit is a closure because connect.ServerStream has
// no exported constructor.
func (s *server) list(projectID string, emit func(*buildv2.JobRef) error) error {
	if projectID == "" {
		return errInvalidArgument("project id is required")
	}
	if err := s.lead(); err != nil {
		return err
	}

	ctx, stop := context.WithTimeout(context.Background(), s.cfg.Queue.ApplyTimeout)
	defer stop()
	keys, err := s.records.List(ctx, projectRows(projectID))
	if err != nil {
		return errUnavailable("listing project %s's jobs: %v", projectID, err)
	}
	slices.Sort(keys)
	for _, key := range keys {
		id, err := lastSegmentOf(key)
		if err != nil {
			return errInternal("%v", err)
		}
		if err := emit(&buildv2.JobRef{Id: id}); err != nil {
			return err
		}
	}
	return nil
}

// cancel writes the terminal record, withdraws the queue item and, if this node
// runs the job, stops it, all in one section. The record is what cancels: since
// the claimer's re-read and its claiming entry are a section too (runner.go's
// begin), a claimer either reads Cancelled and drops the delivery or has
// registered its run for the stop. A run on another node settles against a
// terminal record, writing its log and never a status.
func (s *server) cancel(id string) error {
	if err := requireJobID(id); err != nil {
		return err
	}

	return s.withJob(id, func(j *held) error {
		if !j.found() {
			return errNotFound("no job %s", id)
		}
		rec := j.rec()
		if rec.status() == buildv2.JobStatus_JobStatusCancelled {
			// Cancelling a cancelled job succeeds, and finishes off a withdrawal whose entry
			// did not land.
			return j.withdraw()
		}
		if rec.terminal() {
			return errFailedPrecondition("job %s already finished as %s", id, rec.status())
		}

		// Record first, withdrawal second (the queue does its own Batch). A crash between
		// leaves a cancelled job with an item, which the next claim drops; the other
		// order would leave an open job reconcile pushes back.
		if err := j.finish(buildv2.JobStatus_JobStatusCancelled, "cancelled"); err != nil {
			return err
		}
		if err := j.withdraw(); err != nil {
			return err
		}
		return j.stopRun()
	})
}

// retry re-queues a finished job; a non-terminal one is a FailedPrecondition. The
// record's attempt counter keeps rising, so earlier runs' cids stay filed under
// their attempts.
func (s *server) retry(id string) error {
	if err := requireJobID(id); err != nil {
		return err
	}

	// The reopening, push included, is one section with its deciding read, so this
	// node's own transitions on the job are ordered around it.
	return s.withJob(id, func(j *held) error {
		if !j.found() {
			return errNotFound("no job %s", id)
		}
		rec := j.rec()
		if !rec.terminal() {
			return errFailedPrecondition("job %s is %s, not finished", id, rec.status())
		}

		// dropRetained reads the retention timestamp off the record this section read,
		// not the one being written.
		reopened := j.writes().retried().open()
		if err := reopened.dropRetained().commit("reopening"); err != nil {
			return err
		}

		if err := j.push(); err != nil {
			return refused(err, "queue push")
		}
		return nil
	})
}

func (h *jobHandler) Artifact(ctx context.Context, req *connect.Request[buildv2.ArtifactRequest]) (*connect.Response[buildv2.Built], error) {
	cid, err := h.s.artifact(ctx, req.Msg.GetProject(), req.Msg.GetEnvironment(), req.Msg.GetResource())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&buildv2.Built{Cid: cid}), nil
}

// artifact reads what one resource of one deployment runs, authorized on the
// project (ActArtifact), since a worker asking has no job in hand.
func (s *server) artifact(ctx context.Context, project, environment, resource string) (string, error) {
	switch {
	case project == "":
		return "", errInvalidArgument("build: project is required")
	case environment == "":
		return "", errInvalidArgument("build: environment is required")
	case resource == "":
		return "", errInvalidArgument("build: resource is required")
	}

	if err := s.allow(ctx, ActArtifact, project); err != nil {
		return "", err
	}
	return s.Built(project, environment, resource)
}

// Built is the Artifact read for this node itself, in-process: a host does not
// dial itself. Only the leader answers.
func (s *server) Built(project, environment, resource string) (string, error) {
	if err := s.lead(); err != nil {
		return "", err
	}

	ctx, stop := context.WithTimeout(context.Background(), s.cfg.Queue.ApplyTimeout)
	defer stop()
	return Live(ctx, s.records, project, environment, resource)
}

func (h *jobHandler) Follow(ctx context.Context, req *connect.Request[buildv2.FollowRequest], stream *connect.ServerStream[buildv2.Job_Event]) error {
	if err := h.s.allow(ctx, ActRead, req.Msg.GetJobId()); err != nil {
		return err
	}
	return h.s.follow(ctx, req.Msg, stream.Send)
}

// follow streams one run's events from the request's offset, as its runner's log
// records them: its claim, each line of its output, and the status it left the job
// in, which is the last. Any build node answers: the job record names the runner
// each run went to, and the follow is proxied there. A run to come says the job is
// queued and waits, read again each RecordReadInterval, for the record to name its
// runner or for the job to end without it. A snapshot does not wait.
func (s *server) follow(ctx context.Context, req *buildv2.FollowRequest, send func(*buildv2.Job_Event) error) error {
	jobID, offset := req.GetJobId(), req.GetOffset()
	if err := requireJobID(jobID); err != nil {
		return err
	}
	if offset < 0 {
		return errInvalidArgument("build: offset %d is before the start of the log", offset)
	}
	if req.GetAttempt() < 1 {
		return errInvalidArgument("build: attempt %d is not a run: an offset is a position in one run's log", req.GetAttempt())
	}
	attempt := uint32(req.GetAttempt())

	var queued bool
	for {
		rec, found, err := s.readRecord(jobID)
		switch {
		case err != nil:
			return errUnavailable("build: reading job %s: %v", jobID, err)
		case !found:
			return errNotFound("build: no job %s", jobID)
		case attempt > rec.Attempt+1:
			return errInvalidArgument("build: job %s has run %d times: attempt %d is not its next", jobID, rec.Attempt, attempt)
		case attempt < rec.Attempt, attempt == rec.Attempt+1 && rec.terminal():
			return ended(jobID, attempt, rec)
		}
		if attempt == rec.Attempt {
			if rec.Runner != "" {
				err := s.proxy(ctx, rec.Runner, req, send)
				if connect.CodeOf(err) != connect.CodeNotFound {
					return err
				}
			}
			// THE RUNNER HOLDS NO SUCH LOG: it let go of an ended run, or has not opened
			// this one yet. Only the second is waited for.
			if rec.status() != buildv2.JobStatus_JobStatusClaimed {
				return ended(jobID, attempt, rec)
			}
		}
		if !queued {
			queued = true
			ev := &buildv2.Job_Event{At: s.now().UnixNano(), Kind: &buildv2.Job_Event_Status{Status: buildv2.JobStatus_JobStatusOpen}, Offset: offset}
			if err := send(ev); err != nil {
				return err
			}
		}
		if req.GetSnapshot() {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(s.cfg.RecordReadInterval):
		}
	}
}

// ended is a follow of a run that is over and no longer on its runner: what
// survives is the cid the record files under that attempt.
func ended(jobID string, attempt uint32, rec jobRecord) error {
	return errFailedPrecondition("build: attempt %d of job %s has ended, and the job is %s at attempt %d: its log is the one its record files under that attempt",
		attempt, jobID, rec.status(), rec.Attempt)
}

// proxy is a follow read from the runner holding the run, event by event.
func (s *server) proxy(ctx context.Context, runner string, req *buildv2.FollowRequest, send func(*buildv2.Job_Event) error) error {
	id, err := peer.Decode(runner)
	if err != nil {
		return errInternal("build: job %s names runner %q: %v", req.GetJobId(), runner, err)
	}
	if s.runners == nil {
		return errUnavailable("build: this node reaches no runner to follow job %s on", req.GetJobId())
	}
	r, err := s.runners.At(id)
	if err != nil {
		return errUnavailable("build: reaching runner %s: %v", runner, err)
	}
	events, err := r.Follow(ctx, req)
	if err != nil {
		if code := connect.CodeOf(err); code == connect.CodeNotFound || code == connect.CodeInvalidArgument {
			return err
		}
		return errUnavailable("build: following job %s on runner %s: %v", req.GetJobId(), runner, err)
	}
	defer events.Close()
	for {
		ev, err := events.Receive()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return errUnavailable("build: following job %s: %v", req.GetJobId(), err)
		}
		if err := send(ev); err != nil {
			return err
		}
	}
}
