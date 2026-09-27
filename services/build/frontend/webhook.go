package frontend

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
)

// maxHookIDLength bounds the one caller-chosen string this route puts in a map.
// A hook id is a generated identifier (services/auth/github.go mints it with
// id.Generate over the repository's key row); nothing legitimate is long, and
// the cache and the rate limiter are both keyed by it.
const maxHookIDLength = 256

// handleWebhook is POST /hook/{hook}, and THE ORDER OF THE CHECKS IS THE
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
	// THE HOOK ID COMES FROM THE PATH AND FROM NOWHERE ELSE. net/http's own
	// ServeMux resolves a {wildcard} from the path only, and PathValue cannot
	// be reached by a query parameter, a header or a body field. v1's
	// pkg/http/context/helpers.go resolves query FIRST, then mux vars, then
	// header, then body — so `?hook=` overrides the path on every patrick route
	// that has one, and the string that selects the HMAC key is chosen by the
	// caller twice over. ingress_test.go holds that this one is not.
	hookID := r.PathValue("hook")
	if hookID == "" || len(hookID) > maxHookIDLength {
		respond(w, errDenied)
		return
	}

	// NO EVENT CHECK HERE. Which header names the event is the forge's —
	// X-GitHub-Event, X-Gitlab-Event, X-Event-Key — and this route names no
	// forge, so a ping is told apart from a push only after the delivery
	// verifies (ErrNotAPush below). It costs a cached, rate-limited lookup,
	// which is what every push already costs.

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

	// The cloud is asked. A hook nobody registered is a 404; a hook whose
	// signature does not verify is a 403 below. That distinction admits, to
	// somebody who can already name a hook id, that the hook exists — and a
	// hook id is derived from a repository key the caller must already hold to
	// compute it, so what leaks is "is this repository registered here" to
	// somebody who named the repository. It is kept because the alternative
	// makes a rotated secret indistinguishable from a deleted hook in the
	// provider's delivery log, which is where an operator has to debug it.
	hook, err := s.hook(r.Context(), hookID)
	if err != nil {
		s.log("hook %s: %v", hookID, err)
		respond(w, errDenied)
		return
	}

	// AN EMPTY SECRET IS A 500, NEVER A SKIPPED MAC. It is a hook this cloud
	// registered and cannot check, which is the operator's to see; forge refuses
	// it too, but its refusal reads as a delivery that did not verify.
	if len(hook.GetSecret()) == 0 {
		s.log("hook %s has no secret; refusing to accept an unauthenticated push", hookID)
		respond(w, problem(http.StatusInternalServerError, "hook is not configured"))
		return
	}

	// THE BYTES ARE AUTHENTICATED, AND ONLY THEN INTERPRETED, by the forge that
	// serves the hook's host, under the scheme stored WITH the hook. Verify also
	// holds the check v1 does not have: the signature was made with a secret the
	// URL chose, while the repository is named by the BODY, so a push is only
	// ever for the hook's own repository — otherwise a signature under any hook
	// this cloud has registered would authorize a job for any repository it has.
	event, err := s.deliveries.Verify(hook, r.Header, body)
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
		s.log("hook %s: %v", hookID, err)
		respond(w, problem(http.StatusForbidden, "delivery does not verify"))
		return
	case err != nil:
		s.log("hook %s cannot be verified here: %v", hookID, err)
		respond(w, problem(http.StatusInternalServerError, "hook is not configured"))
		return
	}

	// Branch eligibility, before Enqueue and nowhere else (push.go).
	if !s.eligible(event.Ref) {
		respond(w, ignored("ref is not built"))
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
