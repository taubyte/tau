package frontend

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/register"
)

// jobapi.go is the GitHub-token-authenticated surface a human or the CLI calls:
// v1's four job routes, with the authorization they never had.
//
// EVERY ONE OF THEM AUTHORIZES BEFORE IT ACTS, and for the two mutating routes
// that means the read that authorizes happens first and the mutation second —
// so a caller who may not touch a job cannot cancel it and learn afterwards
// that they could not.
//
// EVERY ONE OF THEM IS ADMITTED BEFORE IT AUTHORIZES, which is a different
// property and is the one v1 has no version of. Authorizing a job route COSTS
// THIS CLOUD SOMETHING before the caller has proven anything: the job's
// repository_key is the authorization subject, so the backend has to be read
// first, and that read is leader-only through a raft barrier — a log entry
// replicated to quorum. bearer() proves only that a header has a shape. So
// credentials(), below, spends a token from the same two limits webhook.go
// spends, and it is the single door all four routes come through.
//
// WHAT IS NOT HERE, and it is not an oversight: v1's GET /logs/{cid} and
// GET /download/{jobId}/{resourceId}. The first is an open IPFS gateway keyed
// by a bare content id and gated only by a token being valid — so anyone with
// any GitHub token can read any log this cloud ever stored, given its cid. The
// second has no Auth block at all (defects.md D4, Critical). They come back as
// GET /job/{jid}/logs/{key} and GET /job/{jid}/assets/{resource}, authorized
// exactly as Get is here and checked against that job's own maps — which is
// what makes them per-job rather than per-cid. There is no bare-cid route in
// v2 (assetapi.go).
//
// AND A THIRD IS HERE THAT v1 HAS NO VERSION OF: following a build WHILE IT
// RUNS. GET /job/{jid}/logs, with an offset, resumable by a client that
// reconnects, or with ?snapshot=true just what it has logged so far, under the
// same authorization as everything else in this file.
//
// THE ROUTING QUESTION THIS WAITED ON IS ANSWERED, and the answer is a status
// code. The log file is on whichever backend node is running that build; every
// other node says Unavailable rather than NotFound, and a pooled caller walks
// to the next peer on exactly that code — so "ask the cloud" finds the writer
// without anything holding a table of which node runs what. The request carries
// the offset the caller reached, so a walk RESUMES instead of repeating.

// jobJSON is how a Job reaches a caller: protojson over the generated message,
// with the proto's own field names.
//
// THE HTTP SHAPE IS DERIVED FROM THE SCHEMA rather than typed a second time in
// a struct beside it, so a field added to build.proto appears here without an
// edit and cannot appear here spelled differently than it is on the wire.
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

// handleFollowJob is GET /job/{jid}/logs. With ?snapshot=true the response
// ends at what the log holds now instead of when the build does.
//
// IT IS NOT BOUNDED BY BackendTimeout, and that is the one thing about it that
// is not like the routes above. BackendTimeout bounds ONE CALL OUT OF THIS
// PROCESS on the assumption that a call returns; a follow returns when the
// build ends, which is the whole point of it. It is bounded by the caller
// hanging up and by the build finishing, and by nothing else.
//
// THE AUTHORIZATION IS DECIDED ONCE, BEFORE THE FIRST BYTE. A build can run for
// an hour and re-checking write access every chunk would ask the provider a
// question per chunk; a caller whose access is revoked mid-build keeps the
// stream it already opened and is refused the next one. That is the same
// bargain every streaming API makes, and it is written down rather than
// discovered.
//
// THE RESPONSE IS ONE RUN'S LOG, and X-Job-Attempt says which: ?attempt=N, or
// without it the job's current run. An offset is a position in that run's
// log, so a client resuming sends the run back with it — a job whose node went
// away runs again, a retried one too, and the new run's log starts again from
// nothing, so an offset carried over to it would splice the two.
//
// WHATEVER STATE THE RUN IS IN, THE RESPONSE IS ITS LOG. A follow of one no
// node is writing yet waits for it to start (followLog); a snapshot of it is
// empty, since it has logged nothing so far. One that has ended is read from
// where its record files it (storedLog). One that ended with no log kept is a
// 410, so that it cannot read as a log that stopped there — and found out
// after the 200, the response is cut rather than ended, so the client resumes
// and is told then.
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

	// THE OFFSET IS ECHOED IN A TRAILER-SHAPED HEADER NOWHERE, deliberately:
	// the response is the log and nothing else, so a client resuming counts the
	// bytes it wrote and adds them to the offset it asked for. That is the same
	// arithmetic the backend does and it needs no framing on this hop.
	out := &logResponse{w: w, run: run}
	err = s.readLog(r.Context(), jobID, run, from, snapshot, out)
	switch {
	case err == nil:
		out.open()
	case !out.opened:
		w.Header().Set(attemptHeader, strconv.Itoa(int(run)))
		respond(w, err)
	default:
		// THE STATUS IS ALREADY SENT, so the failure is logged and the response
		// is CUT: ended, it would read as the whole log, and a client could not
		// tell where the log stopped from where the reading did. Cut, it is a
		// read that broke, which its offset lets it resume.
		s.log("following job %s: %v", jobID, err)
		panic(http.ErrAbortHandler)
	}
}

