package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// harness is a gateway over a real register holding a really-compiled project,
// with a worker that records what it was asked to run.
//
// THE PROJECT IS COMPILED BY tcc. What this service reads is the compiled
// vocabulary — route blocks, handler blocks, cors, rate_limit, transformers —
// so a fixture of my own would be a fixture of what I believe that vocabulary
// produces, and it would keep passing on the day it produced something else.
type harness struct {
	Gateway *Service
	Reg     *register.Register
	Workers *recordingWorkers
	clock   *fakeClock
}

// hosts is the fixture's own names, which is what a publisher would hand the
// register. The generated ones carry the project's portion only: the cloud's
// fqdn is not in the config, and appending it is the node's job.
func hostsOf(t testing.TB, p *specs.Project, cloud string) []string {
	t.Helper()
	var out []string
	for _, h := range p.Hosts() {
		if h.Generated && cloud != "" {
			out = append(out, h.Fqdn+"."+cloud)
			continue
		}
		out = append(out, h.Fqdn)
	}
	return out
}

func newHarness(t testing.TB, cfg Config, opts ...Option) *harness {
	t.Helper()
	return newHarnessOf(t, compiled(t), cfg, opts...)
}

// newHarnessOf is a harness over a compiled project of the test's own.
func newHarnessOf(t testing.TB, bytes []byte, cfg Config, opts ...Option) *harness {
	t.Helper()

	reg, err := register.New(register.WithStore(register.NewMemStore()))
	require.NoError(t, err)

	p, err := specs.Load(bytes)
	require.NoError(t, err)

	_, err = reg.Publish(t.Context(), register.Publication{
		Project: "shop", Environment: "prod", Revision: 1,
		Compiled: bytes, Hosts: hostsOf(t, p, cfg.CloudFqdn),
	})
	require.NoError(t, err)

	workers := &recordingWorkers{}
	clock := &fakeClock{at: time.Unix(1_700_000_000, 0)}

	s, err := New(cfg, append([]Option{
		WithHosts(reg), WithSource(reg), WithWorkers(workers), WithClock(clock.Now),
	}, opts...)...)
	require.NoError(t, err)

	return &harness{Gateway: s, Reg: reg, Workers: workers, clock: clock}
}

// do makes one request against the gateway.
func (h *harness) do(t testing.TB, method, url string, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(method, url, strings.NewReader(body))
	r.RemoteAddr = "203.0.113.7:51000"
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}

	w := httptest.NewRecorder()
	h.Gateway.Handler().ServeHTTP(w, r)
	return w
}

func compiled(t testing.TB) []byte {
	t.Helper()

	c, err := schema.New(
		schema.WithLocal("../../../pkg/tcc/taubyte/v2/fixtures/basic"),
		schema.WithBranch("main"))
	require.NoError(t, err)

	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)

	b, err := json.Marshal(obj.Flat())
	require.NoError(t, err)
	return b
}

// recordingWorkers answers with what it was asked, so a test reads back exactly
// what the route produced.
type recordingWorkers struct {
	mu    sync.Mutex
	calls []call
	err   error
	reply *invoke.Response

	// shaped is what each transformer answers, by function name. A name with no
	// entry forwards the request unchanged, which is what a transformer that
	// decided to change nothing does.
	shaped map[string]*invoke.Outcome
	// shapeErr is a transformer that could not be run, by function name.
	shapeErr map[string]error
	// transformed records every transformer that was asked, in order.
	transformed []call

	// routing is what each router answers, by function name; routeErr is a
	// router that could not be run. routed records every router asked.
	routing  map[string]*invoke.Routing
	routeErr map[string]error
	routed   []call
}

func (w *recordingWorkers) asksRouted() []call {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]call(nil), w.routed...)
}

// Transform stands in for the worker that runs the transformer.
func (w *recordingWorkers) Transform(_ context.Context, at specs.Address, req *invoke.Request) (*invoke.Outcome, error) {
	w.mu.Lock()
	w.transformed = append(w.transformed, call{at: at, req: cloneRequest(req)})
	out, err := w.shaped[at.Name], w.shapeErr[at.Name]
	w.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if out != nil {
		return out, nil
	}
	return &invoke.Outcome{Forward: req}, nil
}

// Route stands in for the worker that runs the router. With no entry it sends
// the request to `function/handled`, which is enough for a test that is about
// something else to get past a router-routed name.
func (w *recordingWorkers) Route(_ context.Context, at specs.Address, req *invoke.Request) (*invoke.Routing, error) {
	w.mu.Lock()
	w.routed = append(w.routed, call{at: at, req: cloneRequest(req)})
	out, err := w.routing[at.Name], w.routeErr[at.Name]
	w.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if out != nil {
		return out, nil
	}
	return &invoke.Routing{To: &invoke.Destination{Kind: "function", Name: "handled"}}, nil
}

