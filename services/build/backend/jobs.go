package backend

import (
	"context"
	"errors"
	"io"
	"slices"

	"connectrpc.com/connect"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// jobHandler is the connect adapter for JobService. As in queue.go, each method
// is authorize, lead, delegate — the bodies are plain methods below, which is
// where this package's coverage comes from.
type jobHandler struct{ s *Service }

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

// job assembles one Job. It is ONE READ: the record carries the whole of it,
// including the cids of what its runs produced.
//
// IT IS THE ONE READ OF A RECORD THAT DECIDES NO WRITE, so it is the one that
// takes no section: there is nothing for a concurrent transition to make stale,
// and serializing every Get behind every build's bookkeeping would buy a
// consistency this shape does not promise anyway.
//
// IT USED TO BE A LISTING. The logs and assets were one row per entry under
// this job's attempt prefix, so assembling a Job meant a prefix scan and a Get
// per row — and writing one meant a replicated entry per log line, from the one
// writer in this package that was outside every section. The maps are fields of
// the record now (store.go), and what they hold is a content id per attempt and
// per resource rather than one per line.
//
// THE MAPS ARE COPIED OUT. The proto message a caller is handed must not share
// a map with the record this process decoded, or a reader and a later write of
// that record are two writers of one map.
func (s *Service) job(id string) (*buildv2.Job, error) {
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
	}, nil
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
func (s *Service) list(projectID string, emit func(*buildv2.JobRef) error) error {
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
		if err := emit(&buildv2.JobRef{Id: id}); err != nil {
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
// settles against a record that is already terminal, so it writes what that run
// PRINTED and never a status, and it is bounded by the claim deadline
// regardless.
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
		if rec.status() == buildv2.JobStatus_JobStatusCancelled {
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
		if err := j.finish(buildv2.JobStatus_JobStatusCancelled, "cancelled"); err != nil {
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
// WHAT EARLIER ATTEMPTS PRODUCED IS KEPT: the record's attempt counter keeps
// going up, so the next run's log is filed under a higher attempt and the
// previous one's cid stays where it is. Push after an Ack is a genuinely new queue
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

		rec.Status = int32(buildv2.JobStatus_JobStatusOpen)
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

// followChunk is how much of a log one message carries.
//
// IT IS SMALL ON PURPOSE. A follower is a person watching a build, so latency
// to the first byte matters and throughput does not — a chunk that waited to
// fill would hold back the line that says what went wrong. livelog's Reader
// blocks until there is something, so a slow build sends short chunks and a
// fast one sends full ones with no rule here deciding which.
const followChunk = 16 << 10

func (h *jobHandler) Artifact(ctx context.Context, req *connect.Request[buildv2.ArtifactRequest]) (*connect.Response[buildv2.Built], error) {
	cid, err := h.s.artifact(ctx, req.Msg.GetProject(), req.Msg.GetEnvironment(), req.Msg.GetResource())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&buildv2.Built{Cid: cid}), nil
}

// artifact reads what one resource of one deployment currently runs.
//
// THE AUTHORIZATION IS THE PROJECT'S, not a job's, and it is the one place in
// this service where that is true — every other read is about a job and is
// authorized on the job's repository. A worker asking what to run has no job in
// hand, and the thing it is asking about belongs to the project. That is why it
// is ActArtifact rather than ActRead: an authorizer told "read" with a project
// in the subject would either check it against jobs and refuse everything, or
// stop checking and allow everything.
func (s *Service) artifact(ctx context.Context, project, environment, resource string) (string, error) {
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
	if err := s.lead(); err != nil {
		return "", err
	}

	cid, ok := s.store.Get(artifactKey(project, environment, resource))
	if !ok {
		// A RESOURCE NOTHING HAS BUILT IS NotFound, and that is not the same as
		// a resource that needs no build — an image is pulled and a component
		// is built as its own resource. Which of those was asked about is the
		// caller's to know.
		return "", errNotFound("build: nothing has been built for %s/%s's %s", project, environment, resource)
	}
	if len(cid) == 0 {
		return "", errInternal("build: %s/%s's %s names an empty artifact", project, environment, resource)
	}
	return string(cid), nil
}

func (h *jobHandler) Follow(ctx context.Context, req *connect.Request[buildv2.FollowRequest], stream *connect.ServerStream[buildv2.LogChunk]) error {
	if err := h.s.allow(ctx, ActRead, req.Msg.GetJobId()); err != nil {
		return err
	}
	return h.s.follow(ctx, req.Msg.GetJobId(), req.Msg.GetOffset(), stream.Send)
}

// follow streams a running build's output to send, from offset to the end.
//
// IT IS SEPARATE FROM THE HANDLER for the same reason job() and list() are: the
// connect adapter decides authorization and nothing else, so what this does can
// be driven without a stream on either side of it.
func (s *Service) follow(ctx context.Context, jobID string, offset int64, send func(*buildv2.LogChunk) error) error {
	if offset < 0 {
		return errInvalidArgument("build: offset %d is before the start of the log", offset)
	}

	reader, err := s.Follow(jobID, offset)
	if err != nil {
		// A NODE THAT IS NOT WRITING THIS LOG IS Unavailable ON THE WIRE, not
		// NotFound. The in-process answer is honest — the log is not here — but
		// a caller's pool walks to the next backend on Unavailable and stops on
		// NotFound, so answering NotFound would make "ask the cloud" mean "ask
		// whichever node the resolver happened to name first".
		//
		// It is narrowed to exactly that case: an offset past the end, a job id
		// that is not one, and a job this caller may not read all keep their
		// own codes, so a bad request does not walk the whole fleet.
		if connect.CodeOf(err) == connect.CodeNotFound {
			return errUnavailable("%v", err)
		}
		return err
	}
	defer reader.Close()

	buf := make([]byte, followChunk)
	for {
		// THE CONTEXT IS CHECKED BEFORE THE READ, because livelog's Reader
		// blocks until the writer appends or closes: a viewer that hung up
		// during a quiet stretch of a long build would otherwise be noticed
		// only when the build ended.
		if ctx.Err() != nil {
			return nil
		}

		n, err := reader.Read(buf)
		if n > 0 {
			offset += int64(n)
			if sendErr := send(&buildv2.LogChunk{Data: buf[:n], NextOffset: offset}); sendErr != nil {
				return sendErr
			}
		}
		if err != nil {
			// EOF IS THE BUILD ENDING, WHICH IS A COMPLETE ANSWER. livelog does
			// not return it early — a reader that catches up blocks — so
			// reaching it means the writer closed and a follower has seen the
			// whole log.
			if errors.Is(err, io.EOF) {
				return nil
			}
			return errUnavailable("build: following job %s: %v", jobID, err)
		}
	}
}
