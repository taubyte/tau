package auth

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"
)

func newMocknet(t testing.TB) mocknet.Mocknet {
	t.Helper()
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	return mn
}

// newHost adds a host to mn and links it to every host already there, which
// is all mocknet needs before a stream can be opened.
func newHost(t testing.TB, mn mocknet.Mocknet) host.Host {
	t.Helper()
	h, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	return h
}

// newTestService constructs (but does not Start) a Service. Handler-level tests call
// handler methods directly; rpc_test.go and authz_test.go Start it where a live
// listener is the point.
func newTestService(t testing.TB, opts ...Option) *Service {
	t.Helper()
	s, err := New(newHost(t, newMocknet(t)), opts...)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}
