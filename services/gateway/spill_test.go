package gateway

import (
	"io"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	goHttp "net/http"
)

// rtt is the delay a lookup answer takes from another region. The first of the
// gateway's lookup attempts, 30 ms, cannot see an answer from that far.
const rtt = 85 * time.Millisecond

func slowLookup(int64) { time.Sleep(rtt) }

// spillGateway is localGateway with a full local node and peers an RTT away.
func spillGateway(t *testing.T, defs ...substrateDef) (goHttp.HandlerFunc, *atomic.Int64) {
	h, _, _, lookups := localGateway(t, localFull, defs...)
	return h, lookups
}

func get() *goHttp.Request { return httptest.NewRequest("GET", "http://a.example.com/", nil) }

// A full node forwards to the node with room straight away: one question to
// that node per request, not a search of every node with a timeout the answer
// cannot meet.
func TestSpillGoesStraightToNodeWithRoom(t *testing.T) {
	busy, idle := &counted{answer: ok("busy")}, &counted{answer: ok("idle")}
	h, lookups := spillGateway(t,
		substrateDef{slowLookup, free(0), busy.serve},
		substrateDef{slowLookup, free(0.9), idle.serve})

	const n = 12
	for i := range n {
		w := httptest.NewRecorder()
		h(w, get())
		if w.Code != goHttp.StatusOK || w.Body.String() != "idle" {
			t.Fatalf("request %d: status %d body %q", i, w.Code, w.Body)
		}
	}
	if busy.n.Load() != 0 || idle.n.Load() != n {
		t.Fatalf("busy node served %d, idle %d", busy.n.Load(), idle.n.Load())
	}
	// one per request, plus the periodic refresh of both nodes; a per-request
	// search costs at least two attempts on both nodes, 4n.
	if got := lookups.Load(); got >= 3*n {
		t.Fatalf("%d lookups for %d requests: spilling searched per request", got, n)
	}
}

// A cluster with no room refuses from the view, without touching any node.
func TestSpillFullClusterRefusesQuickly(t *testing.T) {
	a, b := &counted{answer: ok("a")}, &counted{answer: ok("b")}
	h, lookups := spillGateway(t,
		substrateDef{slowLookup, free(0), a.serve},
		substrateDef{slowLookup, free(0), b.serve})

	first := httptest.NewRecorder()
	h(first, get()) // pays for the view
	if first.Code != goHttp.StatusServiceUnavailable {
		t.Fatalf("status %d", first.Code)
	}
	warm := lookups.Load()

	const n = 20
	start := time.Now()
	for range n {
		w := httptest.NewRecorder()
		h(w, get())
		if w.Code != goHttp.StatusServiceUnavailable || w.Header().Get("Retry-After") != "1" {
			t.Fatalf("status %d retry %q", w.Code, w.Header().Get("Retry-After"))
		}
		if v := w.Header().Values(ProxyHeader); len(v) != 0 {
			t.Fatalf("names a node that served nothing: %v", v)
		}
	}
	if took := time.Since(start); took > n*rtt/2 {
		t.Fatalf("%d refusals took %v", n, took)
	}
	if a.n.Load()+b.n.Load() != 0 {
		t.Fatalf("tunnels opened to full nodes: %d and %d", a.n.Load(), b.n.Load())
	}
	if got := lookups.Load() - warm; got > 2*4 { // the periodic refresh only
		t.Fatalf("%d lookups during refusals", got)
	}
}

// Only maxSpill requests may wait on other nodes; the next is refused at once.
func TestSpillIsBounded(t *testing.T) {
	old := maxSpill
	maxSpill = 1
	t.Cleanup(func() { maxSpill = old })

	entered, release := make(chan struct{}), make(chan struct{})
	hold := func(w goHttp.ResponseWriter, r *goHttp.Request, _ io.Writer) {
		close(entered)
		<-release
	}
	h, _ := spillGateway(t, substrateDef{noLookup, free(0.9), hold})

	done := make(chan struct{})
	go func() {
		defer close(done)
		h(httptest.NewRecorder(), get())
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first request never reached the node")
	}

	w := httptest.NewRecorder()
	start := time.Now()
	h(w, get())
	if w.Code != goHttp.StatusServiceUnavailable || time.Since(start) > spillTimeout/2 {
		t.Fatalf("status %d after %v", w.Code, time.Since(start))
	}
	close(release)
	<-done
}

// A node the view thought free but that refuses is skipped, and the GET lands on another.
func TestSpillMovesPastRefusingNode(t *testing.T) {
	refusing, serving := &counted{answer: refuse}, &counted{answer: ok("served")}
	h, _ := spillGateway(t,
		substrateDef{noLookup, free(0.9), refusing.serve},
		substrateDef{noLookup, free(0.9), serving.serve})

	for i := range 8 {
		w := httptest.NewRecorder()
		h(w, get())
		if w.Code != goHttp.StatusOK || w.Body.String() != "served" {
			t.Fatalf("request %d: status %d body %q", i, w.Code, w.Body)
		}
	}
}

// With no other node there is nobody to ask: the local refusal stands, at once.
func TestSpillAloneRefusesAtOnce(t *testing.T) {
	h, _ := spillGateway(t)
	w := httptest.NewRecorder()
	start := time.Now()
	h(w, get())
	if w.Code != goHttp.StatusServiceUnavailable || time.Since(start) > spillTimeout/2 {
		t.Fatalf("status %d after %v", w.Code, time.Since(start))
	}
}
