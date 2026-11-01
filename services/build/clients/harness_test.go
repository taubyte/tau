package clients

import (
	"net/http"
	"testing"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/protocol"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// The fixture is mocknet only; discovery.Static supplies the peers.

func newMocknet(t testing.TB) mocknet.Mocknet {
	t.Helper()
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	return mn
}

func newHost(t testing.TB, mn mocknet.Mocknet) host.Host {
	t.Helper()
	h, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	return h
}

// serveAuth mounts auth's IdentityService on a fresh mocknet host and returns
// it: any real service on AuthProtocol will do for what the pool tests ask.
func serveAuth(t testing.TB, mn mocknet.Mocknet) host.Host {
	t.Helper()
	h := newHost(t, mn)
	mux := http.NewServeMux()
	mux.Handle(authv2connect.NewIdentityServiceHandler(fakeIdentity{}))
	serve(t, h, AuthProtocol, mux)
	return h
}

func serve(t testing.TB, h host.Host, id protocol.ID, handler http.Handler) {
	t.Helper()
	srv, err := rpc.Listen(t.Context(), h, id, handler)
	require.NoError(t, err)
	t.Cleanup(func() { srv.Close() })
}
