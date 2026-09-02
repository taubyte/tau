package frontend

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
)

// errors.go is this role's one code table (service-conventions.md §1), and it
// is where the boundary is actually spelled: everything this package answers to
// the internet goes through respond, so what a stranger can learn from a status
// code is decided in one place rather than re-guessed per route.

// httpError is an answer this package decided on, as opposed to one it is
// translating from the backend. The message is written for the caller and never
// contains anything the caller sent — an error that echoes its input is a
// reflection, and reflections are how a 404 turns into a search engine.
type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return fmt.Sprintf("%d %s", e.status, e.msg) }

func problem(status int, msg string) *httpError { return &httpError{status: status, msg: msg} }

var (
	// errUnauthorized is a missing or malformed credential: nothing was proven,
	// so nothing was decided.
	errUnauthorized = problem(http.StatusUnauthorized, "unauthorized")

	// errDenied IS A 404, and that is the point. A 403 on /job/{jid} tells a
	// caller that job exists, which turns the route into the id oracle the
	// authorization exists to close. services/auth/github.go's own
	// authorizeProject words its denial as a missing project for the same
	// reason: "telling the caller a project exists but isn't theirs turns the
	// route back into the id oracle this check exists to close".
	errDenied = problem(http.StatusNotFound, "not found")

	// errUpstream is anything this process could not get an answer to. It never
	// carries the upstream's own words: a stranger learns that this cloud is
	// unhappy, not which of its services is.
	errUpstream = problem(http.StatusBadGateway, "upstream failure")
)

// respond writes err as a status and a small json body.
//
// A DECIDED ANSWER WINS OVER A TRANSLATED ONE: httpError is checked first, so a
// route that already concluded "not found" is not overridden by whatever code
// the call underneath happened to carry.
func respond(w http.ResponseWriter, err error) {
	var decided *httpError
	if errors.As(err, &decided) {
		// A 202 is not a failure and must not carry an "error" body: it is how
		// this role says "genuine delivery, nothing to build" to a provider
		// that would otherwise redeliver it.
		if decided.status == http.StatusAccepted {
			accepted(w, decided.msg)
			return
		}
		writeJSON(w, decided.status, map[string]string{"error": decided.msg})
		return
	}
	status, msg := translate(err)
	writeJSON(w, status, map[string]string{"error": msg})
}

// translate maps a connect code from the backend or from auth onto a status.
//
// connect.CodeOf answers CodeUnknown for anything that is not a connect error,
// including services/build/clients' ErrNoBackend — which wraps the last peer's
// error with %w, so the Unavailable underneath is still found and still becomes
// a 503 rather than a 502.
func translate(err error) (int, string) {
	switch connect.CodeOf(err) {
	case connect.CodeNotFound, connect.CodePermissionDenied:
		// PermissionDenied comes from the backend's own Authorizer and means
		// THIS NODE may not ask — a deployment fault, not the caller's. It is
		// still answered as a 404, because the alternative tells a stranger
		// that the thing they named exists.
		return errDenied.status, errDenied.msg
	case connect.CodeInvalidArgument:
		// The caller sent something that is not an id. It already knows that,
		// so saying so leaks nothing and saves it a retry.
		return http.StatusBadRequest, "bad request"
	case connect.CodeFailedPrecondition:
		// The state is wrong for the operation: retrying a running job,
		// cancelling a finished one.
		return http.StatusConflict, "conflict"
	case connect.CodeResourceExhausted:
		// Admission control at the backend. A build was NOT refused, it was
		// not accepted yet, so the honest answer is "come back".
		return http.StatusServiceUnavailable, "busy"
	case connect.CodeUnavailable:
		// Every backend peer said ask somebody else, or there were none.
		return http.StatusServiceUnavailable, "no backend available"
	case connect.CodeDeadlineExceeded:
		return http.StatusGatewayTimeout, "upstream timeout"
	default:
		return errUpstream.status, errUpstream.msg
	}
}

// writeJSON is the only place this package writes a response body, apart from
// the streamed job listing.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	// Nothing this package serves is worth a heuristic guess at its type, and a
	// sniffed content type on an error body is how a json 404 becomes html a
	// browser will render.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// accepted is the webhook's whole answer: a status and NOTHING ELSE.
//
// v1 returns the entire Job — id included — to whoever posted the webhook
// (services/patrick/github.go returns newJob), and its unauthenticated
// /download route is keyed by that id. A verified webhook proves that somebody
// holds a hook secret; it does not make them a reader of this cloud's state.
func accepted(w http.ResponseWriter, reason string) {
	if reason != "" {
		w.Header().Set("X-Build-Ignored", reason)
	}
	w.WriteHeader(http.StatusAccepted)
}
