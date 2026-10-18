package database

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/database"
	kvengine "github.com/taubyte/tau/pkg/database/engines/kv"
	databasev2 "github.com/taubyte/tau/pkg/proto/gen/tau/database/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/database/v2/databasev2connect"
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
	mux.Handle(databasev2connect.NewDatabaseServiceHandler(&handler{s}))
	return mux
}

type handler struct{ s *Service }

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

// KVList pages the keys under a prefix.
//
// THE ENGINE ANSWERS THE WHOLE PREFIX and the page is taken here, which is a
// cost this service pays rather than the caller: pkg/database's KVConn has no
// paging, and inventing one per engine is a bigger change than this call is
// worth today. The wire shape already carries `after` and `more`, so the day an
// engine pages, nothing above changes.
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

// answerHeader marks a refusal as this service's own answer.
//
// WHAT IT SEPARATES IS AN ANSWER FROM A SILENCE, which no status code can: a
// node that refuses a ledger and a node that is not there both reach the caller
// as Unavailable, and the caller has to walk on from one and stop at the other.
// A voter that ANSWERS ends the walk even when the answer is a refusal — that
// is the group talking, and the next voter would only say it again.
//
// A HEADER AND NOT A CODE, because the code is the instruction and this is
// about who produced it. p2p/rpc already carries error metadata end to end.
const answerHeader = "Tau-Database-Answer"

// answered puts this service's mark on a refusal it wrote. See refusal().
func answered(err error) error {
	var status *connect.Error
	if errors.As(err, &status) {
		status.Meta().Set(answerHeader, "1")
	}
	return err
}

// code is the one status table: every connect.NewError in this package goes
// through it, so the mapping from a failure to a wire status is decided once.
//
// THE DEFAULT IS Unavailable AND NOT Internal, for the reason services/register
// gives: everything not named below came out of an engine or a store, and that
// is the caller's cue to retry somewhere else. Internal says "stop, this
// request is broken", which for a replicated store is the wrong instruction.
func code(err error) error {
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
	// rather than papering over with a string match, which is how v1's substrate
	// cache ended up whitelisting a sentence nothing had produced in months.
	case errors.Is(err, ErrNotSet), errors.Is(err, kvengine.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)

	case errors.Is(err, ErrTooManyResident):
		return connect.NewError(connect.CodeResourceExhausted, err)

	// A NODE MID-RELEASE IS UNAVAILABLE FOR THAT DATABASE AND NOT BROKEN. It
	// lands on the default's code and is named anyway: the default is about
	// what came out of an engine, and this is a decision this service made.
	case errors.Is(err, ErrReleasing):
		return connect.NewError(connect.CodeUnavailable, err)

	// A RAFT DATABASE THIS NODE CANNOT ANSWER FOR RIGHT NOW is the same kind of
	// sentence and is named for the same reason: a group that has not formed, a
	// node that does not lead one, and a node that is not in one are three
	// facts about WHERE the answer is, not about whether there is one. The
	// caller retries and the gateway places the next call somewhere else.
	case errors.Is(err, ErrGroupForming), errors.Is(err, ErrNotLeading), errors.Is(err, ErrNotHere):
		return connect.NewError(connect.CodeUnavailable, err)

	default:
		return connect.NewError(connect.CodeUnavailable, err)
	}
}

// KVDigest is what this node holds for one database, as one comparable value.
//
// IT DOES NOT OPEN. A node that answered by opening the database would become a
// holder BECAUSE IT WAS ASKED, and would then answer a digest that looks
// converged — which is how a node placement never chose ends up authorising
// another node's drop. Not held is an answer, not a miss.
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

	// KEEPS IS READ AFTER THE OPEN, not before it. Serving a database records
	// that this node holds it, so a keeps taken beforehand answers false for
	// the first ask about every database — and the first ask is the one a
	// departing node makes.
	return connect.NewResponse(&databasev2.KVDigestValue{
		Keys: keys, Digest: sum, Held: true, Keeps: h.s.own.keeps(at.key()),
	}), nil
}

// Hold is KVDigest without the digest: whether this node has a database and
// means to keep it. It is what the repair sweep asks of co-holders, which needs
// presence rather than proof of convergence.
func (h *handler) Hold(ctx context.Context, req *connect.Request[databasev2.Ref]) (*connect.Response[databasev2.Holding], error) {
	holding, err := h.holding(ctx, addressOf(req.Msg))
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(holding), nil
}

// holding answers both without opening anything.
//
// KEEPS IS THIS NODE'S OWN ANSWER and never a report of somebody else's: it is
// what this node has decided about its own copy, under the same lock as its own
// release. That is what makes it something a peer may rely on — a node that
// says it keeps a database will not let go without its own gated release.
func (h *handler) holding(ctx context.Context, at address) (*databasev2.Holding, error) {
	declaration, err := h.s.declared(ctx, at)
	if err != nil {
		return nil, err
	}

	// A RAFT DATABASE IS HELD BY ITS GROUP'S VOTERS, and a node becomes one by
	// opening the group rather than by having bytes on a disk. Holdings answers
	// about the replicated store, which a ledger does not live in, so it would
	// answer no about every ledger in the cloud.
	if declaration.spec.Consensus == database.Raft {
		return h.s.voting(ctx, at, declaration), nil
	}

	if h.s.holdings == nil || !h.s.holdings.Held(ctx, declaration.spec) {
		return &databasev2.Holding{}, nil
	}
	return &databasev2.Holding{Held: true, Keeps: h.s.own.keeps(at.key())}, nil
}
