package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/project"
	"github.com/taubyte/tau/pkg/register"

	workerv1 "github.com/taubyte/tau/pkg/proto/gen/tau/worker/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/worker/v1/workerv1connect"
)

// client.go is the other side of rpc.go, and it exists for the same reason the
// register's does: so a caller holds one interface and one error path whether
// the worker is on this node or another.
//
// A gateway is the caller that matters. It has to tell "this deployment is
// missing a piece" from "this node cannot do it right now", because the first
// is an answer to the client and the second is a reason to ask another node.

// Client calls a worker on another peer.
type Client struct {
	conn *rpc.Client
	svc  workerv1connect.WorkerServiceClient
}

// NewClient prepares calls to the worker on p. Nothing is dialled until the
// first call, and that call's context governs the dial.
func NewClient(h host.Host, p libp2ppeer.ID) (*Client, error) {
	conn, err := rpc.NewClient(h, p, Protocol)
	if err != nil {
		return nil, fmt.Errorf("worker: dialling %s: %w", p, err)
	}
	return &Client{conn: conn, svc: workerv1connect.NewWorkerServiceClient(conn.HTTP(), conn.URL())}, nil
}

// Close releases the stream. Calls after it fail.
func (c *Client) Close() { c.conn.Close() }

// Invoke runs one request against one resource.
func (c *Client) Invoke(ctx context.Context, t project.Address, req *invoke.Request) (*invoke.Response, error) {
	if req == nil {
		req = &invoke.Request{}
	}

	res, err := c.svc.Invoke(ctx, connect.NewRequest(&workerv1.InvokeRequest{
		Target: &workerv1.Target{
			Project: t.Project, Environment: t.Environment,
			Scope: t.Scope, Kind: t.Kind, Name: t.Name,
		},
		Method:  req.Method,
		Path:    req.Path,
		Headers: toWire(req.Headers),
		Body:    req.Body,
	}))
	if err != nil {
		return nil, unwire(err)
	}

	return &invoke.Response{
		Status:  int(res.Msg.GetStatus()),
		Headers: fromWire(res.Msg.GetHeaders()),
		Body:    res.Msg.GetBody(),
	}, nil
}

// Transform runs one request through a transformer on whichever node holds the
// deployment, and answers with what it decided.
func (c *Client) Transform(ctx context.Context, t project.Address, req *invoke.Request) (*invoke.Outcome, error) {
	if req == nil {
		req = &invoke.Request{}
	}

	res, err := c.svc.Transform(ctx, connect.NewRequest(&workerv1.TransformRequest{
		Target: &workerv1.Target{
			Project: t.Project, Environment: t.Environment,
			Scope: t.Scope, Kind: t.Kind, Name: t.Name,
		},
		Method:  req.Method,
		Path:    req.Path,
		Headers: toWire(req.Headers),
		Body:    req.Body,
	}))
	if err != nil {
		return nil, unwire(err)
	}

	switch out := res.Msg.GetOutcome().(type) {
	case *workerv1.TransformResponse_Answer:
		return &invoke.Outcome{Answer: &invoke.Response{
			Status:  int(out.Answer.GetStatus()),
			Headers: fromWire(out.Answer.GetHeaders()),
			Body:    out.Answer.GetBody(),
		}}, nil

	case *workerv1.TransformResponse_Forward:
		return &invoke.Outcome{Forward: &invoke.Request{
			Method:  out.Forward.GetMethod(),
			Path:    out.Forward.GetPath(),
			Headers: fromWire(out.Forward.GetHeaders()),
			Body:    out.Forward.GetBody(),
		}}, nil
	}

	return nil, ErrNoOutcome
}

// Route asks a router, on whichever node holds the deployment, where one
// request should go.
func (c *Client) Route(ctx context.Context, t project.Address, req *invoke.Request) (*invoke.Routing, error) {
	if req == nil {
		req = &invoke.Request{}
	}

	res, err := c.svc.Route(ctx, connect.NewRequest(&workerv1.RouteRequest{
		Target: &workerv1.Target{
			Project: t.Project, Environment: t.Environment,
			Scope: t.Scope, Kind: t.Kind, Name: t.Name,
		},
		Method:  req.Method,
		Path:    req.Path,
		Headers: toWire(req.Headers),
		Body:    req.Body,
	}))
	if err != nil {
		return nil, unwire(err)
	}

	switch d := res.Msg.GetDecision().(type) {
	case *workerv1.RouteResponse_To:
		return &invoke.Routing{To: &invoke.Destination{
			Kind: d.To.GetKind(), Namespace: d.To.GetNamespace(), Name: d.To.GetName(),
		}}, nil

	case *workerv1.RouteResponse_Answer:
		return &invoke.Routing{Answer: &invoke.Response{
			Status:  int(d.Answer.GetStatus()),
			Headers: fromWire(d.Answer.GetHeaders()),
			Body:    d.Answer.GetBody(),
		}}, nil

	case *workerv1.RouteResponse_None:
		return &invoke.Routing{None: d.None}, nil
	}

	return nil, ErrNoOutcome
}

// unwire turns a status back into the sentinel a local worker would return.
//
// IT MATCHES ON THE MESSAGE AS WELL AS THE CODE, because several sentinels
// share a code and they tell a caller different things: NotFound is both "no
// such environment" and "no such resource in it", and FailedPrecondition covers
// four separate missing pieces.
func unwire(err error) error {
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return err
	}

	candidates := map[connect.Code][]error{
		connect.CodeInvalidArgument:    {ErrNoTarget},
		connect.CodeInternal:           {ErrNoOutcome},
		connect.CodeNotFound:           {register.ErrNotPublished, register.ErrNoObject, project.ErrNotFound},
		connect.CodeFailedPrecondition: {ErrNoRuntime, ErrAmbiguous, ErrNoArtifact, ErrNoSecrets, invoke.ErrNoRuntime},
		connect.CodeResourceExhausted:  {invoke.ErrAtCapacity},
		connect.CodeDataLoss:           {register.ErrMalformedPointer, project.ErrBadIndex, project.ErrNoObject, project.ErrNoID},
	}

	for _, c := range candidates[cerr.Code()] {
		if strings.HasPrefix(cerr.Message(), c.Error()) {
			return fmt.Errorf("%w: %s", c, cerr.Message())
		}
	}
	return err
}
