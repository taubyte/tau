package auth

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
)

// wireHarness is a running Service on one mocknet host reached from a second one
// over the real transport.
type wireHarness struct {
	Service    *Service
	Mocknet    mocknet.Mocknet
	ServerHost host.Host
	CallerHost host.Host
}

func newWireHarness(t testing.TB, opts ...Option) *wireHarness {
	t.Helper()

	mn := newMocknet(t)
	server := newHost(t, mn)
	caller := newHost(t, mn)
	s, err := New(server, opts...)
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { s.Close() })

	return &wireHarness{Service: s, Mocknet: mn, ServerHost: server, CallerHost: caller}
}

// dialAuth opens a p2p/rpc.Client from from to the harness's server, over
// the protocol this package's own Service listens on.
func dialAuth(t testing.TB, from, to host.Host) *rpc.Client {
	t.Helper()
	c, err := rpc.NewClient(from, to.ID(), Protocol)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}
