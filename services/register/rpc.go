package register

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	pkgregister "github.com/taubyte/tau/pkg/register"

	"github.com/taubyte/tau/pkg/proto/codec"
	registerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/register/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/register/v2/registerv2connect"
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
	mux.Handle(registerv2connect.NewRegisterServiceHandler(&handler{s}, codec.Option()))
	return mux
}

type handler struct{ s *Service }

var _ registerv2connect.RegisterServiceHandler = (*handler)(nil)

// Publish stores a compiled project and points an environment at it.
//
// Authorization is not here: any peer that reaches this service can move any
// pointer. Swarm membership is the boundary, enforced by the cloud CA's
// certificate in the libp2p handshake.
func (h *handler) Publish(ctx context.Context, req *connect.Request[registerv2.PublishRequest]) (*connect.Response[registerv2.PublishResponse], error) {
	id, err := h.s.reg.Publish(ctx, pkgregister.Publication{
		Project:     req.Msg.GetProject(),
		Environment: req.Msg.GetEnvironment(),
		Revision:    req.Msg.GetRevision(),
		Compiled:    req.Msg.GetCompiled(),
		Hosts:       req.Msg.GetHosts(),
		Views:       viewsFromWire(req.Msg.GetCut(), req.Msg.GetViews()),
	})
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&registerv2.PublishResponse{Object: string(id)}), nil
}

// Resolve answers which object an environment runs. This is the read on the
// request path, and it returns the pointer rather than the object for the
// reason the schema gives: the pointer is the part that changes.
func (h *handler) Resolve(ctx context.Context, req *connect.Request[registerv2.ResolveRequest]) (*connect.Response[registerv2.Pointer], error) {
	p, err := h.s.reg.Resolve(ctx, req.Msg.GetProject(), req.Msg.GetEnvironment())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&registerv2.Pointer{Object: string(p.Object), Revision: p.Revision}), nil
}

// Object is one compiled project by its content address. A caller makes this
// call once per id and never again.
func (h *handler) Object(ctx context.Context, req *connect.Request[registerv2.ObjectRequest]) (*connect.Response[registerv2.CompiledProject], error) {
	compiled, err := h.s.reg.Object(ctx, pkgregister.ObjectID(req.Msg.GetObject()))
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&registerv2.CompiledProject{Compiled: compiled}), nil
}

// ResolveFleet is the cloud's own document in force.
func (h *handler) ResolveFleet(ctx context.Context, _ *connect.Request[registerv2.FleetRequest]) (*connect.Response[registerv2.Pointer], error) {
	p, err := h.s.reg.ResolveFleet(ctx)
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&registerv2.Pointer{Object: string(p.Object), Revision: p.Revision}), nil
}

// ResolveHost says whose environment answers on a name — the first thing a
// gateway does with a request.
func (h *handler) Environments(ctx context.Context, req *connect.Request[registerv2.ProjectRequest]) (*connect.Response[registerv2.Deployments], error) {
	environments, err := h.s.reg.Environments(ctx, req.Msg.GetProject())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&registerv2.Deployments{Environments: environments}), nil
}

func (h *handler) ResolveHost(ctx context.Context, req *connect.Request[registerv2.HostRequest]) (*connect.Response[registerv2.HostAnswer], error) {
	project, environment, err := h.s.reg.ResolveHost(ctx, req.Msg.GetFqdn())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&registerv2.HostAnswer{Project: project, Environment: environment}), nil
}

// viewsFromWire keeps the one distinction a repeated field cannot: nil is a revision
// published whole, and an empty list one cut for no destination.
func viewsFromWire(cut bool, in []*registerv2.View) []pkgregister.View {
	if !cut {
		return nil
	}
	out := make([]pkgregister.View, 0, len(in))
	for _, v := range in {
		out = append(out, pkgregister.View{Destination: v.GetDestination(), Compiled: v.GetCompiled()})
	}
	return out
}

func viewPointer(p pkgregister.ViewPointer) *registerv2.ViewPointer {
	return &registerv2.ViewPointer{Object: string(p.Object), Revision: p.Revision, Manifest: string(p.Manifest)}
}

// ResolveView answers which object a destination holds of a deployment.
func (h *handler) ResolveView(ctx context.Context, req *connect.Request[registerv2.ViewRequest]) (*connect.Response[registerv2.ViewPointer], error) {
	p, err := h.s.reg.ResolveView(ctx, req.Msg.GetProject(), req.Msg.GetEnvironment(), req.Msg.GetDestination())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(viewPointer(p)), nil
}

// Assigned is every deployment a destination holds a view of now.
func (h *handler) Assigned(ctx context.Context, req *connect.Request[registerv2.DestinationRequest]) (*connect.Response[registerv2.Assignments], error) {
	as, err := h.s.reg.Assigned(ctx, req.Msg.GetDestination())
	if err != nil {
		return nil, wire(err)
	}
	out := &registerv2.Assignments{}
	for _, a := range as {
		out.Assignments = append(out.Assignments, &registerv2.Assignment{Project: a.Project, Environment: a.Environment, View: viewPointer(a.View)})
	}
	return connect.NewResponse(out), nil
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
		errors.Is(err, pkgregister.ErrNoDestination),
		errors.Is(err, pkgregister.ErrNoRepository),
		errors.Is(err, pkgregister.ErrNoClaimant),
		errors.Is(err, pkgregister.ErrNoRole),
		errors.Is(err, pkgregister.ErrNoGrantee),
		errors.Is(err, pkgregister.ErrSeparator):
		return connect.NewError(connect.CodeInvalidArgument, err)

	case errors.Is(err, pkgregister.ErrNotPublished),
		errors.Is(err, pkgregister.ErrNoObject),
		errors.Is(err, pkgregister.ErrNoView),
		errors.Is(err, pkgregister.ErrNoHost),
		errors.Is(err, pkgregister.ErrNotLinked),
		errors.Is(err, pkgregister.ErrNoGrant):
		return connect.NewError(connect.CodeNotFound, err)

	// A name another project holds is a well-formed publish the state refuses,
	// and a retry with the same name will never succeed — which is exactly what
	// AlreadyExists says and FailedPrecondition does not.
	case errors.Is(err, pkgregister.ErrHostClaimed),
		errors.Is(err, pkgregister.ErrProjectClaimed),
		errors.Is(err, pkgregister.ErrRepositoryClaimed):
		return connect.NewError(connect.CodeAlreadyExists, err)

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

// Manifest is a deployment's current revision and every destination it lists.
func (h *handler) Manifest(ctx context.Context, req *connect.Request[registerv2.ResolveRequest]) (*connect.Response[registerv2.DeploymentManifest], error) {
	m, id, err := h.s.reg.Manifest(ctx, req.Msg.GetProject(), req.Msg.GetEnvironment())
	if err != nil {
		return nil, wire(err)
	}
	out := &registerv2.DeploymentManifest{
		Project: m.Project, Environment: m.Environment, Revision: m.Revision,
		Object: string(m.Object), Whole: m.Whole, Views: map[string]string{}, Id: string(id),
	}
	for d, v := range m.Views {
		out.Views[d] = string(v)
	}
	return connect.NewResponse(out), nil
}