// attemptHeader names the run a log response is about.
const attemptHeader = "X-Job-Attempt"

// readLog writes run's log of jobID to out from `from`: what it holds now, and
// unless snapshot, what it goes on to write until the run ends.
//
// THE FIRST ASK IS A SNAPSHOT, for a follow too, because a snapshot answers at
// once whatever the run is doing and the status waits for its answer: soon
// enough that a viewer sees the response open on a build that has printed
// nothing yet, and late enough that a run whose log was not kept is a 410
// rather than a 200 that ends where nothing was.
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

// followLog is Backend.Follow of one run that waits while no node is writing
// its log, asking again from FollowBackoff, doubled per ask up to
// FollowBackoffMax, until a node does, the run ends, or the caller hangs up.
//
// IT ASKS AGAIN AFTER A BREAK TOO, from where the break left it. The request
// names the run, so whatever answers next is that run's log or says it has
// ended — never another run's bytes from the same offset, which is why this
// once stopped at the first byte.
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

// storedLog writes, from `from`, the log an ended run left: what its job's
// record files under its attempt. The record is read again, because the
// authorization's read was taken before the run ended.
//
// NO OTHER RUN'S ENTRY IS THIS RUN'S LOG. A run with none left nothing: one the
// job ended before reaching has logged nothing, which is a whole answer, and
// one that ran had its log lost with its node or refused by the store, which
// is errLogNotKept.
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
	content, err := s.assets.Get(ctx, cid)
	if err != nil {
		return err
	}
	defer content.Close()
	if _, err := content.Seek(from, io.SeekStart); err != nil {
		return err
	}
	_, err = io.Copy(w, content)
	return err
}

// logOf is the cid a job's record files run's log under. The keys are the
// attempt in decimal, padded so that they sort; read as numbers, the padding
// is not this package's to know.
func logOf(job *buildv2.Job, run int32) (string, bool) {
	for key, cid := range job.GetLogs() {
		if n, err := strconv.ParseInt(key, 10, 32); err == nil && int32(n) == run {
			return cid, true
		}
	}
	return "", false
}

// logResponse is a log read's response. Its status and headers go with the
// first byte, or when the read has nothing to send yet, so that until then a
// refusal can still be a status; after that each write is sent as it is made,
// since a log held back in a buffer would defeat the route.
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

// offsetOf reads ?offset=N.
//
// A NEGATIVE OFFSET IS REFUSED RATHER THAN CLAMPED. It means the caller
// computed one, and a caller whose arithmetic went backwards should hear about
// it rather than silently start over from the beginning of somebody's build.
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

// handleCancelJob is POST /cancel/{jid}.
//
// It answers 204: there is nothing to say. v1 answers {"cancelled": jid} —
// echoing back the id the caller just sent — and gets there through three hops
// and up to a minute of polling for a claim record, which it needs because in
// v1 the job record and the running job live in different services. In v2 they
// do not, so this is one call.
func (s *Service) handleCancelJob(w http.ResponseWriter, r *http.Request, token, jobID string) {
	s.mutate(w, r, token, jobID, s.backend.Cancel, http.StatusNoContent)
}

