package node

import (
	"testing"

	"connectrpc.com/connect"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
	authService "github.com/taubyte/tau/services/auth"
)

func TestTheTenancyIsCanonicalAndCheckedAtConstruction(t *testing.T) {
	c, err := Config{Services: []string{Discovery}, Tenancy: []string{"GitHub.com/Acme", "github.com/acme"}}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, []string{"github.com/acme"}, c.Tenancy)

	_, err = Config{Services: []string{Discovery}, Tenancy: []string{"acme"}}.withDefaults()
	require.ErrorContains(t, err, "not a namespace")
}

func TestANodesAuthRefusesARepositoryOutsideItsTenancy(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)
	n, err := New(Config{Services: []string{Auth}, Tenancy: []string{"GitHub.com/Acme"}, Auth: authService.Config{DomainValidation: keyPair(t)}},
		WithHost(h), WithDatabases(&memFactory{}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	require.NoError(t, n.Start(t.Context()))

	client, err := rpc.NewClient(newHost(t, mn), h.ID(), authService.Protocol)
	require.NoError(t, err)
	defer client.Close()
	repos := authv2connect.NewRepositoryServiceClient(client.HTTP(), client.URL())

	_, err = repos.Register(t.Context(), connect.NewRequest(&authv2.RepositoryRef{RepositoryKey: "github.com/other/pay"}))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	require.ErrorContains(t, err, "github.com/other/pay is outside this cloud's tenancy; it serves github.com/acme")

	_, err = repos.Register(t.Context(), connect.NewRequest(&authv2.RepositoryRef{RepositoryKey: "github.com/acme/pay"}))
	require.NoError(t, err)

	projects := authv2connect.NewProjectServiceClient(client.HTTP(), client.URL())
	_, err = projects.Create(t.Context(), connect.NewRequest(&authv2.CreateProjectRequest{Name: "p", ConfigRepositoryKey: "github.com/acme/config", CodeRepositoryKey: "github.com/other/code"}))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = projects.Create(t.Context(), connect.NewRequest(&authv2.CreateProjectRequest{Name: "p", ConfigRepositoryKey: "github.com/acme/config", CodeRepositoryKey: "github.com/acme/code"}))
	require.NoError(t, err)
}
