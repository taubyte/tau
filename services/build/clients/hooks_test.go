package clients

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/discovery"
	authv1 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1"
	"github.com/taubyte/tau/services/build/frontend"
)

// The interface satisfaction lives HERE rather than in hooks.go: a test file
// may import the role package, and production code in this package may not
// (services/build/boundary_test.go).
var _ frontend.Hooks = (*HookClient)(nil)

func newHooks(t *testing.T, get func(string) (*authv1.Hook, error)) *HookClient {
	t.Helper()
	mn := newMocknet(t)
	caller := newHost(t, mn)
	server := serveHooks(t, mn, get)

	p, err := NewPool(caller, discovery.Static(server.ID()), AuthProtocol, 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	c, err := Hooks(p)
	require.NoError(t, err)
	return c
}

// The whole hook is what comes back, because together its fields decide an
// authentication: the scheme and secret verify the body and the key says which
// repository it is good for. v1 fetched only the secret.
func TestGetReturnsTheHookWhole(t *testing.T) {
	var asked string
	c := newHooks(t, func(id string) (*authv1.Hook, error) {
		asked = id
		return &authv1.Hook{Id: id, RepositoryKey: "gitlab.example.com/acme/pay", Handle: "7", Scheme: "standard-webhooks", Secret: []byte("s3cret")}, nil
	})

	got, err := c.Get(t.Context(), "hook-1")

	require.NoError(t, err)
	require.Equal(t, "hook-1", asked)
	require.Equal(t, []byte("s3cret"), got.GetSecret())
	require.Equal(t, "standard-webhooks", got.GetScheme())
	require.Equal(t, "gitlab.example.com/acme/pay", got.GetRepositoryKey())
}

func TestAnUnknownHookStaysNotFound(t *testing.T) {
	c := newHooks(t, func(string) (*authv1.Hook, error) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such hook"))
	})

	_, err := c.Get(t.Context(), "hook-1")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestGetRefusesAnEmptyHookIdWithoutDialling(t *testing.T) {
	c := newHooks(t, func(string) (*authv1.Hook, error) {
		t.Fatal("an empty hook id must not reach the wire")
		return nil, nil
	})

	_, err := c.Get(t.Context(), "")
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestHooksRefusesAPoolForTheWrongProtocol(t *testing.T) {
	mn := newMocknet(t)

	c, err := Hooks(nil)
	require.Error(t, err)
	require.Nil(t, c)

	p, err := NewPool(newHost(t, mn), discovery.Static(), "/tau/build/v1", 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	c, err = Hooks(p)
	require.ErrorContains(t, err, "/tau/auth/v1")
	require.Nil(t, c)
}
