package database

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"connectrpc.com/connect"

	kvengine "github.com/taubyte/tau/pkg/database/engines/kv"
	databasev1 "github.com/taubyte/tau/pkg/proto/gen/tau/database/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/database/v1/databasev1connect"
)

// rpc.go is the whole wire surface: four calls, each one resolution and one
// operation.
//
// NO HANDLER VALIDATES AN ADDRESS. address.valid() is the one check, run where
// a Ref becomes an address. A check repeated here would be a second opinion,
// and the day the two disagreed the answer would depend on which call a caller
// used.

// maxKeys is both the default page and the ceiling on a larger ask. A caller
// that wants everything asks repeatedly; a caller that asks for everything at
// once gets a page and a `more`.
const maxKeys = 512

// mount is the one handler rpc.Listen serves.
func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(databasev1connect.NewDatabaseServiceHandler(&handler{s}))
	return mux
}

type handler struct{ s *Service }

var _ databasev1connect.DatabaseServiceHandler = (*handler)(nil)

func (h *handler) KVGet(ctx context.Context, req *connect.Request[databasev1.KVGetRequest]) (*connect.Response[databasev1.KVValue], error) {
	at := addressOf(req.Msg.GetRef())
	if req.Msg.GetKey() == "" {
		return nil, wire(ErrNoKey)
	}

	kv, err := h.s.kv(ctx, at)
	if err != nil {
		return nil, wire(err)
	}

	value, err := kv.Get(ctx, req.Msg.GetKey())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&databasev1.KVValue{Value: value}), nil
}

// KVPut is an upsert: the vocabulary's kv has no create, so writing twice is
// writing.
func (h *handler) KVPut(ctx context.Context, req *connect.Request[databasev1.KVPutRequest]) (*connect.Response[databasev1.Empty], error) {
	at := addressOf(req.Msg.GetRef())
	if req.Msg.GetKey() == "" {
		return nil, wire(ErrNoKey)
	}

	kv, err := h.s.kv(ctx, at)
	if err != nil {
		return nil, wire(err)
	}
	if err := kv.Put(ctx, req.Msg.GetKey(), req.Msg.GetValue()); err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&databasev1.Empty{}), nil
}

// KVDelete removes one. Deleting what is not there succeeds — the caller wanted
// it gone and it is gone.
func (h *handler) KVDelete(ctx context.Context, req *connect.Request[databasev1.KVDeleteRequest]) (*connect.Response[databasev1.Empty], error) {
	at := addressOf(req.Msg.GetRef())
	if req.Msg.GetKey() == "" {
		return nil, wire(ErrNoKey)
	}

	kv, err := h.s.kv(ctx, at)
	if err != nil {
		return nil, wire(err)
	}
	if err := kv.Delete(ctx, req.Msg.GetKey()); err != nil && !errors.Is(err, kvengine.ErrNotFound) {
		return nil, wire(err)
	}
	return connect.NewResponse(&databasev1.Empty{}), nil
}

// KVList pages the keys under a prefix.
//
// THE ENGINE ANSWERS THE WHOLE PREFIX and the page is taken here, which is a
// cost this service pays rather than the caller: pkg/database's KVConn has no
// paging, and inventing one per engine is a bigger change than this call is
// worth today. The wire shape already carries `after` and `more`, so the day an
// engine pages, nothing above changes.
func (h *handler) KVList(ctx context.Context, req *connect.Request[databasev1.KVListRequest]) (*connect.Response[databasev1.KVKeys], error) {
	at := addressOf(req.Msg.GetRef())

	kv, err := h.s.kv(ctx, at)
	if err != nil {
		return nil, wire(err)
	}

	keys, err := kv.List(ctx, req.Msg.GetPrefix())
	if err != nil {
		return nil, wire(err)
	}
	sort.Strings(keys)

	// AFTER IS A KEY AND NOT AN OFFSET, so a page is stable under concurrent
	// writes: a row inserted before the cursor cannot shift the window.
	if after := req.Msg.GetAfter(); after != "" {
		keys = keys[sort.SearchStrings(keys, after):]
		if len(keys) > 0 && keys[0] == after {
			keys = keys[1:]
		}
	}

	page := int(req.Msg.GetLimit())
	if page <= 0 || page > maxKeys {
		page = maxKeys
	}
	more := len(keys) > page
	if more {
		keys = keys[:page]
	}

	return connect.NewResponse(&databasev1.KVKeys{Keys: keys, More: more}), nil
}

// wire is the one code table: every connect.NewError in this package goes
// through it, so the mapping from a failure to a wire status is decided once.
//
// THE DEFAULT IS Unavailable AND NOT Internal, for the reason services/register
// gives: everything not named below came out of an engine or a store, and that
// is the caller's cue to retry somewhere else. Internal says "stop, this
// request is broken", which for a replicated store is the wrong instruction.
func wire(err error) error {
	switch {
	case errors.Is(err, ErrNoProject), errors.Is(err, ErrNoEnvironment),
		errors.Is(err, ErrNoID), errors.Is(err, ErrNoKey):
		return connect.NewError(connect.CodeInvalidArgument, err)

	case errors.Is(err, ErrNotADatabase), errors.Is(err, ErrWrongModel):
		return connect.NewError(connect.CodeFailedPrecondition, err)

	// UNIMPLEMENTED AND NOT FailedPrecondition, because nothing the caller can
	// change makes the call succeed: this cloud does not serve that model or
	// that consensus, and the fix is a release rather than a retry.
	case errors.Is(err, ErrModelUnserved), errors.Is(err, ErrConsensusUnserved):
		return connect.NewError(connect.CodeUnimplemented, err)

	case errors.Is(err, ErrNotDeclared):
		return connect.NewError(connect.CodeNotFound, err)

	// A MISS IS RECOGNISED BY ITS SENTINEL AND NEVER BY ITS SENTENCE. tau's own
	// kv is the engine named here; a connector that answers a miss with an error
	// of its own falls through to Unavailable until pkg/database carries one
	// shared sentinel for every engine to wrap. That is a gap worth naming
	// rather than papering over with a string match, which is how the substrate
	// cache ended up whitelisting a sentence nothing had produced in months.
	case errors.Is(err, ErrNotSet), errors.Is(err, kvengine.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)

	case errors.Is(err, ErrTooManyResident):
		return connect.NewError(connect.CodeResourceExhausted, err)

	default:
		return connect.NewError(connect.CodeUnavailable, err)
	}
}
