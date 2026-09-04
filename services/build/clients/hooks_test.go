package clients

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/discovery"
	authv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/auth/v1"
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

func githubHook(id, secret, key string) *authv1.Hook {
	return &authv1.Hook{Hook: &authv1.Hook_Github{
		Github: &authv1.GithubHook{Id: id, Secret: secret, RepositoryKey: key},
	}}
}

// The pair is what comes back, because the pair decides an authentication: the
// secret verifies the body and the key says which repository it is good for.
// v1 fetched only the first of them.
func TestGetReturnsTheSecretAndTheRepositoryTogether(t *testing.T) {
	var asked string
	c := newHooks(t, func(id string) (*authv1.Hook, error) {
		asked = id
		return githubHook(id, "s3cret", "github.com/acme/pay"), nil
	})

	got, err := c.Get(t.Context(), "hook-1")

	require.NoError(t, err)
	require.Equal(t, "hook-1", asked)
	require.Equal(t, "s3cret", got.GetSecret())
	require.Equal(t, "github.com/acme/pay", got.GetRepositoryKey())
}

func TestAnUnknownHookStaysNotFound(t *testing.T) {
	c := newHooks(t, func(string) (*authv1.Hook, error) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such hook"))
	})

	_, err := c.Get(t.Context(), "hook-1")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// auth's Hook is a oneof whose bitbucket case is reachable in the schema and
// unreachable in the running service. A hook of another provider has no secret
// to verify with, and answering it as a decided refusal is what keeps the
// frontend from treating an absent secret as an empty one.
func TestAHookOfAnotherProviderIsARefusalAndNotANilSecret(t *testing.T) {
	for _, hook := range []*authv1.Hook{
		{},
		{Hook: &authv1.Hook_Bitbucket{Bitbucket: &authv1.BitbucketHook{Id: "hook-1"}}},
	} {
		c := newHooks(t, func(string) (*authv1.Hook, error) { return hook, nil })

		got, err := c.Get(t.Context(), "hook-1")

		require.Nil(t, got)
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	}
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
