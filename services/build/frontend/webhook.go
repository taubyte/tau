package frontend

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

// handleWebhook is POST /hook/{projectId}, and THE ORDER OF THE CHECKS IS THE
// DESIGN. Each one is placed where it is because of what it costs and what it
// is allowed to know, and the file reads top to bottom in the order a stranger's
// bytes are believed:
//
//	nothing is believed          -> the path, the method, the encoding, the size
//	the cloud is asked           -> only after the rate limits
//	the bytes are authenticated  -> only after the secret
//	the bytes are interpreted    -> only after the signature
//	the cloud does work          -> only after the hook binds the repository
//
// v1 runs that list nearly backwards: the hook lookup IS the route's
// authenticator, so an unauthenticated POST spends a p2p round trip to auth
// before anything at all has been proven, and the whole body is read into
// memory before that.
//
// THERE IS NO DELIVERY-ID REPLAY CACHE, and its absence is a decision rather
// than an omission. A replayed delivery is already answered twice over: the
// backend's job id is a hash of (repository, ref, commit), so a second Enqueue
// of one push returns the first job rather than making a second — that is
// build.proto's own idempotency and it is CORRECT, not tolerated — and the two
// rate limits above bound what a replay costs when it does reach the wire. A
// cache would add a third, weaker copy of a guarantee two mechanisms already
// hold, keyed by yet another string a stranger chooses.
func (s *Service) handleWebhook(w http.ResponseWriter, r *http.Request) {
	// THE PROJECT ID COMES FROM THE PATH AND FROM NOWHERE ELSE. It is in the
	// URL rather than looked up from the body because the body is not believed
	// until it verifies, and verifying needs the project's secret: a project's
	// very first push is verified from its link alone, before anything has been
	// published for it. net/http's own
	// ServeMux resolves a {wildcard} from the path only, and PathValue cannot
	// be reached by a query parameter, a header or a body field. v1's
	// pkg/http/context/helpers.go resolves query FIRST, then mux vars, then
	// header, then body — so `?hook=` overrides the path on every patrick route
	// that has one, and the string that selects the HMAC key is chosen by the
	// caller twice over. ingress_test.go holds that this one is not.
	hookID := r.PathValue("projectId")
	if hookID == "" || len(hookID) > maxHookIDLength {
		respond(w, errDenied)
		return
	}

	// NO EVENT CHECK HERE. Which header names the event is the forge's —
	// X-GitHub-Event, X-Gitlab-Event, X-Event-Key — and this route names no
	// forge, so a ping is told apart from a push only after the delivery
	// verifies (ErrNotAPush below). It costs a rate-limited lookup, which is
	// what every push already costs.

	// ONE ENCODING, BECAUSE THE HOOK IS THIS CLOUD'S OWN. GitHub can be asked
	// to send `application/x-www-form-urlencoded` with the payload in a form
	// field, and every hook this cloud registers asks for json. Accepting the
	// form encoding would be a second parser for a stranger's bytes, for a
	// shape this cloud never asks for.
	if !isJSON(r.Header.Get("Content-Type")) {
		respond(w, problem(http.StatusUnsupportedMediaType, "expected application/json"))
		return
	}

	// THE TWO RATE LIMITS COME BEFORE THE LOOKUP, which is the only ordering
	// that matters — see ratelimit.go for why there are two. The global one is
	// first because it is the one a caller cannot get a fresh bucket for by
	// changing the hook id, and because it gates the map insert the per-hook
	// one would otherwise have to make.
	if !s.globalHooks.allow() || !s.allowHook(hookID) {
		respond(w, tooManyRequests(s.cfg.HookRate.Per))
		return
	}

	// BOUNDED, AND READ EXACTLY ONCE. MaxBytesReader is installed before the
	// first read rather than checked after it, so a body larger than the cap is
	// never in this process's memory. The same bytes are then verified and
	// parsed: reading twice — or verifying one copy and parsing another, which
	// is what v1's `req.Body = io.NopCloser(bytes.NewReader(ctx.Body()))`
	// splice sets up — is how a signature comes to cover something other than
	// what was acted on.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes))
	if err != nil {
		respond(w, problem(http.StatusRequestEntityTooLarge, "payload too large"))
		return
	}

	// The cloud is asked — once, and at most once more below.
	//
	// AN ABSENT HOOK AND A BAD SIGNATURE ARE ONE ANSWER: the same status and
	// the same body, with the reason in this process's log only. Telling them
	// apart tells anybody who can name a project id whether it is linked here,
	// and a project id is in its project.yaml. An operator debugging a rotated
	// secret reads it in the frontend's log, not in the provider's delivery
	// log. A failure to ASK is different — the register or the vault did not
	// answer — and is a 503, because this node does not know which it is and a
	// provider retries a 5xx.
	look, err := s.secrets.get(r.Context(), hookID)
	if err != nil {
		s.refuseLookup(w, hookID, err)
		return
	}
	if !s.usable(w, hookID, look.hook) {
		return
	}

	// THE BYTES ARE AUTHENTICATED, AND ONLY THEN INTERPRETED, by the forge that
	// serves the repository's host, under the scheme that forge signs with.
	// Verify also holds the check v1 does not have: the signature was made with
	// a secret the URL chose, while the repository is named by the BODY, so a
	// push is only ever for one of the project's own repositories — otherwise a
	// signature under any project's secret would authorize a job for any
	// repository this cloud has.
	event, err := s.deliveries.Verify(look.hook, r.Header, body)

	// A REMEMBERED HOOK THAT REFUSES A DELIVERY IS ASKED FOR AGAIN, ONCE, and
	// the same headers and body are verified against the answer. This — not
	// the drop secretapi.go and projectapi.go make — is what makes a rotation
	// correct across the fleet: that drop reaches only the frontend the secret
	// was set through, and every other one is still holding the old secret.
	// Only a CACHED answer is re-asked, never a fresh one, and never twice, so
	// an admitted request costs at most one lookup either way (config.go). Only
	// ErrUnverified triggers it: a delivery that verified and was malformed, or
	// was an event this cloud ignores, says nothing about the secret.
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
		event, err = s.deliveries.Verify(look.hook, r.Header, body)
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

	// 202 AND AN EMPTY BODY. The job id is logged, where an operator tracing a
	// delivery can find it, and is not returned: v1 hands the whole Job back to
	// whoever posted the webhook, and a verified webhook proves only that
	// somebody holds a hook secret — it does not make them a reader of this
	// cloud's state.
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

// usable refuses a hook with no secret. AN EMPTY SECRET IS A 500, NEVER A
// SKIPPED MAC: it is a hook this cloud registered and cannot check, which is
// the operator's to see; forge refuses it too, but its refusal reads as a
// delivery that did not verify.
func (s *Service) usable(w http.ResponseWriter, hookID string, hook Hook) bool {
	if hook.signed() {
		return true
	}
	s.log("hook %s has no secret; refusing to accept an unauthenticated push", hookID)
	respond(w, problem(http.StatusInternalServerError, "hook is not configured"))
	return false
}
