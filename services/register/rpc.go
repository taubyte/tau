package register

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	pkgregister "github.com/taubyte/tau/pkg/register"

	registerv1 "github.com/taubyte/tau/pkg/proto/gen/tau/register/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/register/v1/registerv1connect"
)

// rpc.go is the whole wire surface: three calls, each one line of work and one
// line of error mapping.
//
// NO HANDLER VALIDATES ANYTHING. Names, empty objects, revisions and content
// addresses are all pkg/register's rules, checked there once. A check repeated
// here would be a second opinion, and the day the two disagreed the answer
// would depend on which door the request came through.

// mount is the one handler rpc.Listen serves.
func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(registerv1connect.NewRegisterServiceHandler(&handler{s}))
	return mux
}

type handler struct{ s *Service }

var _ registerv1connect.RegisterServiceHandler = (*handler)(nil)

// Publish stores a compiled project and points an environment at it.
//
// AUTHORIZATION IS NOT HERE, and the omission is stated rather than implied:
// any peer that can reach this service can move any pointer. That is the OSS
// trust model — swarm membership is the boundary and the swarm key is what
// enforces it. See .notes/tee/v2-seam.md.
func (h *handler) Publish(ctx context.Context, req *connect.Request[registerv1.PublishRequest]) (*connect.Response[registerv1.PublishResponse], error) {
	id, err := h.s.reg.Publish(ctx, req.Msg.GetProject(), req.Msg.GetEnvironment(), req.Msg.GetRevision(), req.Msg.GetCompiled())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&registerv1.PublishResponse{Object: string(id)}), nil
}

// Resolve answers which object an environment runs. This is the read on the
// request path, and it returns the pointer rather than the object for the
// reason the schema gives: the pointer is the part that changes.
func (h *handler) Resolve(ctx context.Context, req *connect.Request[registerv1.ResolveRequest]) (*connect.Response[registerv1.Pointer], error) {
	p, err := h.s.reg.Resolve(ctx, req.Msg.GetProject(), req.Msg.GetEnvironment())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&registerv1.Pointer{Object: string(p.Object), Revision: p.Revision}), nil
}

// Object is one compiled project by its content address. A caller makes this
// call once per id and never again.
func (h *handler) Object(ctx context.Context, req *connect.Request[registerv1.ObjectRequest]) (*connect.Response[registerv1.CompiledProject], error) {
	compiled, err := h.s.reg.Object(ctx, pkgregister.ObjectID(req.Msg.GetObject()))
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&registerv1.CompiledProject{Compiled: compiled}), nil
}

// wire is the one code table: every connect.NewError in this package goes
// through it, so the mapping from a register failure to a wire status is
// decided once.
//
// THE DEFAULT IS Unavailable AND NOT Internal. Every error pkg/register can
// produce that is not named below came out of the store, and a store failure is
// the caller's cue to retry somewhere else — Internal says "stop, this request
// is broken", which for a replicated register is the wrong instruction.
func wire(err error) error {
	switch {
	case errors.Is(err, pkgregister.ErrNoProject),
		errors.Is(err, pkgregister.ErrNoEnvironment),
		errors.Is(err, pkgregister.ErrEmptyObject),
		errors.Is(err, pkgregister.ErrSeparator):
		return connect.NewError(connect.CodeInvalidArgument, err)

	case errors.Is(err, pkgregister.ErrNotPublished),
		errors.Is(err, pkgregister.ErrNoObject):
		return connect.NewError(connect.CodeNotFound, err)

	// A revision that does not move forward is a well-formed request the
	// state refuses, which is exactly FailedPrecondition — and crucially not
	// InvalidArgument, because a retry with a higher revision succeeds.
	case errors.Is(err, pkgregister.ErrStaleRevision):
		return connect.NewError(connect.CodeFailedPrecondition, err)

	// A pointer row that is present and unreadable is data loss, and saying so
	// is the whole reason pkg/register does not report it as "not published".
	case errors.Is(err, pkgregister.ErrMalformedPointer):
		return connect.NewError(connect.CodeDataLoss, err)

	default:
		return connect.NewError(connect.CodeUnavailable, err)
	}
}
