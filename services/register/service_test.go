package register

import (
	"net/http"
	"testing"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// TestNewOpensNothing. A node constructs every service before starting any of
// them, so New must bind no listener and reach no network.
func TestNewOpensNothing(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	s, err := New(newHost(t, mn), WithStore(pkgregister.NewMemStore()))
	require.NoError(t, err)
	require.Nil(t, s.server)
	require.NoError(t, s.Close())
}

// TestNewRefusesToInventItsOwnIdentity. A service that can mint a libp2p host
// for itself can be started two ways, and one of them is never how production
// starts it.
func TestNewRefusesToInventItsOwnIdentity(t *testing.T) {
	_, err := New(nil, WithStore(pkgregister.NewMemStore()))
	require.ErrorIs(t, err, ErrNoHost)
}

func TestNewRefusesNoStore(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	_, err := New(h)
	require.ErrorIs(t, err, ErrNoStore)

	_, err = New(h, WithStore(nil))
	require.ErrorIs(t, err, ErrNoStore)
}

// TestServerOptionsReachListen. They are passed straight through, so the check
// is that Start actually hands them over rather than that this package
// understands any one of them.
func TestServerOptionsReachListen(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	s, err := New(newHost(t, mn),
		WithStore(pkgregister.NewMemStore()),
		WithServerOptions(rpc.WithHTTPServer(func(hs *http.Server) { hs.Handler = http.NotFoundHandler() })))
	require.NoError(t, err)

	// rpc.Listen owns http.Server.Handler and refuses an option that sets it,
	// which is how this test knows the option was not dropped on the floor.
	err = s.Start(t.Context())
	require.ErrorIs(t, err, rpc.ErrServerFieldOwned)
	require.NoError(t, s.Close())
}

// TestStartingTwiceIsAnErrorNotASecondListener.
func TestStartingTwiceIsAnErrorNotASecondListener(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	s, err := New(newHost(t, mn), WithStore(pkgregister.NewMemStore()))
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { s.Close() })

	require.Error(t, s.Start(t.Context()))
}

// TestCloseIsIdempotentAndFinal.
func TestCloseIsIdempotentAndFinal(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	s, err := New(newHost(t, mn), WithStore(pkgregister.NewMemStore()))
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))

	require.NoError(t, s.Close())
	require.NoError(t, s.Close())
	require.Error(t, s.Start(t.Context()), "a closed service does not start again")
}

// TestTheProtocolIsDerived: the id is the proto package's, with its "tau."
// segment.
func TestTheProtocolIsDerived(t *testing.T) {
	require.Equal(t, "/tau/register/v2", string(Protocol))
	require.Equal(t, Protocol, Descriptor.Protocol)
	require.Equal(t, IngressNone, Descriptor.Ingress, "publish moves what a project runs; it does not face the internet")
}
