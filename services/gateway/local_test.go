package gateway

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	goHttp "net/http"

	substrateClient "github.com/taubyte/tau/clients/p2p/substrate"
	tauConfig "github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/pkg/platformerr"
)

// localGateway is a gateway with a substrate in its own process, next to defs
// on other nodes. lookups counts the lookups that reached those nodes.
func localGateway(t *testing.T, local tauConfig.LocalHTTP, defs ...substrateDef) (h goHttp.HandlerFunc, ids []string, id string, lookups *atomic.Int64) {
	lookups = new(atomic.Int64)
	for i := range defs {
		slow := defs[i].lookup
		defs[i].lookup = func(n int64) { lookups.Add(1); slow(n) }
	}
	ctx, gw, subs, _ := startSubstrates(t, defs...)
	sc, err := substrateClient.New(ctx, gw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sc.Close() })
	c := new(captureHttp)
	g := &Gateway{ctx: ctx, http: c, node: gw, clientNode: gw, substrateClient: sc, local: local}
	g.attach()
	for _, s := range subs {
		ids = append(ids, s.ID().String())
	}
	return c.handler, ids, gw.ID().String(), lookups
}

func TestLocalNodeServesWithoutLookup(t *testing.T) {
	other := &counted{answer: ok("other")}
	h, _, id, lookups := localGateway(t, func(w goHttp.ResponseWriter, r *goHttp.Request) (bool, error) {
		w.Write([]byte("local"))
		return true, nil
	}, substrateDef{noLookup, free(1), other.serve})

	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "http://a.example.com/", nil))
	if w.Body.String() != "local" || w.Header().Get(ProxyHeader) != id {
		t.Fatalf("body %q served by %q", w.Body, w.Header().Get(ProxyHeader))
	}
	if lookups.Load() != 0 || other.n.Load() != 0 {
		t.Fatalf("lookups %d, other node served %d", lookups.Load(), other.n.Load())
	}
}

var localFull tauConfig.LocalHTTP = func(goHttp.ResponseWriter, *goHttp.Request) (bool, error) {
	return false, platformerr.New(platformerr.NodeBusy, errors.New("full"))
}

// A full local node hands the request, body intact, to another node.
func TestFullLocalNodeSendsWorkElsewhere(t *testing.T) {
	patientLookups(t)
	var got string
	serve := func(w goHttp.ResponseWriter, r *goHttp.Request, _ io.Writer) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.Write([]byte("other"))
	}
	h, ids, _, _ := localGateway(t, localFull, substrateDef{noLookup, free(1), serve})

	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("POST", "http://a.example.com/", strings.NewReader("body")))
	if w.Code != goHttp.StatusOK || w.Body.String() != "other" || got != "body" {
		t.Fatalf("status %d body %q forwarded %q", w.Code, w.Body, got)
	}
	if v := w.Header().Values(ProxyHeader); len(v) != 1 || v[0] != ids[0] {
		t.Fatalf("proxy header %v, want %s", v, ids[0])
	}
}

// One server, full: a 503 the client can retry, not a hang or a 500.
func TestFullLocalNodeAloneIs503(t *testing.T) {
	h, _, _, _ := localGateway(t, localFull)

	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "http://a.example.com/", nil))
	if w.Code != goHttp.StatusServiceUnavailable || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("status %d retry %q", w.Code, w.Header().Get("Retry-After"))
	}
	if v := w.Header().Values(ProxyHeader); len(v) != 0 {
		t.Fatalf("names a node that served nothing: %v", v)
	}
}

// Once the local node has run a request, its failure is the answer: running it
// on another node as well would run it twice.
func TestLocalFailureIsNotRetriedElsewhere(t *testing.T) {
	other := &counted{answer: ok("other")}
	h, _, _, lookups := localGateway(t, func(goHttp.ResponseWriter, *goHttp.Request) (bool, error) {
		return true, platformerr.New(platformerr.FunctionTimeout, errors.New("slow"))
	}, substrateDef{noLookup, free(1), other.serve})

	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "http://a.example.com/", nil))
	if w.Code < 500 || lookups.Load() != 0 || other.n.Load() != 0 {
		t.Fatalf("status %d, lookups %d, other node served %d", w.Code, lookups.Load(), other.n.Load())
	}
}
