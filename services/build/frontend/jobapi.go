package frontend

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"google.golang.org/protobuf/encoding/protojson"

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
// reconnects, under the same authorization as everything else in this file.
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

// handleFollowJob is GET /job/{jid}/logs.
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
func (s *Service) handleFollowJob(w http.ResponseWriter, r *http.Request, token, jobID string) {
	authorize, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	if _, err := s.mayTouchJob(authorize, token, jobID, register.RoleViewer); err != nil {
		cancel()
		respond(w, err)
		return
	}
	cancel()

	from, err := offsetOf(r)
	if err != nil {
		respond(w, err)
		return
	}

	// THE HEADERS GO BEFORE THE FIRST CHUNK IS ASKED FOR, so a viewer sees the
	// connection open on a build that has produced nothing yet rather than
	// waiting on a blank response for the first line of output.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// A proxy that buffered this would defeat the whole route.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}

	// THE OFFSET IS ECHOED IN A TRAILER-SHAPED HEADER NOWHERE, deliberately:
	// the response is the log and nothing else, so a client resuming counts the
	// bytes it wrote and adds them to the offset it asked for. That is the same
	// arithmetic the backend does and it needs no framing on this hop.
	_, err = s.backend.Follow(r.Context(), jobID, from, func(data []byte, _ int64) error {
		if _, err := w.Write(data); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	})
	if err != nil {
		// THE STATUS IS ALREADY SENT. There is nothing to respond with, so the
		// failure is logged and the connection ends — which is what a client
		// tailing a log sees as "it stopped", and what its offset lets it
		// recover from.
		s.log("following job %s: %v", jobID, err)
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
// could not close it — a job id cannot, being 43 base64url characters, but a
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
