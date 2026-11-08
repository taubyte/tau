package gateway

import (
	"net/http/httptest"
	"testing"
	"time"

	goHttp "net/http"

	substrateClient "github.com/taubyte/tau/clients/p2p/substrate"
)

// cloud is a gateway on a client with the substrate client's own defaults
// (threshold 3, 10 ms), which is what a deployed gateway runs with.
func cloud(t *testing.T, defs ...substrateDef) (h goHttp.HandlerFunc, subIDs []string, stop []func(), gwPeers func() int) {
	ctx, gw, subs, stop := startSubstrates(t, defs...)
	sc, err := substrateClient.New(ctx, gw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sc.Close() })
	c := new(captureHttp)
	g := &Gateway{ctx: ctx, http: c, substrateClient: sc, clientNode: gw}
	g.attach()
	for _, s := range subs {
		subIDs = append(subIDs, s.ID().String())
	}
	return c.handler, subIDs, stop, func() int { return len(gw.Peer().Network().Peers()) }
}

// quickest is the fastest of n requests: a loaded machine slows some, but only
// a lookup that waits for absent peers slows all of them (30 ms and up).
func quickest(t *testing.T, h goHttp.HandlerFunc, n int) (best time.Duration, last *httptest.ResponseRecorder) {
	best = time.Hour
	for range n {
		w := httptest.NewRecorder()
		start := time.Now()
		h(w, httptest.NewRequest("GET", "http://a.example.com/", nil))
		if d := time.Since(start); d < best {
			best = d
		}
		last = w
	}
	return best, last
}

const noWait = 25 * time.Millisecond

// (a) one node, one substrate: the lookup must not wait for two more answers.
func TestOneSubstrateCostsNoWait(t *testing.T) {
	only := &counted{answer: ok("served")}
	h, _, _, _ := cloud(t, substrateDef{noLookup, free(0.9), only.serve})

	best, w := quickest(t, h, 5)
	if w.Code != goHttp.StatusOK || w.Body.String() != "served" {
		t.Fatalf("status %d body %q", w.Code, w.Body)
	}
	if best > noWait {
		t.Fatalf("fastest of 5 requests took %s: the lookup waits for peers that do not exist", best)
	}
}

// (b) the local substrate is one candidate among several, and the best one is
// served directly: the other node sees a lookup only.
func TestLocalSubstrateServesWhenBest(t *testing.T) {
	local, remote := &counted{answer: ok("local")}, &counted{answer: ok("remote")}
	h, ids, _, _ := cloud(t,
		substrateDef{noLookup, free(0.9), local.serve},
		substrateDef{noLookup, free(0.1), remote.serve})

	best, w := quickest(t, h, 5)
	if got := w.Header().Get(ProxyHeader); got != ids[0] {
		t.Fatalf("served by %q, want the local %q", got, ids[0])
	}
	if remote.n.Load() != 0 || local.n.Load() != 5 {
		t.Fatalf("local %d remote %d", local.n.Load(), remote.n.Load())
	}
	if best > noWait {
		t.Fatalf("fastest of 5 requests took %s", best)
	}
}

// (c) a gateway whose cloud has no substrate yet: a clear 503 soon, not a hang.
func TestNoSubstrateYetIs503Soon(t *testing.T) {
	h, _, _, _ := cloud(t)

	start := time.Now()
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "http://a.example.com/", nil))
	if w.Code != goHttp.StatusServiceUnavailable || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("status %d retry %q", w.Code, w.Header().Get("Retry-After"))
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %s to say 503", d)
	}
}

// (d) two of three substrates are gone: the survivor answers with no penalty.
func TestSurvivorCostsNoWait(t *testing.T) {
	var served [3]*counted
	var defs []substrateDef
	for i := range served {
		served[i] = &counted{answer: ok("served")}
		defs = append(defs, substrateDef{noLookup, free(0.5), served[i].serve})
	}
	h, _, stop, peers := cloud(t, defs...)

	quickest(t, h, 3) // all three alive
	stop[1]()
	stop[2]()
	for deadline := time.Now().Add(10 * time.Second); peers() != 1; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("gateway still connected to %d peers", peers())
		}
	}
	quickest(t, h, 6) // the client forgets the dead ones as it fails to dial them

	before := served[0].n.Load()
	best, w := quickest(t, h, 5)
	if w.Code != goHttp.StatusOK || served[0].n.Load()-before != 5 {
		t.Fatalf("status %d, survivor served %d of 5", w.Code, served[0].n.Load()-before)
	}
	if best > noWait {
		t.Fatalf("fastest of 5 requests took %s with one substrate left", best)
	}
}
