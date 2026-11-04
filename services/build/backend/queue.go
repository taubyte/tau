package backend

import (
	"context"
	"errors"

	"github.com/taubyte/tau/services/build/run"

	"connectrpc.com/connect"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// queueHandler is the connect adapter for QueueService: authorize, lead,
// delegate. The split matters because no test outside p2p/rpc can fabricate a
// caller identity, so the logic lives in plain methods.
type queueHandler struct{ s *Service }

func (h *queueHandler) Enqueue(ctx context.Context, req *connect.Request[buildv2.PushEvent]) (*connect.Response[buildv2.JobRef], error) {
	if err := h.s.allow(ctx, ActEnqueue, req.Msg.GetRepositoryKey()); err != nil {
		return nil, err
	}
	id, err := h.s.enqueue(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&buildv2.JobRef{Id: id}), nil
}

// enqueue durably records a push and returns its job id. Idempotent: the job id is
// a function of (repository, ref, commit), so queue dedup and this are one
// mechanism. A redelivered webhook can arrive while the first is in flight, so the
// decision and its write are one section (create). Every argument is
// stranger-authored.
func (s *Service) enqueue(ctx context.Context, p *buildv2.PushEvent) (string, error) {
	key, ok := parseRepositoryKey(p.GetRepositoryKey())
	if !ok {
		return "", errInvalidArgument("repository key %q is not canonical", p.GetRepositoryKey())
	}
	// A shape check, not a second policy filter.
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

	// An unregistered repository is refused before anything is queued.
	projectID, err := s.repos.Project(ctx, key.String())
	if err != nil {
		return "", s.projectError(key.String(), err)
	}
	if projectID == "" {
		return "", errFailedPrecondition("repository %q belongs to no project", key)
	}

	if s.confirm != nil {
		err := s.confirm.Confirm(ctx, key.String(), p.GetRef(), p.GetCommitSha())
		switch {
		case errors.Is(err, run.ErrNotAtCommit):
			return "", errFailedPrecondition("%s does not hold %s at %s", key, p.GetRef(), p.GetCommitSha())
		case err != nil:
			return "", errUnavailable("confirming %s %s: %v", key, p.GetRef(), err)
		}
	}

	return id, s.create(id, key.String(), p.GetRef(), p.GetCommitSha(), projectID)
}

// resume answers a push this service already has; the read and the push it decides
// are one section, so a cancel's withdrawal cannot be undone.
func (s *Service) resume(id string) (bool, error) {
	var found bool
	err := s.withJob(id, func(j *held) error {
		if !j.found() {
			return nil
		}
		found = true
		return j.requeue()
	})
	return found, err
}

// create writes a new job's created event, project row and open row in one delta,
// then its item. The read is taken here behind the queue's barrier, after the auth
// round trip, so a delivery finding the job already there requeues it.
func (s *Service) create(id, repositoryKey, ref, commitSHA, projectID string) error {
	return s.withJob(id, func(j *held) error {
		if j.found() {
			return j.requeue()
		}

		now := s.now()
		rec := jobRecord{
			ID:            id,
			Status:        int32(buildv2.JobStatus_JobStatusOpen),
			RepositoryKey: repositoryKey,
			Ref:           ref,
			CommitSHA:     commitSHA,
			ProjectID:     projectID,
			CreatedAt:     now.Unix(),
			UpdatedAt:     now.Unix(),
		}

		// One entry, so no reader sees a job in some of the three rows; every key comes
		// from the subject and this record.
		if err := j.writes().created(rec).inProject().open().commit("writing"); err != nil {
			return err
		}

		// A plain push, since the record was just written Open by this body.
		if err := j.push(); err != nil {
			return refused(err, "queue push")
		}
		return nil
	})
}

// createEnvironment enqueues the build of one environment of plan's push, or finds
// the one already there. It is a push of its own, admitted by the plan's.
func (s *Service) createEnvironment(plan jobRecord, environment, prelude string) (string, error) {
	key, ok := parseRepositoryKey(plan.RepositoryKey)
	if !ok {
		return "", errInternal("plan %s: repository key %q is not canonical", plan.ID, plan.RepositoryKey)
	}
	if environment == "" {
		return "", errInternal("plan %s: an environment with no name", plan.ID)
	}
	id := environmentJobID(key, plan.Ref, plan.CommitSHA, environment)
	return id, s.withJob(id, func(j *held) error {
		if j.found() {
			return j.requeue()
		}
		now := s.now()
		rec := jobRecord{
			ID:            id,
			Status:        int32(buildv2.JobStatus_JobStatusOpen),
			RepositoryKey: plan.RepositoryKey,
			Ref:           plan.Ref,
			CommitSHA:     plan.CommitSHA,
			ProjectID:     plan.ProjectID,
			Environment:   environment,
			Prelude:       prelude,
			CreatedAt:     now.Unix(),
			UpdatedAt:     now.Unix(),
		}
		if err := j.writes().created(rec).inProject().open().commit("writing"); err != nil {
			return err
		}
		if err := j.push(); err != nil {
			return refused(err, "queue push")
		}
		return nil
	})
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
	open, err := s.outstanding(repositoryKey)
	if err != nil {
		return err
	}
	if open >= s.cfg.MaxOpenPerRepo {
		return errResourceExhausted("repository %q already has %d jobs outstanding", repositoryKey, open)
	}
	return nil
}

// outstanding is how many of a repository's jobs have an open row: each job once,
// however many reopenings it has had.
func (s *Service) outstanding(repositoryKey string) (int, error) {
	ctx, stop := context.WithTimeout(context.Background(), s.cfg.Queue.ApplyTimeout)
	defer stop()
	keys, err := s.records.List(ctx, openRows(repositoryKey))
	if err != nil {
		return 0, errUnavailable("listing %q's outstanding jobs: %v", repositoryKey, err)
	}
	jobs := map[string]bool{}
	for _, key := range keys {
		if _, id, err := parseOpenRow(key); err == nil {
			jobs[id] = true
		}
	}
	return len(jobs), nil
}

// projectError keeps auth's code for an unknown repository and maps anything else
// to Unavailable.
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

// readRecord is one job, as the fold of its events on this node's replica. Its
// callers: enter() (before handing a body its handle) and job() (decides no
// write).
func (s *Service) readRecord(id string) (jobRecord, bool, error) {
	ctx, stop := context.WithTimeout(context.Background(), s.cfg.Queue.ApplyTimeout)
	defer stop()
	return s.journal.read(ctx, id)
}
