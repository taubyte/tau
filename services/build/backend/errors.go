package backend

import (
	"fmt"
	"regexp"

	"connectrpc.com/connect"
)

// The one code table: every connect.NewError in this package goes through these.

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

// requireJobID refuses a caller-supplied id this service could not have minted,
// before any key builder or store read. Fixed-length hex is always exactly one
// key segment and never a prefix of another id. Ids are guessable, so every call
// still authorizes.
func requireJobID(id string) error {
	if id == "" {
		return errInvalidArgument("job id is required")
	}
	if !jobIDPattern.MatchString(id) {
		return errInvalidArgument("%q is not a job id", id)
	}
	return nil
}
