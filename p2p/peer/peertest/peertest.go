// Package peertest gives a test a network of mock nodes of its own: nodes made for
// one test never reach another's, and the network goes when the test ends.
package peertest

import (
	"context"
	"sync"
	"testing"

	"github.com/taubyte/tau/p2p/peer"
)

// nets is each running test's network, by test. A test's helpers find it through
// the test rather than being handed it.
var nets = struct {
	sync.Mutex
	of map[testing.TB]*peer.Mocknet
}{of: map[testing.TB]*peer.Mocknet{}}

// Net is t's network, made the first time it is asked for and closed when t ends.
func Net(t testing.TB) *peer.Mocknet {
	nets.Lock()
	defer nets.Unlock()
	if m, ok := nets.of[t]; ok {
		return m
	}
	m := peer.NewMocknet()
	nets.of[t] = m
	t.Cleanup(func() {
		nets.Lock()
		delete(nets.of, t)
		nets.Unlock()
		m.Close()
	})
	return m
}

// Mock is a node on t's network that lives until ctx ends or it is closed.
func Mock(t testing.TB, ctx context.Context) peer.Node { return Net(t).Mock(ctx) }

// LinkAll links each pair of the open nodes on t's network once.
func LinkAll(t testing.TB) error { return Net(t).LinkAll() }
