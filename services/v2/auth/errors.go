package auth

import (
	"fmt"

	"connectrpc.com/connect"
)

// errors.go is the one code table (service-conventions.md §1): every
// connect.NewError call in this package goes through one of these, so the
// mapping from a Go failure to a wire status code is decided in one place
// rather than re-guessed per handler.

func errInvalidArgument(format string, args ...any) error {
	return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(format, args...))
}

func errNotFound(format string, args ...any) error {
	return connect.NewError(connect.CodeNotFound, fmt.Errorf(format, args...))
}

func errPermissionDenied(format string, args ...any) error {
	return connect.NewError(connect.CodePermissionDenied, fmt.Errorf(format, args...))
}

func errUnauthenticated(err error) error {
	return connect.NewError(connect.CodeUnauthenticated, err)
}

func errInternal(format string, args ...any) error {
	return connect.NewError(connect.CodeInternal, fmt.Errorf(format, args...))
}

func errUnimplemented(format string, args ...any) error {
	return connect.NewError(connect.CodeUnimplemented, fmt.Errorf(format, args...))
}