// handleRetryJob is POST /retry/{jid}.
//
// It answers 202 rather than 204 because the difference is real: the job is
// queued, not done. v1's retry answered success without doing anything at all
// when the job was still open — it looked the id up, found it, and returned
// {"retry": jid} — so "retry" on a running job was a lie the caller could not
// detect. The backend answers that case with a FailedPrecondition, which
// arrives here as a 409.
func (s *Service) handleRetryJob(w http.ResponseWriter, r *http.Request, token, jobID string) {
	s.mutate(w, r, token, jobID, s.backend.Retry, http.StatusAccepted)
}

// mutate is cancel and retry: authorize on the job, then act on it. The two
// differ by one function and one status code, and writing them twice would be
// two places for the authorization to be forgotten in.
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

// handleListJobs is GET /jobs/{projectId}.
//
// IT STREAMS. The listing is unbounded and unpaginated — as v1's is, and as
// build.proto says JobService.List is — so the difference that matters is that
// neither the backend nor this process holds the whole answer to send it. v1
// materializes every job id in memory first, in the internet-facing process.
//
// The cost is that a failure after the first id cannot change the status code,
// which is inherent to streaming and is handled honestly below rather than
// hidden: the array is left unterminated, so a caller's json decoder reports a
// truncated document instead of silently accepting a short list as complete.
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

// jobHandler is what a job route's handler is, and THE TWO EXTRA PARAMETERS ARE
// THE POINT. A job handler is given the credential and the path value rather
// than reading them, so there is no version of one that fetches them itself and
// skips what reading them costs. It is not an http.HandlerFunc and the mux will
// not take it.
type jobHandler func(w http.ResponseWriter, r *http.Request, token, id string)

// admitted turns a jobHandler into a handler the mux WILL take, and it is the
// only thing in this package that does.
//
// THAT IS THE GUARANTEE, and it is a compile-time one rather than a habit.
// Every one of the four routes below reaches the cloud, and every one of those
// reaches costs something before the caller has proven anything — so the check
// that bounds it must not be a line each handler remembers. Retry is the route that proves why
// the difference matters: it QUEUES A BUILD, so a copy of this function's three
// lines missing from it is not a slow read, it is a stranger's tenant asking
// this cloud for containers as fast as it can open connections. Written as a
// habit it was missing; written as a type it cannot be.
//
// It also puts the path value's name beside the pattern that declares it
// (ingress.go), so "{jid}" and "jid" are read together instead of a segment
// apart, and a route whose two spellings disagree is visible where it is
// mounted.
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

// credentials reads the bearer, reads one bounded path value, and admits the
// request — IN THAT ORDER, and the order is the whole of it.
//
// THE FREE CHECKS COME FIRST. bearer and pathID read a header and a path
// segment: no allocation that outlives the call, no map insert, nothing asked
// of anything. Spending a limiter token on them would let a caller drain the
// global bucket with requests that cost this cloud nothing, and lock out the
// callers whose requests would have cost something — a rate limit turned into
// the denial of service it exists to prevent.
//
// THE GLOBAL LIMIT COMES BEFORE THE PER-CALLER ONE, for webhook.go's reason:
// the per-caller bucket lives in a map keyed by a string a stranger chooses, so
// the unkeyable limit has to be what gates the insert.
//
// IT IS ONE FUNCTION BECAUSE IT IS ONE DOOR, and admitted() above is what makes
// it the only one: a job route reaches nothing without a token and an id, this
// is the only place either comes from, and the mux takes nothing that has not
// been through it.
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

// writeJSONString writes one string as a json literal. It goes through
// encoding/json rather than fmt so that a value which somehow contained a quote
// could not close it — a job id cannot, being 64 hex characters, but a
// hand-rolled quote in an internet-facing response is the kind of thing that
// stays correct only as long as its input never changes.
//
// json.Marshal cannot fail on a string, so the error return below is the one
// unreachable branch in this package. It stays because the alternative is to
// assume that, and the assumption is exactly what a hand-rolled quote is.
func writeJSONString(w http.ResponseWriter, s string) error {
	encoded, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = w.Write(encoded)
	return err
}
