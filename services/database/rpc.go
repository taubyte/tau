package database

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/database"
	kvengine "github.com/taubyte/tau/pkg/database/engines/kv"
	"github.com/taubyte/tau/pkg/proto/codec"
	databasev2 "github.com/taubyte/tau/pkg/proto/gen/tau/database/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/database/v2/databasev2connect"
)

// The wire surface. No handler validates an address: address.valid() is the one
// check.

// maxKeys is both the default page and the ceiling on a larger ask. A caller
// that wants everything asks repeatedly; a caller that asks for everything at
// once gets a page and a `more`.
const maxKeys = 512

// mount is the one handler rpc.Listen serves.
func (s *server) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(databasev2connect.NewDatabaseServiceHandler(&handler{s}, codec.Option()))
	return mux
}

type handler struct{ s *server }

var _ databasev2connect.DatabaseServiceHandler = (*handler)(nil)

func (h *handler) KVGet(ctx context.Context, req *connect.Request[databasev2.KVGetRequest]) (*connect.Response[databasev2.KVValue], error) {
	at := addressOf(req.Msg.GetRef())
	ctx = hopsIn(ctx, req.Header())
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
	return connect.NewResponse(&databasev2.KVValue{Value: value}), nil
}

// KVPut is an upsert: the vocabulary's kv has no create, so writing twice is
// writing.
func (h *handler) KVPut(ctx context.Context, req *connect.Request[databasev2.KVPutRequest]) (*connect.Response[databasev2.Empty], error) {
	at := addressOf(req.Msg.GetRef())
	ctx = hopsIn(ctx, req.Header())
	if req.Msg.GetKey() == "" {
		return nil, wire(ErrNoKey)
	}

	err := h.s.mutate(ctx, at, func(kv database.KVConn) error {
		return kv.Put(ctx, req.Msg.GetKey(), req.Msg.GetValue())
	})
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&databasev2.Empty{}), nil
}

// KVDelete removes one. Deleting what is not there succeeds — the caller wanted
// it gone and it is gone.
func (h *handler) KVDelete(ctx context.Context, req *connect.Request[databasev2.KVDeleteRequest]) (*connect.Response[databasev2.Empty], error) {
	at := addressOf(req.Msg.GetRef())
	ctx = hopsIn(ctx, req.Header())
	if req.Msg.GetKey() == "" {
		return nil, wire(ErrNoKey)
	}

	err := h.s.mutate(ctx, at, func(kv database.KVConn) error {
		if err := kv.Delete(ctx, req.Msg.GetKey()); err != nil && !errors.Is(err, kvengine.ErrNotFound) {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&databasev2.Empty{}), nil
}

// KVList pages the keys under a prefix. The engine answers the whole prefix and the
// page is taken here, since pkg/database's KVConn has no paging; the wire already
// carries `after` and `more`.
func (h *handler) KVList(ctx context.Context, req *connect.Request[databasev2.KVListRequest]) (*connect.Response[databasev2.KVKeys], error) {
	at := addressOf(req.Msg.GetRef())
	ctx = hopsIn(ctx, req.Header())

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

	return connect.NewResponse(&databasev2.KVKeys{Keys: keys, More: more}), nil
}

// wire is how a failure leaves this service: the one status table, plus the
// mark that says this node wrote it.
func wire(err error) error {
	return answered(code(err))
}

// answerHeader marks a refusal as this service's own answer, separating an answer
// from a silence, which both reach the caller as Unavailable. A header, since the
// code is the instruction and this is about who produced it.
const answerHeader = "Tau-Database-Answer"

// answered puts this service's mark on a refusal it wrote. See refusal().
func answered(err error) error {
	var status *connect.Error
	if errors.As(err, &status) {
		status.Meta().Set(answerHeader, "1")
	}
	return err
}

// code is the one status table. The default is Unavailable, not Internal: what is not
// named came from an engine or store, a cue to retry elsewhere.
func code(err error) error {
	switch {
	case errors.Is(err, ErrNoProject), errors.Is(err, ErrNoEnvironment),
		errors.Is(err, ErrNoID), errors.Is(err, ErrNoKey):
		return connect.NewError(connect.CodeInvalidArgument, err)

	case errors.Is(err, ErrNotADatabase), errors.Is(err, ErrWrongModel):
		return connect.NewError(connect.CodeFailedPrecondition, err)

	// Unimplemented: the fix is a release, not anything the caller can change.
	case errors.Is(err, ErrModelUnserved), errors.Is(err, ErrConsensusUnserved):
		return connect.NewError(connect.CodeUnimplemented, err)

	case errors.Is(err, ErrNotDeclared):
		return connect.NewError(connect.CodeNotFound, err)

	// A miss is recognised by sentinel, never by sentence. A connector with its own miss
	// error falls through to Unavailable until pkg/database has a shared sentinel.
	case errors.Is(err, ErrNotSet), errors.Is(err, kvengine.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)

	case errors.Is(err, ErrTooManyResident):
		return connect.NewError(connect.CodeResourceExhausted, err)

	// Mid-release is unavailable for that database, not broken; named because it is this
	// service's decision.
	case errors.Is(err, ErrReleasing):
		return connect.NewError(connect.CodeUnavailable, err)

	// Facts about where a raft answer is, not whether there is one: the caller retries
	// elsewhere.
	case errors.Is(err, ErrGroupForming), errors.Is(err, ErrNotLeading), errors.Is(err, ErrNotHere):
		return connect.NewError(connect.CodeUnavailable, err)

	default:
		return connect.NewError(connect.CodeUnavailable, err)
	}
}

// KVDigest is what this node holds for one database, as one comparable value. It does
// not open: a node answering by opening would become a holder because it was asked.
// Not held is an answer, not a miss.
func (h *handler) KVDigest(ctx context.Context, req *connect.Request[databasev2.Ref]) (*connect.Response[databasev2.KVDigestValue], error) {
	at := addressOf(req.Msg)

	holding, err := h.holding(ctx, at)
	if err != nil {
		return nil, wire(err)
	}
	if !holding.GetHeld() {
		return connect.NewResponse(&databasev2.KVDigestValue{Keeps: holding.GetKeeps()}), nil
	}

	kv, err := h.s.kv(ctx, at)
	if err != nil {
		return nil, wire(err)
	}
	keys, sum, err := digestOf(ctx, kv)
	if err != nil {
		return nil, wire(err)
	}

	// Keeps is read after the open, since serving records holding.
	return connect.NewResponse(&databasev2.KVDigestValue{
		Keys: keys, Digest: sum, Held: true, Keeps: h.s.own.keeps(at.key()),
	}), nil
}

// Hold is KVDigest without the digest, for the repair sweep.
func (h *handler) Hold(ctx context.Context, req *connect.Request[databasev2.Ref]) (*connect.Response[databasev2.Holding], error) {
	holding, err := h.holding(ctx, addressOf(req.Msg))
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(holding), nil
}

// holding answers both without opening anything. Keeps is this node's own decision,
// made under the lock of its own release.
func (h *handler) holding(ctx context.Context, at address) (*databasev2.Holding, error) {
	declaration, err := h.s.declared(ctx, at)
	if err != nil {
		return nil, err
	}

	// A raft database is held by its voters, not bytes on disk; Holdings would answer no
	// about every ledger.
	if declaration.spec.Consensus == database.Raft {
		return h.s.voting(ctx, at, declaration), nil
	}

	if h.s.holdings == nil || !h.s.holdings.Held(ctx, declaration.spec) {
		return &databasev2.Holding{}, nil
	}
	return &databasev2.Holding{Held: true, Keeps: h.s.own.keeps(at.key())}, nil
}
