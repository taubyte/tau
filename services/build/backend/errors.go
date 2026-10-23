package backend

import (
	"fmt"
	"regexp"

	"connectrpc.com/connect"
)

// errors.go is the one code table (service-conventions.md §1): every
// connect.NewError in this package goes through one of these, so the mapping
// from a Go failure to a wire status is decided in one place rather than
// re-guessed per handler.

func errInvalidArgument(format string, args ...any) error {
	return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(format, args...))
}

func errNotFound(format string, args ...any) error {
	return connect.NewError(connect.CodeNotFound, fmt.Errorf(format, args...))
}

func errFailedPrecondition(format string, args ...any) error {
	return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(format, args...))
}

func errPermissionDenied(format string, args ...any) error {
	return connect.NewError(connect.CodePermissionDenied, fmt.Errorf(format, args...))
}

func errUnauthenticated(err error) error {
	return connect.NewError(connect.CodeUnauthenticated, err)
}

func errResourceExhausted(format string, args ...any) error {
	return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf(format, args...))
}

func errInternal(format string, args ...any) error {
	return connect.NewError(connect.CodeInternal, fmt.Errorf(format, args...))
}

// errUnavailable is "ask somebody else" — the answer a follower gives to every
// RPC in this service (leader.go), and the one code services/build/clients'
// Pool.Do walks past to the next peer.
func errUnavailable(format string, args ...any) error {
	return connect.NewError(connect.CodeUnavailable, fmt.Errorf(format, args...))
}

// A job id is 64 lowercase hex characters — the whole of a sha256 and
// nothing else (jobid.go).
var jobIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// requireJobID refuses a caller-supplied id that this service could not have
// minted, BEFORE it reaches a key builder or a single store read.
//
// The id is derived, so it is guessable, and it is therefore never a bearer
// token: every JobService call authorizes separately. What this closes is the
// other half — an id is a KEY SEGMENT, and store.go's grammar is stated over
// ids that are exactly one segment. hex cannot contain "/"
// and cannot be "." or "..", and a fixed length means no id can be a prefix of
// another, so no listing under one job's prefix can reach another's rows.
// The encoding in store.go holds that for any input; this holds it for the
// SHAPE, so a malformed id is an InvalidArgument at the edge rather than a
// lookup that quietly finds nothing.
func requireJobID(id string) error {
	if id == "" {
		return errInvalidArgument("job id is required")
	}
	if !jobIDPattern.MatchString(id) {
		return errInvalidArgument("%q is not a job id", id)
	}
	return nil
}
