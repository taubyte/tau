package dream

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
	httpIface "github.com/taubyte/tau/pkg/http"
	basicHttp "github.com/taubyte/tau/pkg/http/basic"
	"github.com/taubyte/tau/pkg/http/options"
)

// A universe reports the port a node took, which it can only do by asking the
// node after it bound. The alternative — reserving a number first and binding
// it later — is a window another process can step into, and four dreaming
// packages running in parallel step into it.
func TestTheP2PPortIsReadOffTheNodeThatBoundIt(t *testing.T) {
	unbound := []string{fmt.Sprintf(DefaultP2PListenFormat, 0)}

	node, err := peer.NewPublic(t.Context(), t.TempDir(), keypair.NewRaw(), nil, unbound, unbound, peer.BootstrapParams{})
	require.NoError(t, err)
	defer node.Close()

	port, err := boundP2PPort(node)
	require.NoError(t, err)
	require.NotZero(t, port, "reported the port it asked with rather than the one it got")
	require.Contains(t, node.Peer().Addrs()[0].String(), fmt.Sprintf("/tcp/%d", port))
}

type fakeService struct{ http httpIface.Service }

func (fakeService) Node() peer.Node { return nil }
func (fakeService) Close() error    { return nil }

type servingService struct{ fakeService }

func (s servingService) Http() httpIface.Service { return s.http }

// The http port comes from the service, because the service is who bound it.
func TestTheHTTPPortComesFromTheServiceThatServes(t *testing.T) {
	srv, err := basicHttp.New(t.Context(), options.Listen(DefaultHost+":0"))
	require.NoError(t, err)
	srv.Start()
	require.NoError(t, srv.Error())

	port, serving := boundHTTPPort(servingService{fakeService{http: srv}})
	require.True(t, serving)
	require.NotZero(t, port)

	listen, err := srv.GetListenAddress()
	require.NoError(t, err)
	require.Equal(t, listen.Port(), fmt.Sprint(port))
}

// Most services serve no http at all, and a universe reports a port only for
// those that do — an absent one is not a zero.
func TestAServiceWithNoHTTPReportsNoPort(t *testing.T) {
	for _, service := range []commonIface.Service{fakeService{}, servingService{}} {
		_, serving := boundHTTPPort(service)
		require.False(t, serving)
	}
}
