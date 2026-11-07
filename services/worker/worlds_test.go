package worker

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"

	workerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/worker/v2"
)

// The four worlds a gateway and a proxy reach a worker through: transformer, router,
// proxy router, proxy transformer. Each arm crosses the wire, a malformed answer is
// refused, and a function built for another world is refused at the resolve.

// The fake tier answers each world from what it was handed, so a test can steer
// it to every arm. A path, a peek or a chunk naming an arm picks that arm;
// `neither` picks none, which is the malformed answer; `fail` is a tier error.

func (i *fakeInstance) Transform(_ context.Context, req *invoke.Request) (*invoke.Outcome, error) {
	switch req.Path {
	case "/answer":
		return &invoke.Outcome{Answer: &invoke.Response{Status: 418, Headers: map[string][]string{"X-Seen": {req.Method}}, Body: req.Body}}, nil
	case "/neither":
		return &invoke.Outcome{}, nil
	case "/fail":
		return nil, errors.New("the transformer trapped")
	}
	return &invoke.Outcome{Forward: &invoke.Request{Method: req.Method, Path: req.Path + "/shaped", Headers: req.Headers, Body: req.Body}}, nil
}

func (i *fakeInstance) Route(_ context.Context, req *invoke.Request) (*invoke.Routing, error) {
	switch req.Path {
	case "/answer":
		return &invoke.Routing{Answer: &invoke.Response{Status: 302, Headers: map[string][]string{"Location": {"/elsewhere"}}}}, nil
	case "/none":
		return &invoke.Routing{None: "nothing here answers " + req.Path}, nil
	case "/neither":
		return &invoke.Routing{}, nil
	case "/fail":
		return nil, errors.New("the router trapped")
	}
	return &invoke.Routing{To: &invoke.Destination{Kind: "function", Namespace: "/shop", Name: "checkout"}}, nil
}

func (i *fakeInstance) Direct(_ context.Context, conn *invoke.Connection, peeked []byte) (*invoke.Direction, error) {
	switch string(peeked) {
	case "more":
		return &invoke.Direction{More: 8}, nil
	case "refuse":
		return &invoke.Direction{Refused: "not from " + conn.Source}, nil
	case "neither":
		return &invoke.Direction{}, nil
	case "fail":
		return nil, errors.New("the proxy router trapped")
	}
	return &invoke.Direction{To: &invoke.Endpoint{Kind: "service", Namespace: "/shop", Name: "db", Port: conn.Host}}, nil
}

func (i *fakeInstance) Shape(_ context.Context, s *invoke.Stream) (*invoke.Shaping, error) {
	switch string(s.Chunk) {
	case "close":
		side := "inbound"
		if s.Way == invoke.Outbound {
			side = "outbound"
		}
		return &invoke.Shaping{Refused: "closed on the " + side + " side"}, nil
	case "neither":
		return &invoke.Shaping{}, nil
	case "fail":
		return nil, errors.New("the proxy transformer trapped")
	}
	return &invoke.Shaping{Pass: &invoke.Shaped{Out: append([]byte(s.Conn.ServerName+":"), s.Chunk...), State: append(s.State, '+')}}, nil
}

// worldsHarness is a worker over a project holding one function per world, and a
// handler to name where a world was wanted.
func worldsHarness(t *testing.T) (*harness, *Client) {
	t.Helper()
	fn := func(id, world, call string) string {
		return "id: " + id + "\npackage:\n    name: code\nimplements: " + world + "\nexecution:\n    call: " + call + "\n    timeout: 5s\n    memory: 16MB\n"
	}
	h := build(t, Config{}, resolvers()...)
	h.Register.Register = publishedDocs(t, map[string]string{
		"/package/code.yaml":    "id: QmQ21mKigzmJBeoPNZkjkM4cxMyMjDx7PF9JqDrmWZoP3L\ntype: wasm\n",
		"/function/redact.yaml": fn("QmQ21mKigzmJBeoPNZkjkM4cxMyMjDx7PF9JqDrmWZoP3K", "gateway-transformer", "transform"),
		"/function/edge.yaml":   fn("QmZEDwPfbUF9WAui2sFGQJZyYucnV3zfuUHW8FahdJQB2B", "gateway-router", "route"),
		"/function/picker.yaml": fn("Qmc18Dcz6pUAGJqrUZTYjRia5RReCWQByKq4L73VioUfSw", "proxy-router", "direct"),
		"/function/shaper.yaml": fn("QmcMwon2o4JQSgWXcaEw6AjViyDiCCTJKyTmSxX93V985J", "proxy-transformer", "shape"),
		"/function/plain.yaml":  fn("QmZBUW9eWreBhsF8ct6FXVhXtRg6gx6V1uWsqwSVYyhY7W", "handler", "handle"),
	})
	return h, newWorkerClient(t, h)
}

