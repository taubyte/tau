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
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"

	workerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/worker/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/worker/v2/workerv2connect"
)

// Client is the other side of rpc.go, so a caller holds one interface and one error
// path whether the worker is local or remote. A gateway must tell "this deployment
// is missing a piece" (an answer for the client) from "this node cannot do it now"
// (a reason to ask another node).

// Client calls a worker on another peer.
type Client struct {
	conn *rpc.Client
	svc  workerv2connect.WorkerServiceClient
}

// NewClient prepares calls to the worker on p. Nothing is dialled until the
// first call, and that call's context governs the dial.
func NewClient(h host.Host, p libp2ppeer.ID) (*Client, error) {
	conn, err := rpc.NewClient(h, p, Protocol)
	if err != nil {
		return nil, fmt.Errorf("worker: dialling %s: %w", p, err)
	}
	return &Client{conn: conn, svc: workerv2connect.NewWorkerServiceClient(conn.HTTP(), conn.URL())}, nil
}

// Close releases the stream. Calls after it fail.
func (c *Client) Close() { c.conn.Close() }

// Invoke runs one request against one resource.
func (c *Client) Invoke(ctx context.Context, t specs.Address, req *invoke.Request) (*invoke.Response, error) {
	if req == nil {
		req = &invoke.Request{}
	}

	res, err := c.svc.Invoke(ctx, connect.NewRequest(&workerv2.InvokeRequest{
		Target: &workerv2.Target{
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
func (c *Client) Transform(ctx context.Context, t specs.Address, req *invoke.Request) (*invoke.Outcome, error) {
	if req == nil {
		req = &invoke.Request{}
	}

	res, err := c.svc.Transform(ctx, connect.NewRequest(&workerv2.TransformRequest{
		Target: &workerv2.Target{
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
	case *workerv2.TransformResponse_Answer:
		return &invoke.Outcome{Answer: &invoke.Response{
			Status:  int(out.Answer.GetStatus()),
			Headers: fromWire(out.Answer.GetHeaders()),
			Body:    out.Answer.GetBody(),
		}}, nil

	case *workerv2.TransformResponse_Forward:
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
func (c *Client) Route(ctx context.Context, t specs.Address, req *invoke.Request) (*invoke.Routing, error) {
	if req == nil {
		req = &invoke.Request{}
	}

	res, err := c.svc.Route(ctx, connect.NewRequest(&workerv2.RouteRequest{
		Target: &workerv2.Target{
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
	case *workerv2.RouteResponse_To:
		return &invoke.Routing{To: &invoke.Destination{
			Kind: d.To.GetKind(), Namespace: d.To.GetNamespace(), Name: d.To.GetName(),
		}}, nil

	case *workerv2.RouteResponse_Answer:
		return &invoke.Routing{Answer: &invoke.Response{
			Status:  int(d.Answer.GetStatus()),
			Headers: fromWire(d.Answer.GetHeaders()),
			Body:    d.Answer.GetBody(),
		}}, nil

	case *workerv2.RouteResponse_None:
		return &invoke.Routing{None: d.None}, nil
	}

	return nil, ErrNoOutcome
}

// Direct asks a proxy router, on whichever node holds the deployment, where one
// connection should go.
func (c *Client) Direct(ctx context.Context, t specs.Address, conn *invoke.Connection, peeked []byte) (*invoke.Direction, error) {
	res, err := c.svc.Direct(ctx, connect.NewRequest(&workerv2.DirectRequest{
		Target:     targetWire(t),
		Connection: connectionWire(conn),
		Peeked:     peeked,
	}))
	if err != nil {
		return nil, unwire(err)
	}

	switch d := res.Msg.GetDecision().(type) {
	case *workerv2.DirectResponse_To:
		return &invoke.Direction{To: &invoke.Endpoint{
			Kind: d.To.GetKind(), Namespace: d.To.GetNamespace(), Name: d.To.GetName(), Port: d.To.GetPort(),
		}}, nil

	case *workerv2.DirectResponse_More:
		return &invoke.Direction{More: d.More}, nil

	case *workerv2.DirectResponse_Refuse:
		return &invoke.Direction{Refused: d.Refuse}, nil
	}

	return nil, ErrNoOutcome
}

// Shape runs one chunk of a connection through a proxy transformer, on
// whichever node holds the deployment.
func (c *Client) Shape(ctx context.Context, t specs.Address, stream *invoke.Stream) (*invoke.Shaping, error) {
	if stream == nil {
		stream = &invoke.Stream{}
	}

	way := workerv2.ShapeRequest_DIRECTION_INBOUND
	if stream.Way == invoke.Outbound {
		way = workerv2.ShapeRequest_DIRECTION_OUTBOUND
	}

	res, err := c.svc.Shape(ctx, connect.NewRequest(&workerv2.ShapeRequest{
		Target:     targetWire(t),
		Connection: connectionWire(stream.Conn),
		Way:        way,
		State:      stream.State,
		Chunk:      stream.Chunk,
		Last:       stream.Last,
	}))
	if err != nil {
		return nil, unwire(err)
	}

	switch v := res.Msg.GetVerdict().(type) {
	case *workerv2.ShapeResponse_Pass:
		return &invoke.Shaping{Pass: &invoke.Shaped{
			Out: v.Pass.GetOut(), State: v.Pass.GetState(),
		}}, nil

	case *workerv2.ShapeResponse_Close:
		return &invoke.Shaping{Refused: v.Close}, nil
	}

	return nil, ErrNoOutcome
}

func targetWire(t specs.Address) *workerv2.Target {
	return &workerv2.Target{
		Project: t.Project, Environment: t.Environment,
		Scope: t.Scope, Kind: t.Kind, Name: t.Name,
	}
}

func connectionWire(c *invoke.Connection) *workerv2.Connection {
	if c == nil {
		c = &invoke.Connection{}
	}
	return &workerv2.Connection{
		Source:     c.Source,
		Host:       c.Host,
		Protocol:   c.Protocol,
		Transport:  c.Transport,
		ServerName: c.ServerName,
	}
}

// unwire turns a status back into the sentinel a local worker would return. It
// matches the message as well as the code, since several sentinels share a code.
func unwire(err error) error {
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return err
	}

	candidates := map[connect.Code][]error{
		connect.CodeInvalidArgument:    {ErrNoTarget},
		connect.CodeInternal:           {ErrNoOutcome},
		connect.CodeNotFound:           {register.ErrNotPublished, register.ErrNoObject, specs.ErrNotFound},
		connect.CodeFailedPrecondition: {ErrNoRuntime, ErrAmbiguous, ErrNoArtifact, ErrNoSecrets, invoke.ErrNoRuntime},
		connect.CodeResourceExhausted:  {invoke.ErrAtCapacity},
		connect.CodeDataLoss:           {register.ErrMalformedPointer, specs.ErrBadIndex, specs.ErrNoObject, specs.ErrNoID},
	}

	for _, c := range candidates[cerr.Code()] {
		if strings.HasPrefix(cerr.Message(), c.Error()) {
			return fmt.Errorf("%w: %s", c, cerr.Message())
		}
	}
	return err
}
