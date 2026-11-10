// Package peertest gives a test a network of mock nodes of its own.
package peertest

import (
	"testing"

	"github.com/taubyte/tau/p2p/peer"
)

// Net is a network for t, closed when t ends. Only the nodes made on it reach
// each other, so a test hands it to whatever makes its nodes.
func Net(t testing.TB) *peer.Mocknet {
	m := peer.NewMocknet()
	t.Cleanup(func() { m.Close() })
	return m
}
