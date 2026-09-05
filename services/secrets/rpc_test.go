package secrets

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"
)

// EVERY TEST IN THIS FILE GOES OVER THE WIRE: connect-go, over
// go-libp2p-gostream, over libp2p's mocknet. What the vault MEANS is proven in
// vault_test.go against a map; what is left is that a request survives the
// round trip and that a failure arrives as a status a caller can act on —
// neither of which a direct handler call would show.
type harness struct {
	Service    *Service
	CallerHost host.Host
	ServerHost host.Host
}

func newHarness(t testing.TB, store Store) *harness {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	server := newHost(t, mn)
	caller := newHost(t, mn)

	s, err := New(server, WithStore(store))
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { s.Close() })

	return &harness{Service: s, CallerHost: caller, ServerHost: server}
}

func newHost(t testing.TB, mn mocknet.Mocknet) host.Host {
	t.Helper()
	h, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	return h
}

func newClient(t testing.TB, h *harness) *Client {
	t.Helper()
	c, err := NewClient(h.CallerHost, h.ServerHost.ID())
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

// TestARemoteVaultAnswersLikeALocalOne is the pair rpc.go and client.go exist
// for: a worker composed against either differs in no code path but the
// constructor.
func TestARemoteVaultAnswersLikeALocalOne(t *testing.T) {
	h := newHarness(t, NewMemStore())
	c := newClient(t, h)
	ctx := t.Context()

	require.NoError(t, c.Set(ctx, "shop", "production", "DATABASE_URL", []byte("postgres://x")))

	value, err := c.Get(ctx, "shop", "production", "DATABASE_URL")
	require.NoError(t, err)
	require.Equal(t, "postgres://x", string(value))

	// The same value, through services/worker's own interface.
	secret, err := c.Secret(ctx, "shop", "production", "DATABASE_URL")
	require.NoError(t, err)
	require.Equal(t, "postgres://x", secret)

	names, err := c.List(ctx, "shop", "production")
	require.NoError(t, err)
	require.Equal(t, []string{"DATABASE_URL"}, names)

	require.NoError(t, c.Delete(ctx, "shop", "production", "DATABASE_URL"))
	_, err = c.Get(ctx, "shop", "production", "DATABASE_URL")
	require.ErrorIs(t, err, ErrNotSet, "the sentinel survived the wire")
}

// TestASetOnOneSideIsReadableOnTheOther — the local vault and the served one
// are the same rows, which is what Local() promises a composer.
func TestWhatTheServiceServesIsWhatLocalReads(t *testing.T) {
	h := newHarness(t, NewMemStore())
	c := newClient(t, h)
	ctx := t.Context()

	require.NoError(t, c.Set(ctx, "shop", "production", "TOKEN", []byte("v")))

	value, err := h.Service.Local().Get(ctx, "shop", "production", "TOKEN")
	require.NoError(t, err)
	require.Equal(t, "v", string(value))
}

// TestEveryFailureArrivesAsItsOwnSentinel. A caller distinguishes "you asked
// wrong" from "nothing is set" from "the store could not look", and acts
// differently on each: fix the request, set the secret, retry elsewhere.
func TestEveryFailureArrivesAsItsOwnSentinel(t *testing.T) {
	c := newClient(t, newHarness(t, NewMemStore()))
	ctx := t.Context()

	for _, tc := range []struct {
		name                             string
		project, environment, secretName string
		want                             error
	}{
		{"unset", "shop", "production", "NEVER_SET", ErrNotSet},
		{"no project", "", "production", "T", ErrNoProject},
		{"no environment", "shop", "", "T", ErrNoEnvironment},
		{"no name", "shop", "production", "", ErrNoName},
		{"a name that climbs", "shop", "staging", "../production/T", ErrSeparator},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Get(ctx, tc.project, tc.environment, tc.secretName)
			require.ErrorIs(t, err, tc.want)
		})
	}
}

// TestAStoreFailureIsUnavailableAndNotInternal. Internal tells a caller to
// stop; a replicated store that could not answer here can answer on the next
// node, and Unavailable is what says so.
func TestAStoreFailureIsUnavailableAndNotInternal(t *testing.T) {
	c := newClient(t, newHarness(t, brokenStore{}))
	ctx := t.Context()

	_, err := c.Get(ctx, "shop", "production", "T")
	require.Error(t, err)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	require.NotErrorIs(t, err, ErrNotSet, "an unreachable store is not a missing secret")

	// The write path says the same thing. A Set that came back Internal would
	// stop a rotation that the next node would have accepted.
	for _, call := range []func() error{
		func() error { return c.Set(ctx, "shop", "production", "T", []byte("v")) },
		func() error { return c.Delete(ctx, "shop", "production", "T") },
		func() error { _, err := c.List(ctx, "shop", "production"); return err },
	} {
		err := call()
		require.Error(t, err)
		require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	}
}

func TestAServiceWithNoHostOrNoStoreIsRefused(t *testing.T) {
	_, err := New(nil, WithStore(NewMemStore()))
	require.ErrorIs(t, err, ErrNoHost)

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	_, err = New(newHost(t, mn))
	require.ErrorIs(t, err, ErrNoStore)

	_, err = New(newHost(t, mn), WithStore(nil))
	require.ErrorIs(t, err, ErrNoStore)
}

func TestStartingTwiceOrAfterCloseIsAnError(t *testing.T) {
	h := newHarness(t, NewMemStore())
	require.Error(t, h.Service.Start(t.Context()), "a second listener would leak")

	require.NoError(t, h.Service.Close())
	require.NoError(t, h.Service.Close(), "Close is idempotent")
	require.Error(t, h.Service.Start(t.Context()))
}

// TestTheSecretsServiceIsNotOnTheInternet. Get returns the value and no call
// is authorized, so a public ingress here is every project's secrets on the
// internet.
func TestTheSecretsServiceIsNotOnTheInternet(t *testing.T) {
	require.Equal(t, IngressNone, Descriptor.Ingress)
	require.Equal(t, "/tau/secrets/v1", string(Descriptor.Protocol))
}
