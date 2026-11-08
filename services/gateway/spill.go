package gateway

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"runtime"
	"sync"
	"time"

	goHttp "net/http"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/streams/client"
	tunnel "github.com/taubyte/tau/p2p/streams/tunnels/http"
	"github.com/taubyte/tau/services/substrate/components/metrics"
)

const (
	// spillRefresh is how often the view of other nodes is renewed while
	// requests spill; an idle gateway sends nothing.
	spillRefresh = 250 * time.Millisecond
	// spillMaxAge is the oldest view worth using: past it a spill waits for a new one.
	spillMaxAge = 2 * time.Second
	// spillTimeout is one question to other nodes. It must fit a round trip
	// between regions; a spill that cannot be placed by then is refused.
	spillTimeout = 400 * time.Millisecond
)

// maxSpill bounds the requests on their way to other nodes, so the rest of an
// overload is refused at once. A spill spends about two round trips idle, so
// the bound is what caps its rate: 128 per CPU allows some 1300/s on 2 CPUs at
// 85 ms round trips, about what the other nodes' spare capacity can use.
var maxSpill = int64(runtime.NumCPU() * 128)

// spillView is the free capacity of other nodes, last time they were asked.
type spillView struct {
	mu      sync.Mutex
	free    map[peer.ID]float32
	at      time.Time
	running chan struct{} // closed when the refresh in progress ends
}

func (s *spillView) set(pid peer.ID, free float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.free != nil {
		s.free[pid] = free
	}
}

// pick chooses a node with room, more often the one with more of it.
func (s *spillView) pick() (peer.ID, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total float32
	for _, f := range s.free {
		total += f
	}
	if total <= 0 {
		return "", false
	}
	at := rand.Float32() * total
	var last peer.ID
	for pid, f := range s.free {
		if f <= 0 {
			continue
		}
		if last = pid; at < f {
			break
		}
		at -= f
	}
	return last, true
}

// refresh asks the other nodes for their capacity if the view is due, and
// returns the channel of the refresh in progress, if any. r is copied: the
// refresh can outlive the request.
func (g *Gateway) refreshSpillView(r *goHttp.Request) <-chan struct{} {
	s := &g.spillView
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != nil || time.Since(s.at) < spillRefresh {
		return s.running
	}
	known := g.knownSubstrates()
	if known == 0 {
		return nil
	}
	running := make(chan struct{})
	s.running = running
	probe := &goHttp.Request{Host: r.Host, Method: r.Method, URL: &url.URL{Path: r.URL.Path}}
	go func() {
		free := make(map[peer.ID]float32)
		if res, err := g.lookupOnce(probe, spillTimeout, known); err == nil {
			for _, m := range res.functions {
				free[m.PID()] = m.metrics.(*metrics.Function).Free
			}
			res.close()
		}
		s.mu.Lock()
		s.free, s.at, s.running = free, time.Now(), nil
		s.mu.Unlock()
		close(running)
	}()
	return running
}

// spillPeer is a node to send r to, from the view. Only a view too old to use
// makes a request wait, and only for the one refresh already on its way.
func (g *Gateway) spillPeer(r *goHttp.Request) (peer.ID, bool) {
	running := g.refreshSpillView(r)
	g.spillView.mu.Lock()
	stale := time.Since(g.spillView.at) > spillMaxAge
	g.spillView.mu.Unlock()
	if stale && running != nil {
		select {
		case <-running:
		case <-r.Context().Done():
			return "", false
		}
	}
	return g.spillView.pick()
}

// spill sends a request the local substrate is too full for to a node known to
// have room, so overload costs one question to one node instead of a search of
// all of them, and busy is the answer the moment no node has room.
func (g *Gateway) spill(w goHttp.ResponseWriter, r *goHttp.Request, busy error) error {
	if g.spilling.Add(1) > maxSpill {
		g.spilling.Add(-1)
		return busy
	}
	defer g.spilling.Add(-1)

	for range maxTries {
		pid, ok := g.spillPeer(r)
		if !ok {
			break
		}
		result, err := g.lookupOnce(r, spillTimeout, 1, client.To(pid))
		if err != nil || len(result.functions) == 0 {
			g.spillView.set(pid, 0)
			if err == nil {
				result.close()
			}
			continue
		}
		pick := result.functions[0]
		free := pick.metrics.(*metrics.Function).Free
		g.spillView.set(pid, free)
		if free <= 0 {
			result.close()
			continue
		}

		w.Header().Set(ProxyHeader, pid.String())
		err = tunnel.Frontend(w, r, pick.Response)
		result.close()
		if err == nil {
			return nil
		}
		if !errors.Is(err, tunnel.ErrRefused) {
			return fmt.Errorf("tunneling Frontend failed with: %w", err)
		}
		g.spillView.set(pid, 0)
		w.Header().Del(ProxyHeader)
	}
	return busy
}