func fn(name string) specs.Address {
	return specs.Address{Project: "shop", Environment: "prod", Kind: specs.KindFunction, Name: name}
}

func TestATransformerForwardsOrAnswersOverTheWire(t *testing.T) {
	_, c := worldsHarness(t)

	out, err := c.Transform(t.Context(), fn("redact"), &invoke.Request{
		Method: "POST", Path: "/orders", Headers: map[string][]string{"X-Trace": {"abc"}}, Body: []byte("order"),
	})
	require.NoError(t, err)
	require.Nil(t, out.Answer)
	require.Equal(t, &invoke.Request{Method: "POST", Path: "/orders/shaped", Headers: map[string][]string{"X-Trace": {"abc"}}, Body: []byte("order")}, out.Forward)

	out, err = c.Transform(t.Context(), fn("redact"), &invoke.Request{Method: "GET", Path: "/answer", Body: []byte("no")})
	require.NoError(t, err)
	require.Nil(t, out.Forward)
	require.Equal(t, 418, out.Answer.Status)
	require.Equal(t, []string{"GET"}, out.Answer.Headers["X-Seen"])
	require.Equal(t, []byte("no"), out.Answer.Body)

	_, err = c.Transform(t.Context(), fn("redact"), &invoke.Request{Path: "/neither"})
	require.ErrorIs(t, err, ErrNoOutcome, "neither arm is a malformed answer, not a default")

	_, err = c.Transform(t.Context(), fn("redact"), &invoke.Request{Path: "/fail"})
	require.ErrorContains(t, err, "the transformer trapped")

	_, err = c.Transform(t.Context(), fn("plain"), nil)
	require.ErrorIs(t, err, ErrNoTarget, "a handler named as a transformer is refused at the resolve")
	require.ErrorContains(t, err, `plain implements "handler" rather than a transformer`)

	_, err = c.Transform(t.Context(), fn("nope"), nil)
	require.ErrorIs(t, err, specs.ErrNotFound)
}

func TestARouterDecidesOverTheWire(t *testing.T) {
	_, c := worldsHarness(t)

	out, err := c.Route(t.Context(), fn("edge"), &invoke.Request{Method: "GET", Path: "/orders"})
	require.NoError(t, err)
	require.Equal(t, &invoke.Routing{To: &invoke.Destination{Kind: "function", Namespace: "/shop", Name: "checkout"}}, out)

	out, err = c.Route(t.Context(), fn("edge"), &invoke.Request{Path: "/answer"})
	require.NoError(t, err)
	require.Equal(t, 302, out.Answer.Status)
	require.Equal(t, []string{"/elsewhere"}, out.Answer.Headers["Location"])

	out, err = c.Route(t.Context(), fn("edge"), &invoke.Request{Path: "/none"})
	require.NoError(t, err)
	require.Equal(t, &invoke.Routing{None: "nothing here answers /none"}, out, "none is a decision, not a failure")

	_, err = c.Route(t.Context(), fn("edge"), &invoke.Request{Path: "/neither"})
	require.ErrorIs(t, err, ErrNoOutcome)

	_, err = c.Route(t.Context(), fn("edge"), &invoke.Request{Path: "/fail"})
	require.ErrorContains(t, err, "the router trapped")

	_, err = c.Route(t.Context(), fn("redact"), nil)
	require.ErrorIs(t, err, ErrNoTarget)
	require.ErrorContains(t, err, `redact implements "gateway-transformer" rather than a router`)

	_, err = c.Route(t.Context(), fn("nope"), nil)
	require.ErrorIs(t, err, specs.ErrNotFound)
}

func TestAProxyRouterDirectsOverTheWire(t *testing.T) {
	_, c := worldsHarness(t)
	conn := &invoke.Connection{Source: "203.0.113.7:5000", Host: "postgres", Protocol: "tcp", Transport: "tcp", ServerName: "db.example.com"}

	out, err := c.Direct(t.Context(), fn("picker"), conn, []byte("hello"))
	require.NoError(t, err)
	require.Equal(t, &invoke.Direction{To: &invoke.Endpoint{Kind: "service", Namespace: "/shop", Name: "db", Port: "postgres"}}, out,
		"the connection crossed the wire: its host came back as the port")

	out, err = c.Direct(t.Context(), fn("picker"), conn, []byte("more"))
	require.NoError(t, err)
	require.Equal(t, &invoke.Direction{More: 8}, out)

	out, err = c.Direct(t.Context(), fn("picker"), conn, []byte("refuse"))
	require.NoError(t, err)
	require.Equal(t, &invoke.Direction{Refused: "not from 203.0.113.7:5000"}, out)

	_, err = c.Direct(t.Context(), fn("picker"), nil, []byte("neither"))
	require.ErrorIs(t, err, ErrNoOutcome)

	_, err = c.Direct(t.Context(), fn("picker"), conn, []byte("fail"))
	require.ErrorContains(t, err, "the proxy router trapped")

	_, err = c.Direct(t.Context(), fn("edge"), conn, nil)
	require.ErrorIs(t, err, ErrNoTarget)
	require.ErrorContains(t, err, `edge implements "gateway-router" rather than a proxy router`)

	_, err = c.Direct(t.Context(), fn("nope"), conn, nil)
	require.ErrorIs(t, err, specs.ErrNotFound)
}

