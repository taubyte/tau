package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	goHttp "net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	substrateClient "github.com/taubyte/tau/clients/p2p/substrate"
	"github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
	con "github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/tau/p2p/streams/packer"
	streams "github.com/taubyte/tau/p2p/streams/service"
	httptun "github.com/taubyte/tau/p2p/streams/tunnels/http"
	http "github.com/taubyte/tau/pkg/http"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	protocolCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/services/substrate/components/metrics"
)

// captureHttp keeps the handler the gateway registers, so tests drive the real one.
type captureHttp struct {
	http.Service
	handler goHttp.HandlerFunc
}

func (c *captureHttp) LowLevel(d *http.LowLevelDefinition) *mux.Route {
	c.handler = d.Handler
	return nil
}

func newTestGateway(ctx context.Context, sc substrate.ProxyClient) goHttp.HandlerFunc {
	cap := new(captureHttp)
	g := &Gateway{ctx: ctx, http: cap, substrateClient: sc}
	g.attach()
	return cap.handler
}

// fakeSubstrate is a real substrate stream service on its own peer, with the
// lookup and the tunnel handler supplied by the test.
func fakeSubstrate(t *testing.T, lookup func(n int64), serve func(w goHttp.ResponseWriter, r *goHttp.Request, raw io.Writer)) goHttp.HandlerFunc {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// real TCP peers: mocknet streams ignore read deadlines, so a slow lookup would never time out
	newPeer := func() peer.Node {
		p, err := peer.New(ctx, nil, keypair.NewRaw(), nil, []string{"/ip4/127.0.0.1/tcp/0"}, nil, true, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	sub, gw := newPeer(), newPeer()

	svc, err := streams.New(sub, protocolCommon.Substrate, protocolCommon.SubstrateProtocol)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Stop)

	var lookups atomic.Int64
	match := cr.Response{functionSpec.PathVariable.String(): new(metrics.Function).Encode()}
	if err := svc.DefineStream(substrateClient.CommandHTTP,
		func(context.Context, con.Connection, command.Body) (cr.Response, error) {
			lookup(lookups.Add(1))
			return match, nil
		},
		func(_ context.Context, rw io.ReadWriter) {
			w, r, err := httptun.Backend(rw)
			if err != nil {
				return // an abandoned attempt's stream: nothing was forwarded
			}
			serve(w, r, rw)
		}); err != nil {
		t.Fatal(err)
	}
	svc.Start()

	if err := gw.Peer().Connect(ctx, peercore.AddrInfo{ID: sub.ID(), Addrs: sub.Peer().Addrs()}); err != nil {
		t.Fatal(err)
	}
	// wait for identify, so the client finds the substrate without discovery
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		protos, _ := gw.Peer().Peerstore().GetProtocols(sub.ID())
		if slices.Contains(protos, protocol.ID(protocolCommon.SubstrateProtocol)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("substrate protocol never showed up")
		}
	}

	sc, err := substrateClient.New(ctx, gw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sc.Close() })
	return newTestGateway(ctx, sc)
}

// The retry loop repeats only the lookup. The request and its body reach the
// substrate once, after the loop, even when the first lookup is too slow.
func TestRetryForwardsRequestOnce(t *testing.T) {
	firstDone := make(chan struct{})
	var (
		lookups atomic.Int64
		mu      sync.Mutex
		bodies  [][]byte
	)
	handler := fakeSubstrate(t,
		func(n int64) {
			lookups.Store(n)
			if n == 1 { // the first attempt times out; its stream is reset before this answers
				time.Sleep(500 * time.Millisecond)
				close(firstDone)
			}
		},
		func(w goHttp.ResponseWriter, r *goHttp.Request, _ io.Writer) {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			bodies = append(bodies, b)
			mu.Unlock()
			w.WriteHeader(goHttp.StatusOK)
			w.Write([]byte("ok"))
		})

	body := make([]byte, 1<<20)
	rand.Read(body)
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest("POST", "http://a.example.com/upload", bytes.NewReader(body)))
	if w.Code != goHttp.StatusOK || w.Body.String() != "ok" {
		t.Fatalf("status %d body %q", w.Code, w.Body)
	}

	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the first lookup never ran: the test did not force a retry")
	}
	time.Sleep(200 * time.Millisecond) // let a late tunnel for the first attempt run, if it would

	if lookups.Load() < 2 {
		t.Fatalf("the lookup ran %d times: no retry happened", lookups.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("substrate saw %d requests, want 1", len(bodies))
	}
	if !bytes.Equal(bodies[0], body) {
		t.Fatalf("body changed: got %d bytes", len(bodies[0]))
	}
}

// A substrate that already sent 200 and a body, then a broken frame: the user
// must get exactly what the substrate sent, with no error text appended.
func TestFrontendFailureAfterBodyLeavesBody(t *testing.T) {
	handler := fakeSubstrate(t, func(int64) {},
		func(w goHttp.ResponseWriter, r *goHttp.Request, raw io.Writer) {
			w.WriteHeader(goHttp.StatusOK)
			w.Write([]byte("hello"))
			packer.New(httptun.Magic, httptun.Version).Send(99, raw, strings.NewReader("x"), 1) // unknown op: Frontend fails
		})
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest("GET", "http://a.example.com/", nil))
	if w.Code != goHttp.StatusOK || w.Body.String() != "hello" {
		t.Fatalf("status %d body %q", w.Code, w.Body)
	}
}

type failingProxy struct{ err error }

func (f failingProxy) ProxyHTTP(string, string, string, ...client.Option[client.Request]) (<-chan *client.Response, error) {
	return nil, f.err
}
func (failingProxy) Close() error { return nil }

// Internal error text names CIDs, paths and project ids; none of it is public.
func TestPublicBodyHidesInternals(t *testing.T) {
	secrets := []string{"bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi", "/tmp/x", "Qmc3WjpDvCaVY3jWmxranUY7roFhRj66SNqstiRbKxDbU4"}
	handler := newTestGateway(context.Background(), failingProxy{errors.New("caching CID `" + secrets[0] + "` at " + secrets[1] + " for project " + secrets[2] + " failed")})
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest("GET", "http://a.example.com/", nil))
	if w.Code != goHttp.StatusInternalServerError {
		t.Fatalf("status %d", w.Code)
	}
	for _, s := range secrets {
		if strings.Contains(w.Body.String(), s) {
			t.Fatalf("body leaks %q: %s", s, w.Body)
		}
	}
}
