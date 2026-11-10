package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	goHttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	peercore "github.com/libp2p/go-libp2p/core/peer"
	substrateClient "github.com/taubyte/tau/clients/p2p/substrate"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
	con "github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	streams "github.com/taubyte/tau/p2p/streams/service"
	httptun "github.com/taubyte/tau/p2p/streams/tunnels/http"
	"github.com/taubyte/tau/pkg/platformerr"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	protocolCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/services/substrate/components/metrics"
)

// patientLookups keeps a slow test machine from losing a candidate to the timeout.
func patientLookups(t *testing.T) {
	old := lookupTimeouts
	lookupTimeouts = []time.Duration{5 * time.Second}
	t.Cleanup(func() { lookupTimeouts = old })
}

func noLookup(int64) {}

type counted struct {
	n      atomic.Int64
	answer func(w goHttp.ResponseWriter)
}

func (c *counted) serve(w goHttp.ResponseWriter, r *goHttp.Request, _ io.Writer) {
	c.n.Add(1)
	io.Copy(io.Discard, r.Body)
	c.answer(w)
}

func ok(body string) func(goHttp.ResponseWriter) {
	return func(w goHttp.ResponseWriter) { w.Write([]byte(body)) }
}

// refuse is what substrate does when its admission limit turns a request away.
func refuse(w goHttp.ResponseWriter) {
	httptun.Refuse(w)
	platformerr.Write(w, platformerr.New(platformerr.NodeBusy, errors.New("full")), "")
}

func free(f float32) *metrics.Function { return &metrics.Function{Free: f} }

func TestPicksNodeWithMostFreeCapacity(t *testing.T) {
	patientLookups(t)
	busy, idle := &counted{answer: ok("busy")}, &counted{answer: ok("idle")}
	h := fakeSubstrates(t,
		substrateDef{noLookup, free(0.1), busy.serve},
		substrateDef{noLookup, free(0.9), idle.serve})

	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "http://a.example.com/", nil))
	if w.Body.String() != "idle" || busy.n.Load() != 0 {
		t.Fatalf("body %q, busy node served %d", w.Body, busy.n.Load())
	}
}

func TestRefusedGetGoesToNextNodeOnce(t *testing.T) {
	patientLookups(t)
	first, second := &counted{answer: refuse}, &counted{answer: ok("served")}
	h := fakeSubstrates(t,
		substrateDef{noLookup, free(0.9), first.serve},
		substrateDef{noLookup, free(0.1), second.serve})

	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "http://a.example.com/", nil))
	if w.Code != goHttp.StatusOK || w.Body.String() != "served" {
		t.Fatalf("status %d body %q", w.Code, w.Body)
	}
	if first.n.Load() != 1 || second.n.Load() != 1 {
		t.Fatalf("first %d second %d", first.n.Load(), second.n.Load())
	}
	if got := w.Header().Values(ProxyHeader); len(got) != 1 {
		t.Fatalf("proxy header %v", got)
	}
}

func TestBothRefusedIs503(t *testing.T) {
	patientLookups(t)
	first, second := &counted{answer: refuse}, &counted{answer: refuse}
	h := fakeSubstrates(t,
		substrateDef{noLookup, free(0.9), first.serve},
		substrateDef{noLookup, free(0.1), second.serve})

	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "http://a.example.com/", nil))
	if w.Code != goHttp.StatusServiceUnavailable || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("status %d retry %q", w.Code, w.Header().Get("Retry-After"))
	}
	if first.n.Load() != 1 || second.n.Load() != 1 {
		t.Fatalf("first %d second %d", first.n.Load(), second.n.Load())
	}
}

