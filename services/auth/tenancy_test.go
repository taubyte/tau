package auth

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/tenancy"
)

func acmeOnly(t *testing.T) Option {
	t.Helper()
	ten, err := tenancy.Parse([]string{"github.com/acme"})
	require.NoError(t, err)
	return WithTenancy(ten)
}

func TestARepositoryOutsideTheTenancyIsNotRegistered(t *testing.T) {
	h := &repositoryHandler{newTestService(t, acmeOnly(t))}
	_, err := h.Register(t.Context(), connect.NewRequest(&authv2.RepositoryRef{RepositoryKey: "github.com/other/pay"}))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	require.ErrorContains(t, err, "github.com/other/pay is outside this cloud's tenancy; it serves github.com/acme")

	// Inside it, Register goes on to ask who the caller is — the next check.
	_, err = h.Register(t.Context(), connect.NewRequest(&authv2.RepositoryRef{RepositoryKey: "github.com/acme/pay"}))
	require.NotEqual(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestAProjectIsNotLinkedToARepositoryOutsideTheTenancy(t *testing.T) {
	h := &projectHandler{newTestService(t, acmeOnly(t))}
	for _, req := range []*authv2.CreateProjectRequest{
		{Name: "p", ConfigRepositoryKey: "github.com/other/config", CodeRepositoryKey: "github.com/acme/code"},
		{Name: "p", ConfigRepositoryKey: "github.com/acme/config", CodeRepositoryKey: "gitlab.com/acme/code"},
	} {
		_, err := h.Create(t.Context(), connect.NewRequest(req))
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		require.ErrorContains(t, err, "outside this cloud's tenancy")
	}

	res, err := h.Create(t.Context(), connect.NewRequest(&authv2.CreateProjectRequest{Name: "p", ConfigRepositoryKey: "github.com/Acme/config", CodeRepositoryKey: "github.com/acme/code"}))
	require.NoError(t, err)
	require.NotEmpty(t, res.Msg.GetId())
}
