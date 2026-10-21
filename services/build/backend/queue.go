package backend

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
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
func (s *Service) enqueue(ctx context.Context, p *buildv2.PushEvent) (string, error) {
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

	if s.confirm != nil {
		err := s.confirm.Confirm(ctx, key.String(), p.GetRef(), p.GetCommitSha())
		switch {
		case errors.Is(err, ErrNotAtCommit):
			return "", errFailedPrecondition("%s does not hold %s at %s", key, p.GetRef(), p.GetCommitSha())
		case err != nil:
			return "", errUnavailable("confirming %s %s: %v", key, p.GetRef(), err)
		}
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

		// ONE Batch: the record, the project index row and the open index row
		// are one replicated entry, so a reader never sees a job that is in one
		// of the three and not the others. ALL THREE KEYS ARE THE ENTRY'S OWN:
		// the id half of each is the handle's subject, and the repository and
		// the project are read off the record this same entry writes.
		if err := j.writes().record(rec).inProject().open().commit("writing"); err != nil {
			return err
		}

		// AND THE ITEM, IN THE SAME SECTION. It is a push rather than a requeue
		// because there is nothing to consult: the read found nothing, this
		// entry wrote the record Open, and the only thing left is the item it
		// is to be claimed by. requeue asks the record what state it is in, and
		// the answer would be one this body just wrote rather than one it read.
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

// readRecord reads one job record. It is the only reader of jobRecordKey there
// is, and it has exactly three callers: enter(), which takes the lock and takes
// this read before it hands a body its handle; reread(), which is mend's second
// read under that same holding; and job() — the one read in this package that
// decides no write.
//
// IT IS NOT WHERE A SECTION COMES FROM ANY MORE, and that is the whole of what
// changed. A read used to be handed the section it was taken in and stamp what
// it returned, so a caller inside a live section could stamp any value at all;
// now the SOURCE named at the door calls this itself, before the body runs, and
// the result is what the body is handed. A read on its own decides nothing and
// can write nothing — there is no handle attached to it.
//
// AND THE RECORD IT RETURNS IS THE RECORD THE KEY NAMES. decodeJobRecord is
// handed the id as well as the key, so a row that decodes cleanly and says it
// is some other job is a corrupt row rather than a record this service goes on
// to decide a transition on.
func (s *Service) readRecord(id string) (jobRecord, bool, error) {
	key := jobRecordKey(id)
	raw, exists := s.store.Get(key)
	if !exists {
		return jobRecord{}, false, nil
	}
	rec, err := decodeJobRecord(key, id, raw)
	if err != nil {
		return jobRecord{}, false, errInternal("%v", err)
	}
	return rec, true, nil
}