// Anything that consumed a body stays a single send.
func TestRefusedWritesAreNotReplayed(t *testing.T) {
	for _, req := range []func() *goHttp.Request{
		func() *goHttp.Request {
			return httptest.NewRequest("POST", "http://a.example.com/", strings.NewReader("x"))
		},
		func() *goHttp.Request {
			return httptest.NewRequest("GET", "http://a.example.com/", strings.NewReader("x"))
		},
	} {
		patientLookups(t)
		first, second := &counted{answer: refuse}, &counted{answer: ok("served")}
		h := fakeSubstrates(t,
			substrateDef{noLookup, free(0.9), first.serve},
			substrateDef{noLookup, free(0.1), second.serve})

		w := httptest.NewRecorder()
		h(w, req())
		if w.Code != goHttp.StatusServiceUnavailable || second.n.Load() != 0 {
			t.Fatalf("status %d, second node served %d", w.Code, second.n.Load())
		}
	}
}

// A function answering 503 itself has run: only the platform's refusal may retry.
func TestFunctionsOwn503IsNotRetried(t *testing.T) {
	patientLookups(t)
	body := `{"code":503,"error":"node_busy"}`
	first := &counted{answer: func(w goHttp.ResponseWriter) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(goHttp.StatusServiceUnavailable)
		w.Write([]byte(body))
	}}
	second := &counted{answer: ok("served")}
	h := fakeSubstrates(t,
		substrateDef{noLookup, free(0.9), first.serve},
		substrateDef{noLookup, free(0.1), second.serve})

	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "http://a.example.com/", nil))
	if w.Code != goHttp.StatusServiceUnavailable || w.Body.String() != body || second.n.Load() != 0 {
		t.Fatalf("status %d body %q second %d", w.Code, w.Body, second.n.Load())
	}
}

// The real shape: gateway and substrate share node A, and the gateway's client
// node is bootstrapped to A alone. It has to see A's substrate and B's. The DHT
// ignores loopback addresses, so this needs a real interface.
func TestClientNodeReachesLocalAndRemoteSubstrate(t *testing.T) {
	ip := privateIP()
	if ip == "" {
		t.Skip("no private network interface")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	listen := []string{"/ip4/" + ip + "/tcp/0"}

	full := func(bootstrap ...peercore.AddrInfo) peer.Node {
		p, err := peer.NewPublic(ctx, nil, keypair.NewRaw(), nil, listen, nil, peer.BootstrapParams{Enable: true, Peers: bootstrap})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		svc, err := streams.New(p, protocolCommon.Substrate, protocolCommon.SubstrateProtocol)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(svc.Stop)
		if err := svc.DefineStream(substrateClient.CommandHTTP,
			func(context.Context, con.Connection, command.Body) (cr.Response, error) {
				return cr.Response{functionSpec.PathVariable.String(): new(metrics.Function).Encode()}, nil
			},
			func(context.Context, io.ReadWriter) {}); err != nil {
			t.Fatal(err)
		}
		svc.Start()
		return p
	}
	a := full()
	b := full(peercore.AddrInfo{ID: a.ID(), Addrs: a.Peer().Addrs()})

	lite, err := peer.NewClientNode(ctx, nil, keypair.NewRaw(), nil, listen, nil, true,
		[]peercore.AddrInfo{{ID: a.ID(), Addrs: a.Peer().Addrs()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lite.Close)

	sc, err := substrateClient.New(ctx, lite, substrateClient.Threshold(2), substrateClient.Timeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sc.Close() })

	seen := map[peercore.ID]bool{}
	start := time.Now()
	for deadline := start.Add(150 * time.Second); len(seen) < 2; time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("client node saw %v, want %s and %s; connected to %v", seen, a.ID(), b.ID(), lite.Peer().Network().Peers())
		}
		ch, err := sc.ProxyHTTP("a.example.com", "/", "GET")
		if err != nil {
			continue
		}
		for res := range ch {
			if res.Error() == nil {
				seen[res.PID()] = true
			}
			res.Close()
		}
	}
	t.Logf("client node saw both substrates after %s", time.Since(start))
}

func privateIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipn, ok := addr.(*net.IPNet); ok && ipn.IP.To4() != nil && ipn.IP.IsPrivate() {
			return ipn.IP.String()
		}
	}
	return ""
}
