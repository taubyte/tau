package frontend

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/build/livelog"
)

// jobapi.go is the job API a person or the CLI calls. Every route authorizes
// before it acts, and is admitted before it authorizes: authorizing reads the job
// from the backend leader through a raft barrier, which a well-shaped header
// alone must not be able to trigger (credentials()). Logs and artifacts are per
// job (assetapi.go), never by bare cid. A running build is followed at
// GET /job/{jid}/logs; the writer is found because every other backend answers
// Unavailable and the pool walks on, resuming from the offset.

// jobJSON is protojson over the generated Job, with proto field names, so the HTTP
// shape follows the schema.
var jobJSON = protojson.MarshalOptions{UseProtoNames: true}

// handleGetJob is GET /job/{jid}.
func (s *Service) handleGetJob(w http.ResponseWriter, r *http.Request, token, jobID string) {
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()

	job, err := s.mayTouchJob(ctx, token, jobID, register.RoleViewer)
	if err != nil {
		respond(w, err)
		return
	}

	body, err := jobJSON.Marshal(job)
	if err != nil {
		s.log("encoding job %s: %v", jobID, err)
		respond(w, errUpstream)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(body)
}

// handleFollowJob is GET /job/{jid}/logs; with ?snapshot=true it ends at what the
// log holds now. Not bounded by BackendTimeout: it ends when the build does or the
// caller hangs up. Authorization is decided once, before the first byte. The
// response is one run's log (?attempt=N, else the current run), named in
// X-Job-Attempt, since an offset belongs to one run. An unstarted run is waited
// for; an ended one is read from its record; one ended with no log is a 410, or a
// cut response if found after the 200.
func (s *Service) handleFollowJob(w http.ResponseWriter, r *http.Request, token, jobID string) {
	authorize, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	job, err := s.mayTouchJob(authorize, token, jobID, register.RoleViewer)
	cancel()
	if err != nil {
		respond(w, err)
		return
	}

	from, err := offsetOf(r)
	if err != nil {
		respond(w, err)
		return
	}
	snapshot, err := snapshotOf(r)
	if err != nil {
		respond(w, err)
		return
	}
	run, err := attemptOf(r, job)
	if err != nil {
		respond(w, err)
		return
	}

	// The response is the log only: a client resuming adds the bytes it got to its
	// offset.
	out := &logResponse{w: w, run: run}
	err = s.readLog(r.Context(), jobID, run, from, snapshot, out)
	switch {
	case err == nil:
		out.open()
	case !out.opened:
		w.Header().Set(attemptHeader, strconv.Itoa(int(run)))
		respond(w, err)
	default:
		// The status is already sent, so the response is cut rather than ended, which a
		// client resumes from.
		s.log("following job %s: %v", jobID, err)
		panic(http.ErrAbortHandler)
	}
}

// attemptHeader names the run a log response is about.
const attemptHeader = "X-Job-Attempt"

// readLog writes run's log of jobID to out from `from`, and unless snapshot keeps
// following. The first ask is a snapshot, so the status can still be a 410 for an
// unkept log.
func (s *Service) readLog(ctx context.Context, jobID string, run int32, from int64, snapshot bool, out *logResponse) error {
	reached, err := s.backend.Snapshot(ctx, jobID, run, from, out.emit)
	if !snapshot && (err == nil || connect.CodeOf(err) == connect.CodeUnavailable) {
		out.open()
		reached, err = s.followLog(ctx, jobID, run, reached, out.emit)
	}
	switch {
	case connect.CodeOf(err) == connect.CodeFailedPrecondition:
		return s.storedLog(ctx, jobID, run, reached, out)
	case snapshot && reached == from && connect.CodeOf(err) == connect.CodeUnavailable:
		// No node is writing it yet, so it has logged nothing so far.
		return nil
	}
	return err
}

// followLog follows one run, asking again with backoff while no node writes it,
// and after a break from where it left off; the request names the run, so no other
// run's bytes come back.
func (s *Service) followLog(ctx context.Context, jobID string, run int32, from int64, emit func([]byte, int64) error) (int64, error) {
	wait := s.cfg.FollowBackoff
	for {
		reached, err := s.backend.Follow(ctx, jobID, run, from, emit)
		if connect.CodeOf(err) != connect.CodeUnavailable {
			return reached, err
		}
		if reached != from {
			from, wait = reached, s.cfg.FollowBackoff
		}
		select {
		case <-ctx.Done():
			return from, nil
		case <-time.After(wait):
		}
		wait = min(2*wait, s.cfg.FollowBackoffMax)
	}
}

// storedLog writes, from `from`, an ended run's stored log, re-reading the record
// since authorization's read predates the end. A run that never ran logged
// nothing; one whose log was lost is errLogNotKept.
func (s *Service) storedLog(ctx context.Context, jobID string, run int32, from int64, w io.Writer) error {
	read, cancel := context.WithTimeout(ctx, s.cfg.BackendTimeout)
	job, err := s.backend.Get(read, jobID)
	cancel()
	if err != nil {
		return err
	}
	cid, kept := logOf(job, run)
	switch {
	case !kept && run > job.GetAttempt():
		return nil
	case !kept:
		return errLogNotKept
	case s.assets == nil:
		return problem(http.StatusServiceUnavailable, "this node reads no asset store")
	}
	stored, err := s.assets.Read(ctx, cid)
	if err != nil {
		return err
	}
	content := livelog.Decompressed(stored)
	defer content.Close()
	// A compressed log has no offsets to seek to, so the bytes before from are
	// read and dropped.
	if _, err := io.CopyN(io.Discard, content, from); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	_, err = io.Copy(w, content)
	return err
}

// logOf is the cid a job's record files run's log under, keyed by the padded
// attempt and read as numbers.
func logOf(job *buildv2.Job, run int32) (string, bool) {
	for key, cid := range job.GetLogs() {
		if n, err := strconv.ParseInt(key, 10, 32); err == nil && int32(n) == run {
			return cid, true
		}
	}
	return "", false
}

// logResponse sends status and headers with the first byte (or when there is
// nothing yet), so a refusal can still be a status; afterwards each write is
// flushed.
type logResponse struct {
	w      http.ResponseWriter
	run    int32
	opened bool
}

func (o *logResponse) open() {
	if o.opened {
		return
	}
	o.opened = true
	h := o.w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	// A proxy that buffered this would defeat the whole route.
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	h.Set(attemptHeader, strconv.Itoa(int(o.run)))
	o.w.WriteHeader(http.StatusOK)
	o.flush()
}

func (o *logResponse) Write(p []byte) (int, error) {
	o.open()
	n, err := o.w.Write(p)
	o.flush()
	return n, err
}

func (o *logResponse) emit(data []byte, _ int64) error {
	_, err := o.Write(data)
	return err
}

func (o *logResponse) flush() {
	if flusher, ok := o.w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// offsetOf reads ?offset=N; a negative offset is refused, not clamped.
func offsetOf(r *http.Request) (int64, error) {
	raw := r.URL.Query().Get("offset")
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, problem(http.StatusBadRequest, "offset must be a non-negative integer")
	}
	return n, nil
}

// snapshotOf reads ?snapshot=, refusing a value that is not a boolean the way
// offsetOf refuses an offset that is not a number.
func snapshotOf(r *http.Request) (bool, error) {
	raw := r.URL.Query().Get("snapshot")
	if raw == "" {
		return false, nil
	}
	snapshot, err := strconv.ParseBool(raw)
	if err != nil {
		return false, problem(http.StatusBadRequest, "snapshot must be true or false")
	}
	return snapshot, nil
}

// attemptOf reads ?attempt=N, the run a log read is about, refusing one that is
// not a positive number the way offsetOf refuses an offset that is not one.
// Without it the read is about the job's current run: the one claimed or last
// run, or a queued job's next.
func attemptOf(r *http.Request, job *buildv2.Job) (int32, error) {
	raw := r.URL.Query().Get("attempt")
	if raw == "" {
		if job.GetStatus() == buildv2.JobStatus_JobStatusOpen || job.GetAttempt() == 0 {
			return job.GetAttempt() + 1, nil
		}
		return job.GetAttempt(), nil
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || n < 1 {
		return 0, problem(http.StatusBadRequest, "attempt must be a positive integer")
	}
	return int32(n), nil
}

// handleCancelJob is POST /cancel/{jid}, answering 204.
func (s *Service) handleCancelJob(w http.ResponseWriter, r *http.Request, token, jobID string) {
	s.mutate(w, r, token, jobID, s.backend.Cancel, http.StatusNoContent)
}

// handleRetryJob is POST /retry/{jid}, answering 202 since the job is queued; a
// still-open job is a 409.
func (s *Service) handleRetryJob(w http.ResponseWriter, r *http.Request, token, jobID string) {
	s.mutate(w, r, token, jobID, s.backend.Retry, http.StatusAccepted)
}

// mutate is cancel and retry: authorize on the job, then act.
func (s *Service) mutate(w http.ResponseWriter, r *http.Request, token, jobID string, act func(context.Context, string) error, status int) {
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()

	if _, err := s.mayTouchJob(ctx, token, jobID, register.RoleBuilder); err != nil {
		respond(w, err)
		return
	}
	if err := act(ctx, jobID); err != nil {
		respond(w, err)
		return
	}
	w.WriteHeader(status)
}

// handleListJobs is GET /jobs/{projectId}. It streams, so no process holds the
// whole list; a failure after the first id leaves the array unterminated so a
// decoder sees truncation.
func (s *Service) handleListJobs(w http.ResponseWriter, r *http.Request, token, projectID string) {
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()

	if _, err := s.mayTouchProject(ctx, token, projectID, register.RoleViewer); err != nil {
		respond(w, err)
		return
	}

	sent := 0
	err := s.backend.List(ctx, projectID, func(jobID string) error {
		if sent == 0 {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if _, err := w.Write([]byte("[")); err != nil {
				return err
			}
		} else if _, err := w.Write([]byte(",")); err != nil {
			return err
		}
		sent++
		return writeJSONString(w, jobID)
	})

	if err != nil {
		s.log("listing jobs of %s: %v", projectID, err)
		if sent == 0 {
			respond(w, err)
			return
		}
		// Committed. Leave the array open so the truncation is visible.
		return
	}

	if sent == 0 {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	w.Write([]byte("]"))
}

// jobHandler is handed the credential and path value rather than reading them, and
// is not an http.HandlerFunc, so only admitted() can mount it.
type jobHandler func(w http.ResponseWriter, r *http.Request, token, id string)

// admitted is the only way a jobHandler reaches the mux, so the admission check is
// a type, not a habit (Retry queues builds). The path value's name sits beside its
// pattern in ingress.go.
func (s *Service) admitted(name string, handle jobHandler) http.HandlerFunc {
	return s.admittedWithin(name, maxIDLength, handle)
}

// admittedWithin is admitted with its own bound on the path value, for a
// route whose value is a path rather than an id.
func (s *Service) admittedWithin(name string, bound int, handle jobHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, id, err := s.credentials(r, name, bound)
		if err != nil {
			respond(w, err)
			return
		}
		handle(w, r, token, id)
	}
}

// credentials reads the bearer, reads one bounded path value, then admits, in that
// order: the free checks first so they spend no token, and the global limit before
// the per-caller map insert.
func (s *Service) credentials(r *http.Request, name string, bound int) (token, id string, err error) {
	if token, err = bearer(r); err != nil {
		return "", "", err
	}
	if name != "" {
		if id, err = boundedPath(r, name, bound); err != nil {
			return "", "", err
		}
	}
	if !s.globalJobs.allow() || !s.allowCaller(token) {
		return "", "", tooManyRequests(s.cfg.CallerRate.Per)
	}
	return token, id, nil
}

// writeJSONString writes one string as a json literal through encoding/json.
func writeJSONString(w http.ResponseWriter, s string) error {
	encoded, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = w.Write(encoded)
	return err
}