func TestAProxyTransformerShapesOverTheWire(t *testing.T) {
	_, c := worldsHarness(t)
	conn := &invoke.Connection{ServerName: "db.example.com"}

	out, err := c.Shape(t.Context(), fn("shaper"), &invoke.Stream{Conn: conn, Way: invoke.Outbound, State: []byte("s"), Chunk: []byte("row")})
	require.NoError(t, err)
	require.Equal(t, &invoke.Shaping{Pass: &invoke.Shaped{Out: []byte("db.example.com:row"), State: []byte("s+")}}, out)

	out, err = c.Shape(t.Context(), fn("shaper"), &invoke.Stream{Conn: conn, Way: invoke.Outbound, Chunk: []byte("close")})
	require.NoError(t, err)
	require.Equal(t, &invoke.Shaping{Refused: "closed on the outbound side"}, out, "the direction crossed the wire")

	out, err = c.Shape(t.Context(), fn("shaper"), &invoke.Stream{Conn: conn, Chunk: []byte("close"), Last: true})
	require.NoError(t, err)
	require.Equal(t, &invoke.Shaping{Refused: "closed on the inbound side"}, out)

	_, err = c.Shape(t.Context(), fn("shaper"), nil)
	require.NoError(t, err, "an empty stream is the first chunk of nothing, not a panic")

	_, err = c.Shape(t.Context(), fn("shaper"), &invoke.Stream{Chunk: []byte("neither")})
	require.ErrorIs(t, err, ErrNoOutcome)

	_, err = c.Shape(t.Context(), fn("shaper"), &invoke.Stream{Chunk: []byte("fail")})
	require.ErrorContains(t, err, "the proxy transformer trapped")

	_, err = c.Shape(t.Context(), fn("picker"), nil)
	require.ErrorIs(t, err, ErrNoTarget)
	require.ErrorContains(t, err, `picker implements "proxy-router" rather than a proxy transformer`)

	_, err = c.Shape(t.Context(), fn("nope"), nil)
	require.ErrorIs(t, err, specs.ErrNotFound)
}

func TestAWorldCallWithNoTargetIsRefused(t *testing.T) {
	h, _ := worldsHarness(t)
	handler := &handler{s: h.Worker}

	_, err := handler.Transform(t.Context(), connect.NewRequest(&workerv2.TransformRequest{}))
	require.ErrorIs(t, err, ErrNoTarget)
	_, err = handler.Route(t.Context(), connect.NewRequest(&workerv2.RouteRequest{}))
	require.ErrorIs(t, err, ErrNoTarget)
	_, err = handler.Direct(t.Context(), connect.NewRequest(&workerv2.DirectRequest{}))
	require.ErrorIs(t, err, ErrNoTarget)
	_, err = handler.Shape(t.Context(), connect.NewRequest(&workerv2.ShapeRequest{}))
	require.ErrorIs(t, err, ErrNoTarget)
}

func TestANodeAdvertisesWhatItHolds(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, []string{Descriptor.Name}, h.Worker.Serving())
	require.Empty(t, h.Worker.Proxies(t.Context()))

	_, err := h.Worker.Resolve(t.Context(), specs.Address{Project: "shop", Environment: "prod", Kind: specs.KindFunction, Name: "checkout"})
	require.NoError(t, err)

	at := register.Deployment{Project: "shop", Environment: "prod"}
	require.Equal(t, []string{Descriptor.Name, Descriptor.Name + "/" + at.String()}, h.Worker.Serving())

	proxies := h.Worker.Proxies(t.Context())
	require.NotEmpty(t, proxies, "the fixture declares a proxy")
	for _, p := range proxies {
		require.Equal(t, "shop", p.Project)
		require.Equal(t, "prod", p.Environment)
		require.NotEmpty(t, p.Name)
	}
}
