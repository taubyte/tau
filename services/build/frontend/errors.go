package frontend

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/register"
)

// errors.go is this role's one code table: everything answered to the internet
// goes through respond.

// httpError is an answer this package decided on. Its message never echoes the
// caller's input.
type httpError struct {
	status int
	msg    string
	// retryAfter is how long the caller should wait, when this answer knows.
	// Only a rate limit does.
	retryAfter time.Duration
}

func (e *httpError) Error() string { return fmt.Sprintf("%d %s", e.status, e.msg) }

func problem(status int, msg string) *httpError { return &httpError{status: status, msg: msg} }

// tooManyRequests is the answer to a limited caller, with the limiter's refill
// period as Retry-After.
func tooManyRequests(after time.Duration) *httpError {
	return &httpError{
		status:     http.StatusTooManyRequests,
		msg:        "too many requests",
		retryAfter: after,
	}
}

var (
	// errUnauthorized is a missing or malformed credential: nothing was proven,
	// so nothing was decided.
	errUnauthorized = problem(http.StatusUnauthorized, "unauthorized")

	// errDenied is a 404: a 403 would confirm the job exists.
	errDenied = problem(http.StatusNotFound, "not found")

	// errTooLarge is a body that exceeded its cap. It is the caller's to fix
	// and saying so leaks nothing: the limit is a property of the route, not
	// of anything this cloud holds.
	errTooLarge = problem(http.StatusRequestEntityTooLarge, "request body too large")

	// errLogNotKept is a run that ended with no log stored: its node went away
	// before handing it over, or the asset store would not take it. GONE and
	// not empty, so that what a client read of it cannot pass for all of it.
	errLogNotKept = problem(http.StatusGone, "this run's log was not kept")

	// errUpstream is anything this process could not get an answer to. It never
	// carries the upstream's own words: a stranger learns that this cloud is
	// unhappy, not which of its services is.
	errUpstream = problem(http.StatusBadGateway, "upstream failure")
)

// respond writes err as a status and a small json body; a decided answer wins
// over a translated one.
func respond(w http.ResponseWriter, err error) {
	var decided *httpError
	if errors.As(err, &decided) {
		// A 202 carries no error body: it tells a provider not to redeliver.
		if decided.status == http.StatusAccepted {
			accepted(w, decided.msg)
			return
		}
		// Seconds, floored: Retry-After is an integer count and a limiter whose
		// period rounds to zero is one nobody has to wait for.
		if seconds := int(decided.retryAfter.Seconds()); seconds > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
		}
		writeJSON(w, decided.status, map[string]string{"error": decided.msg})
		return
	}
	status, msg := translate(err)
	writeJSON(w, status, map[string]string{"error": msg})
}

// translate maps a connect code onto a status. ErrNoBackend wraps with %w, so its
// Unavailable still becomes a 503.
func translate(err error) (int, string) {
	// A project the register has no record of is the same 404 a denial is.
	if errors.Is(err, register.ErrNotLinked) {
		return errDenied.status, errDenied.msg
	}
	switch connect.CodeOf(err) {
	case connect.CodeNotFound, connect.CodePermissionDenied:
		// PermissionDenied is the backend refusing this node, a deployment fault, still
		// answered 404.
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
	// No sniffing, so a json error body never renders as html.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// accepted is the webhook's whole answer: a status and nothing else. A verified
// webhook proves a hook secret, not read access to this cloud's state.
func accepted(w http.ResponseWriter, reason string) {
	if reason != "" {
		w.Header().Set("X-Build-Ignored", reason)
	}
	w.WriteHeader(http.StatusAccepted)
}
