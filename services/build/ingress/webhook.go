package ingress

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
)

// maxHookIDLength bounds the one caller-chosen string this route puts in a map.
// The path names a project id — a CID project.yaml carries — and nothing
// legitimate is long; the cache and the rate limiter are both keyed by it.
const maxHookIDLength = 256

// handleWebhook is POST /hook/{projectId}, its checks ordered by what each costs
// and may know:
//
//	nothing is believed          -> the path, the method, the encoding, the size
//	the cloud is asked           -> only after the rate limits
//	the bytes are authenticated  -> only after the secret
//	the bytes are interpreted    -> only after the signature
//	the cloud does work          -> only after the hook binds the repository
//
// No delivery-id replay cache: Enqueue is idempotent on (repository, ref, commit)
// and the rate limits bound a replay's cost.
func (s *Service) handleWebhook(w http.ResponseWriter, r *http.Request) {
	// The project id comes from the path only: the body is not believed until it
	// verifies, which needs the project's secret.
	hookID := r.PathValue("projectId")
	if hookID == "" || len(hookID) > maxHookIDLength {
		respond(w, errDenied)
		return
	}

	// No event check here: which header names the event is the forge's, so a ping is
	// told apart only after verification.

	// JSON only: every hook this cloud registers asks for it, so no second parser for a
	// stranger's bytes.
	if !isJSON(r.Header.Get("Content-Type")) {
		respond(w, problem(http.StatusUnsupportedMediaType, "expected application/json"))
		return
	}

	// Both rate limits come before the lookup, the global one first since it gates the
	// per-hook map insert.
	if !s.globalHooks.allow() || !s.allowHook(hookID) {
		respond(w, tooManyRequests(s.cfg.HookRate.Per))
		return
	}

	// Bounded before the first read and read once; the same bytes are verified and
	// parsed.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes))
	if err != nil {
		respond(w, problem(http.StatusRequestEntityTooLarge, "payload too large"))
		return
	}

	// An absent hook and a bad signature get the same answer, the reason only in the
	// log, so a project id's linkage is not revealed. A failure to ask is a 503 so the
	// provider retries.
	look, err := s.secrets.get(r.Context(), hookID)
	if err != nil {
		s.refuseLookup(w, hookID, err)
		return
	}
	if !s.usable(w, hookID, look.hook) {
		return
	}

	// Authenticated, then interpreted, by the forge serving the repository's host;
	// Verify also requires the push to be for one of the project's own repositories.
	event, err := s.deliveries.Verify(look.hook.accepting(s.now(), s.cfg.HookSecretGrace), r.Header, body)

	// A remembered hook that refuses a delivery is asked again once and the delivery
	// re-verified: this is what makes rotation correct on every frontend. Only a
	// cached answer, only on ErrUnverified, never twice.
	if errors.Is(err, ErrUnverified) && look.cached {
		s.log("hook %s: %v; asking again in case the secret was rotated", hookID, err)
		look, err = s.secrets.refresh(r.Context(), hookID, look.ticket)
		if err != nil {
			s.refuseLookup(w, hookID, err)
			return
		}
		if !s.usable(w, hookID, look.hook) {
			return
		}
		event, err = s.deliveries.Verify(look.hook.accepting(s.now(), s.cfg.HookSecretGrace), r.Header, body)
	}

	switch {
	case errors.Is(err, ErrNotAPush):
		accepted(w, "not a push event")
		return
	case errors.Is(err, ErrRefDeleted):
		respond(w, ignored("ref deleted"))
		return
	case errors.Is(err, ErrMalformed):
		s.log("hook %s: %v", hookID, err)
		respond(w, problem(http.StatusBadRequest, "malformed payload"))
		return
	case errors.Is(err, ErrUnverified):
		s.log("hook %s: delivery refused: %v", hookID, err)
		respond(w, errDenied)
		return
	case err != nil:
		s.log("hook %s cannot be verified here: %v", hookID, err)
		respond(w, problem(http.StatusInternalServerError, "hook is not configured"))
		return
	}

	if err := s.refusal(event); err != nil {
		s.log("hook %s: %v", hookID, err)
		respond(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()

	jobID, err := s.backend.Enqueue(ctx, event.Repository, event.Ref, event.Commit)
	if err != nil {
		s.log("enqueue %s %s: %v", event.Repository, event.Commit, err)
		respond(w, err)
		return
	}

	// 202 and an empty body; the job id goes to the log.
	s.log("hook %s queued job %s for %s %s", hookID, jobID, event.Repository, event.Ref)
	accepted(w, "")
}

// isJSON reports whether a Content-Type names json, parameters and all.
func isJSON(header string) bool {
	mediaType, _, err := mime.ParseMediaType(header)
	return err == nil && mediaType == "application/json"
}

// refuseLookup answers a hook lookup that did not produce a hook: the uniform
// denial when the cloud said there is none, a 503 when it could not be asked.
func (s *Service) refuseLookup(w http.ResponseWriter, hookID string, err error) {
	if absent(err) {
		s.log("hook %s: refused, no hook: %v", hookID, err)
		respond(w, errDenied)
		return
	}
	s.log("hook %s: lookup failed: %v", hookID, err)
	respond(w, problem(http.StatusServiceUnavailable, "try again later"))
}

// usable refuses a hook with no secret as a 500, never a skipped MAC.
func (s *Service) usable(w http.ResponseWriter, hookID string, hook Hook) bool {
	if hook.signed() {
		return true
	}
	s.log("hook %s has no secret; refusing to accept an unauthenticated push", hookID)
	respond(w, problem(http.StatusInternalServerError, "hook is not configured"))
	return false
}
