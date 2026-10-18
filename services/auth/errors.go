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

// requireID refuses an empty id before it can reach a row builder.
//
// store.go's grammar is stated over NON-EMPTY ids: an id is exactly one
// segment, so every boundary sits at a fixed segment index. The empty
// string encodes to the empty segment, and go-datastore then deletes that
// segment outright — every key goes through ds.NewKey, which cleans "//"
// away (pkg/kvdb/methods.go). One segment removed is one boundary moved,
// which is the entire class store.go exists to keep out. Refusing it at the
// edge keeps the grammar's proof one line long, instead of a row-by-row
// argument about which of the shortened keys happens not to collide today.
func requireID(name, id string) error {
	if id == "" {
		return errInvalidArgument("%s is required", name)
	}
	return nil
}
