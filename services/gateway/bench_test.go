package gateway

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

type benchWorkers struct{ reply *invoke.Response }

func (w benchWorkers) Invoke(context.Context, specs.Address, *invoke.Request) (*invoke.Response, error) {
	return w.reply, nil
}

func (benchWorkers) Transform(_ context.Context, _ specs.Address, req *invoke.Request) (*invoke.Outcome, error) {
	return &invoke.Outcome{Forward: req}, nil
}

func (benchWorkers) Route(context.Context, specs.Address, *invoke.Request) (*invoke.Routing, error) {
	return &invoke.Routing{To: &invoke.Destination{Kind: "function", Name: "handled"}}, nil
}

func (benchWorkers) Conn(context.Context, specs.Address, string) (net.Conn, error) {
	return nil, net.ErrClosed
}

func benchGateway(b *testing.B) http.Handler {
	b.Helper()
	bytes := compiled(b)
	reg, err := register.New(register.WithStore(register.NewMemStore()))
	require.NoError(b, err)
	p, err := specs.Load(bytes)
	require.NoError(b, err)
	_, err = reg.Publish(b.Context(), register.Publication{
		Project: "shop", Environment: "prod", Revision: 1,
		Compiled: bytes, Hosts: hostsOf(b, p, ""),
	})
	require.NoError(b, err)

	reply := &invoke.Response{Status: http.StatusOK, Body: []byte("ok")}
	at := time.Unix(1_700_000_000, 0)
	s, err := New(Config{},
		WithHosts(reg), WithSource(reg), WithWorkers(benchWorkers{reply: reply}),
		WithClock(func() time.Time { return at }))
	require.NoError(b, err)
	return s.Handler()
}

func BenchmarkServeHTTP(b *testing.B) {
	gw := benchGateway(b)
	noop := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	type testCase struct {
		name, method, url, body string
		h                       http.Handler
	}
	for _, c := range []testCase{
		{"baseline", http.MethodGet, "http://nobody.example.com/", "", noop},
		{"invoke", http.MethodPost, "http://mcp.example.com/", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, gw},
		{"routed", http.MethodGet, "http://api.example.com/v1/orders", "", gw},
		{"miss", http.MethodGet, "http://nobody.example.com/", "", gw},
	} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				r := httptest.NewRequest(c.method, c.url, strings.NewReader(c.body))
				r.RemoteAddr = "203.0.113.7:51000"
				w := httptest.NewRecorder()
				c.h.ServeHTTP(w, r)
				if w.Code == 0 {
					b.Fatal("no answer")
				}
			}
		})
	}
}

// BenchmarkServeHTTPParallel is a routed request from every core at once: what one
// costs when the host table and the project cache are contended.
func BenchmarkServeHTTPParallel(b *testing.B) {
	gw := benchGateway(b)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			r := httptest.NewRequest(http.MethodGet, "http://api.example.com/v1/orders", nil)
			r.RemoteAddr = "203.0.113.7:51000"
			w := httptest.NewRecorder()
			gw.ServeHTTP(w, r)
			if w.Code == 0 {
				b.Fatal("no answer")
			}
		}
	})
}
