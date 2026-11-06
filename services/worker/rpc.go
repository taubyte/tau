package worker

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"

	"github.com/taubyte/tau/pkg/proto/codec"
	workerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/worker/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/worker/v2/workerv2connect"
)

func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(workerv2connect.NewWorkerServiceHandler(&handler{s}, codec.Option()))
	return mux
}

type handler struct{ s *Service }

var _ workerv2connect.WorkerServiceHandler = (*handler)(nil)

func (h *handler) Invoke(ctx context.Context, req *connect.Request[workerv2.InvokeRequest]) (*connect.Response[workerv2.InvokeResponse], error) {
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

	return connect.NewResponse(&workerv2.InvokeResponse{
		Status:  int32(res.Status),
		Headers: toWire(res.Headers),
		Body:    res.Body,
	}), nil
}

func (h *handler) Transform(ctx context.Context, req *connect.Request[workerv2.TransformRequest]) (*connect.Response[workerv2.TransformResponse], error) {
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
		return connect.NewResponse(&workerv2.TransformResponse{
			Outcome: &workerv2.TransformResponse_Answer{Answer: &workerv2.InvokeResponse{
				Status:  int32(out.Answer.Status),
				Headers: toWire(out.Answer.Headers),
				Body:    out.Answer.Body,
			}},
		}), nil

	case out.Forward != nil:
		return connect.NewResponse(&workerv2.TransformResponse{
			Outcome: &workerv2.TransformResponse_Forward{Forward: &workerv2.ForwardedRequest{
				Method:  out.Forward.Method,
				Path:    out.Forward.Path,
				Headers: toWire(out.Forward.Headers),
				Body:    out.Forward.Body,
			}},
		}), nil
	}

	// An unset oneof is a malformed answer, not a default.
	return nil, connect.NewError(connect.CodeInternal, ErrNoOutcome)
}

func (h *handler) Route(ctx context.Context, req *connect.Request[workerv2.RouteRequest]) (*connect.Response[workerv2.RouteResponse], error) {
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
		return connect.NewResponse(&workerv2.RouteResponse{
			Decision: &workerv2.RouteResponse_To{To: &workerv2.Destination{
				Kind: out.To.Kind, Namespace: out.To.Namespace, Name: out.To.Name,
			}},
		}), nil

	case out.Answer != nil:
		return connect.NewResponse(&workerv2.RouteResponse{
			Decision: &workerv2.RouteResponse_Answer{Answer: &workerv2.InvokeResponse{
				Status:  int32(out.Answer.Status),
				Headers: toWire(out.Answer.Headers),
				Body:    out.Answer.Body,
			}},
		}), nil

	case out.None != "":
		return connect.NewResponse(&workerv2.RouteResponse{
			Decision: &workerv2.RouteResponse_None{None: out.None},
		}), nil
	}

	return nil, connect.NewError(connect.CodeInternal, ErrNoOutcome)
}

func (h *handler) Direct(ctx context.Context, req *connect.Request[workerv2.DirectRequest]) (*connect.Response[workerv2.DirectResponse], error) {
	t := req.Msg.GetTarget()
	if t == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrNoTarget)
	}

	out, err := h.s.Direct(ctx, targetOf(t), connectionOf(req.Msg.GetConnection()), req.Msg.GetPeeked())
	if err != nil {
		return nil, wire(err)
	}

	switch {
	case out.To != nil:
		return connect.NewResponse(&workerv2.DirectResponse{
			Decision: &workerv2.DirectResponse_To{To: &workerv2.Endpoint{
				Kind: out.To.Kind, Namespace: out.To.Namespace, Name: out.To.Name, Port: out.To.Port,
			}},
		}), nil

	case out.More > 0:
		return connect.NewResponse(&workerv2.DirectResponse{
			Decision: &workerv2.DirectResponse_More{More: out.More},
		}), nil

	case out.Refused != "":
		return connect.NewResponse(&workerv2.DirectResponse{
			Decision: &workerv2.DirectResponse_Refuse{Refuse: out.Refused},
		}), nil
	}

	return nil, connect.NewError(connect.CodeInternal, ErrNoOutcome)
}

func (h *handler) Shape(ctx context.Context, req *connect.Request[workerv2.ShapeRequest]) (*connect.Response[workerv2.ShapeResponse], error) {
	t := req.Msg.GetTarget()
	if t == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrNoTarget)
	}

	way := invoke.Inbound
	if req.Msg.GetWay() == workerv2.ShapeRequest_DIRECTION_OUTBOUND {
		way = invoke.Outbound
	}

	out, err := h.s.Shape(ctx, targetOf(t), &invoke.Stream{
		Conn:  connectionOf(req.Msg.GetConnection()),
		Way:   way,
		State: req.Msg.GetState(),
		Chunk: req.Msg.GetChunk(),
		Last:  req.Msg.GetLast(),
	})
	if err != nil {
		return nil, wire(err)
	}

	switch {
	case out.Pass != nil:
		return connect.NewResponse(&workerv2.ShapeResponse{
			Verdict: &workerv2.ShapeResponse_Pass{Pass: &workerv2.Shaped{
				Out: out.Pass.Out, State: out.Pass.State,
			}},
		}), nil

	case out.Refused != "":
		return connect.NewResponse(&workerv2.ShapeResponse{
			Verdict: &workerv2.ShapeResponse_Close{Close: out.Refused},
		}), nil
	}

	return nil, connect.NewError(connect.CodeInternal, ErrNoOutcome)
}

// targetOf and connectionOf lift the two records every proxy call carries.
func targetOf(t *workerv2.Target) Target {
	return Target{
		Project:     t.GetProject(),
		Environment: t.GetEnvironment(),
		Scope:       t.GetScope(),
		Kind:        t.GetKind(),
		Name:        t.GetName(),
	}
}

func connectionOf(c *workerv2.Connection) *invoke.Connection {
	return &invoke.Connection{
		Source:     c.GetSource(),
		Host:       c.GetHost(),
		Protocol:   c.GetProtocol(),
		Transport:  c.GetTransport(),
		ServerName: c.GetServerName(),
	}
}

func fromWire(in map[string]*workerv2.Headers) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for name, v := range in {
		out[name] = v.GetValues()
	}
	return out
}

func toWire(in map[string][]string) map[string]*workerv2.Headers {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*workerv2.Headers, len(in))
	held := make([]workerv2.Headers, len(in))
	i := 0
	for name, values := range in {
		held[i].Values = values
		out[name] = &held[i]
		i++
	}
	return out
}

// wire is the one code table. A target naming nothing is the caller's mistake (no
// other node answers differently); an unreachable register or a pool at capacity is
// this node's problem. A caller must tell them apart to neither retry a typo forever
// nor give up on a blip.
func wire(err error) error {
	switch {
	case errors.Is(err, ErrNoTarget):
		return connect.NewError(connect.CodeInvalidArgument, err)

	case errors.Is(err, register.ErrNotPublished),
		errors.Is(err, register.ErrNoObject),
		errors.Is(err, specs.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)

	// A deployment missing a piece (tier, artifact or secret); another node will not fix
	// it.
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

	// A malformed pointer or object says the store is broken, not the deployment.
	case errors.Is(err, register.ErrMalformedPointer),
		errors.Is(err, specs.ErrBadIndex),
		errors.Is(err, specs.ErrNoObject),
		errors.Is(err, specs.ErrNoID):
		return connect.NewError(connect.CodeDataLoss, err)

	default:
		return connect.NewError(connect.CodeUnavailable, err)
	}
}