func (w *recordingWorkers) asked() []call {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]call(nil), w.transformed...)
}

// cloneRequest snapshots what a transformer was HANDED. The chain mutates the
// request in place as each one forwards, so recording the pointer would record
// what the LAST transformer produced for every entry.
func cloneRequest(req *invoke.Request) *invoke.Request {
	out := *req
	out.Headers = make(map[string][]string, len(req.Headers))
	for k, v := range req.Headers {
		out.Headers[k] = append([]string(nil), v...)
	}
	out.Body = append([]byte(nil), req.Body...)
	return &out
}

type call struct {
	at  specs.Address
	req *invoke.Request

	// port is the named port a request was delivered over, "" for one that
	// was invoked.
	port string
}

func (w *recordingWorkers) Invoke(_ context.Context, at specs.Address, req *invoke.Request) (*invoke.Response, error) {
	return w.served(call{at: at, req: req})
}

// served records one delivery and answers it.
func (w *recordingWorkers) served(c call) (*invoke.Response, error) {
	w.mu.Lock()
	w.calls = append(w.calls, c)
	err, reply := w.err, w.reply
	w.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if reply != nil {
		return reply, nil
	}
	headers := map[string][]string{"X-Served": {c.at.Kind + "/" + c.at.Name}}
	if c.port != "" {
		headers["X-Port"] = []string{c.port}
	}
	return &invoke.Response{Status: http.StatusOK, Headers: headers, Body: []byte(c.req.Path)}, nil
}

// Conn stands in for a connection to a workload's named port: what is spoken
// over it is HTTP, answered the way Invoke answers, and recorded with the
// port it came in on.
func (w *recordingWorkers) Conn(_ context.Context, at specs.Address, port string) (net.Conn, error) {
	w.mu.Lock()
	err := w.err
	w.mu.Unlock()
	if err != nil {
		return nil, err
	}

	client, workload := net.Pipe()
	go func() {
		defer workload.Close()
		hr, err := http.ReadRequest(bufio.NewReader(workload))
		if err != nil {
			return
		}
		body, err := io.ReadAll(hr.Body)
		if err != nil {
			return
		}
		res, err := w.served(call{at: at, port: port, req: &invoke.Request{
			Method: hr.Method, Path: hr.URL.Path, Headers: hr.Header, Body: body,
		}})
		if err != nil {
			return
		}
		out := &http.Response{
			StatusCode: res.Status, ProtoMajor: 1, ProtoMinor: 1,
			Header: res.Headers, Body: io.NopCloser(bytes.NewReader(res.Body)),
			ContentLength: int64(len(res.Body)),
		}
		out.Write(workload)
	}()
	return client, nil
}

func (w *recordingWorkers) seen() []call {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]call(nil), w.calls...)
}

func (w *recordingWorkers) fail(err error) {
	w.mu.Lock()
	w.err = err
	w.mu.Unlock()
}

func (w *recordingWorkers) answer(res *invoke.Response) {
	w.mu.Lock()
	w.reply = res
	w.mu.Unlock()
}

type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

func httptestRequest(method, path, host string) *http.Request {
	r := httptest.NewRequest(method, "http://placeholder"+path, strings.NewReader(""))
	r.Host = host
	r.URL.Host = ""
	r.RemoteAddr = "203.0.113.7:51000"
	return r
}

func recorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }

// countingHosts is the host table with a tally.
type countingHosts struct {
	inner interface {
		ResolveHost(ctx context.Context, fqdn string) (string, string, error)
	}
	mu sync.Mutex
	n  int
}

func (c *countingHosts) ResolveHost(ctx context.Context, fqdn string) (string, string, error) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return c.inner.ResolveHost(ctx, fqdn)
}

func (c *countingHosts) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// brokenHosts is a register that cannot be reached.
type brokenHosts struct{}

func (brokenHosts) ResolveHost(context.Context, string) (string, string, error) {
	return "", "", errUnreachable
}

var errUnreachable = errors.New("the register is unreachable")

// emptySource claims to know a pointer and holds no object for it — what a
// half-replicated publish looks like on a node that has one and not the other.
type emptySource struct{}

func (emptySource) Resolve(context.Context, string, string) (register.Pointer, error) {
	return register.Pointer{Object: "an-object-nobody-has", Revision: 1}, nil
}

func (emptySource) Object(context.Context, register.ObjectID) ([]byte, error) {
	return nil, register.ErrNoObject
}
