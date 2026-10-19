package auth

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// accounts is a fake identity: subject "1" is linked, anything else is not.
type accounts struct{ err error }

func (a accounts) Admit(_ context.Context, c Caller) (Admission, error) {
	if a.err != nil {
		return Admission{}, a.err
	}
	if c.Subject != "1" {
		return Admission{Reason: "no account is linked to " + c.Login}, nil
	}
	return Admission{Admitted: true, Accounts: []string{"acct-" + c.Forge}}, nil
}

func identityClient(t *testing.T, opts ...Option) authv2connect.IdentityServiceClient {
	t.Helper()
	h := newWireHarness(t, opts...)
	client := dialAuth(t, h.CallerHost, h.ServerHost)
	return authv2connect.NewIdentityServiceClient(client.HTTP(), client.URL())
}

func TestAuthAdmitsWhoeverTheForgeVouchedForByDefault(t *testing.T) {
	got, err := identityClient(t).Admit(t.Context(), connect.NewRequest(&authv2.Caller{Forge: "github.com", Subject: "42"}))
	require.NoError(t, err)
	require.True(t, got.Msg.GetAdmitted())
}

func TestAComposedIdentityDecidesAdmission(t *testing.T) {
	c := identityClient(t, WithIdentity(accounts{}))

	got, err := c.Admit(t.Context(), connect.NewRequest(&authv2.Caller{Forge: "github.com", Subject: "1", Login: "ada"}))
	require.NoError(t, err)
	require.True(t, got.Msg.GetAdmitted())
	require.Equal(t, []string{"acct-github.com"}, got.Msg.GetAccounts())

	got, err = c.Admit(t.Context(), connect.NewRequest(&authv2.Caller{Forge: "github.com", Subject: "2", Login: "bob"}))
	require.NoError(t, err)
	require.False(t, got.Msg.GetAdmitted())
	require.Equal(t, "no account is linked to bob", got.Msg.GetReason())

	_, err = c.Admit(t.Context(), connect.NewRequest(&authv2.Caller{Forge: "github.com"}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestAnIdentityThatCannotAnswerIsUnavailable(t *testing.T) {
	c := identityClient(t, WithIdentity(accounts{err: errors.New("accounts unreachable")}))
	_, err := c.Admit(t.Context(), connect.NewRequest(&authv2.Caller{Forge: "github.com", Subject: "1"}))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

func TestANilIdentityIsRefused(t *testing.T) {
	mn := newMocknet(t)
	_, err := New(newHost(t, mn), WithIdentity(nil))
	require.Error(t, err)
}
