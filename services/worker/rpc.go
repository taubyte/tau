package worker

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/project"
	"github.com/taubyte/tau/pkg/register"

	workerv1 "github.com/taubyte/tau/pkg/proto/gen/tau/worker/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/worker/v1/workerv1connect"
)

// rpc.go is the wire surface: one call, and the mapping from a failure to a
// status a caller can act on.

func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(workerv1connect.NewWorkerServiceHandler(&handler{s}))
	return mux
}

type handler struct{ s *Service }

var _ workerv1connect.WorkerServiceHandler = (*handler)(nil)

func (h *handler) Invoke(ctx context.Context, req *connect.Request[workerv1.InvokeRequest]) (*connect.Response[workerv1.InvokeResponse], error) {
	t := req.Msg.GetTarget()
	if t == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrNoTarget)
	}

	res, err := h.s.Invoke(ctx,
		Target{
			Project:     t.GetProject(),
			Environment: t.GetEnvironment(),
			Scope:       t.GetScope(),
			Kind:        t.GetKind(),
			Name:        t.GetName(),
		},
		&invoke.Request{
			Method:  req.Msg.GetMethod(),
			Path:    req.Msg.GetPath(),
			Headers: fromWire(req.Msg.GetHeaders()),
			Body:    req.Msg.GetBody(),
		})
	if err != nil {
		return nil, wire(err)
	}

	return connect.NewResponse(&workerv1.InvokeResponse{
		Status:  int32(res.Status),
		Headers: toWire(res.Headers),
		Body:    res.Body,
	}), nil
}

func (h *handler) Transform(ctx context.Context, req *connect.Request[workerv1.TransformRequest]) (*connect.Response[workerv1.TransformResponse], error) {
	t := req.Msg.GetTarget()
	if t == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrNoTarget)
	}

	out, err := h.s.Transform(ctx,
		Target{
			Project:     t.GetProject(),
			Environment: t.GetEnvironment(),
			Scope:       t.GetScope(),
			Kind:        t.GetKind(),
			Name:        t.GetName(),
		},
		&invoke.Request{
			Method:  req.Msg.GetMethod(),
			Path:    req.Msg.GetPath(),
			Headers: fromWire(req.Msg.GetHeaders()),
			Body:    req.Msg.GetBody(),
		})
	if err != nil {
		return nil, wire(err)
	}

	switch {
	case out.Answer != nil:
		return connect.NewResponse(&workerv1.TransformResponse{
			Outcome: &workerv1.TransformResponse_Answer{Answer: &workerv1.InvokeResponse{
				Status:  int32(out.Answer.Status),
				Headers: toWire(out.Answer.Headers),
				Body:    out.Answer.Body,
			}},
		}), nil

	case out.Forward != nil:
		return connect.NewResponse(&workerv1.TransformResponse{
			Outcome: &workerv1.TransformResponse_Forward{Forward: &workerv1.ForwardedRequest{
				Method:  out.Forward.Method,
				Path:    out.Forward.Path,
				Headers: toWire(out.Forward.Headers),
				Body:    out.Forward.Body,
			}},
		}), nil
	}

	// AN UNSET oneof IS A MALFORMED ANSWER AND NOT A DEFAULT. Choosing one here
	// would be this handler deciding whether traffic carries on.
	return nil, connect.NewError(connect.CodeInternal, ErrNoOutcome)
}

func (h *handler) Route(ctx context.Context, req *connect.Request[workerv1.RouteRequest]) (*connect.Response[workerv1.RouteResponse], error) {
	t := req.Msg.GetTarget()
	if t == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrNoTarget)
	}

	out, err := h.s.Route(ctx,
		Target{
			Project:     t.GetProject(),
			Environment: t.GetEnvironment(),
			Scope:       t.GetScope(),
			Kind:        t.GetKind(),
			Name:        t.GetName(),
		},
		&invoke.Request{
			Method:  req.Msg.GetMethod(),
			Path:    req.Msg.GetPath(),
			Headers: fromWire(req.Msg.GetHeaders()),
			Body:    req.Msg.GetBody(),
		})
	if err != nil {
		return nil, wire(err)
	}

	switch {
	case out.To != nil:
		return connect.NewResponse(&workerv1.RouteResponse{
			Decision: &workerv1.RouteResponse_To{To: &workerv1.Destination{
				Kind: out.To.Kind, Namespace: out.To.Namespace, Name: out.To.Name,
			}},
		}), nil

	case out.Answer != nil:
		return connect.NewResponse(&workerv1.RouteResponse{
			Decision: &workerv1.RouteResponse_Answer{Answer: &workerv1.InvokeResponse{
				Status:  int32(out.Answer.Status),
				Headers: toWire(out.Answer.Headers),
				Body:    out.Answer.Body,
			}},
		}), nil

	case out.None != "":
		return connect.NewResponse(&workerv1.RouteResponse{
			Decision: &workerv1.RouteResponse_None{None: out.None},
		}), nil
	}

	return nil, connect.NewError(connect.CodeInternal, ErrNoOutcome)
}

func fromWire(in map[string]*workerv1.Headers) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for name, v := range in {
		out[name] = v.GetValues()
	}
	return out
}

func toWire(in map[string][]string) map[string]*workerv1.Headers {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*workerv1.Headers, len(in))
	for name, values := range in {
		out[name] = &workerv1.Headers{Values: values}
	}
	return out
}

// wire is the one code table.
//
// THE INTERESTING DISTINCTION IS "ASK SOMEBODY ELSE" VERSUS "STOP". A target
// that names nothing is the caller's mistake and no other node will answer it
// differently; a register that could not be reached, or a pool at capacity, is
// this node's problem and another node has it or does not. Those are
// NotFound-ish and Unavailable-ish respectively, and a caller that could not
// tell them apart would either retry forever on a typo or give up on a blip.
func wire(err error) error {
	switch {
	case errors.Is(err, ErrNoTarget):
		return connect.NewError(connect.CodeInvalidArgument, err)

	case errors.Is(err, register.ErrNotPublished),
		errors.Is(err, register.ErrNoObject),
		errors.Is(err, project.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)

	// The config selected a tier nothing runs, wants an artifact nobody
	// resolves, or a secret nobody holds. All three are well-formed requests
	// against a deployment that is missing a piece, and none of them is fixed
	// by asking another node.
	case errors.Is(err, ErrNoRuntime),
		errors.Is(err, ErrAmbiguous),
		errors.Is(err, ErrNoArtifact),
		errors.Is(err, ErrNoSecrets),
		errors.Is(err, invoke.ErrNoRuntime):
		return connect.NewError(connect.CodeFailedPrecondition, err)

	case errors.Is(err, invoke.ErrAtCapacity):
		return connect.NewError(connect.CodeResourceExhausted, err)

	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err)

	// A malformed pointer or object is data loss, and it is worth its own code
	// for the same reason it is in the register: it says the store is the
	// broken part rather than the deployment.
	case errors.Is(err, register.ErrMalformedPointer),
		errors.Is(err, project.ErrBadIndex),
		errors.Is(err, project.ErrNoObject),
		errors.Is(err, project.ErrNoID):
		return connect.NewError(connect.CodeDataLoss, err)

	default:
		return connect.NewError(connect.CodeUnavailable, err)
	}
}
