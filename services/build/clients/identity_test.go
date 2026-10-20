package clients

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/discovery"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

type fakeIdentity struct {
	authv2connect.UnimplementedIdentityServiceHandler
}

func (fakeIdentity) Admit(_ context.Context, req *connect.Request[authv2.Caller]) (*connect.Response[authv2.Admission], error) {
	if req.Msg.GetSubject() == "broken" {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("down"))
	}
	return connect.NewResponse(&authv2.Admission{
		Admitted: req.Msg.GetSubject() == "1",
		Reason:   "sign up first",
		Accounts: []string{req.Msg.GetForge() + "/" + req.Msg.GetLogin()},
	}), nil
}

func (fakeIdentity) Open(_ context.Context, req *connect.Request[authv2.OpenRequest]) (*connect.Response[authv2.Opened], error) {
	if req.Msg.GetHost() != "github.com" {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("not connected"))
	}
	return connect.NewResponse(&authv2.Opened{Forge: &authv2.ForgeToken{AccessToken: "gho_" + req.Msg.GetToken()}}), nil
}

func TestIdentitiesCarriesAuthsAnswer(t *testing.T) {
	mn := newMocknet(t)
	caller := newHost(t, mn)
	server := newHost(t, mn)
	mux := http.NewServeMux()
	mux.Handle(authv2connect.NewIdentityServiceHandler(fakeIdentity{}))
	serve(t, server, AuthProtocol, mux)

	p, err := NewPool(caller, discovery.Static(server.ID()), AuthProtocol, 2)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	c, err := Identities(p)
	require.NoError(t, err)

	got, err := c.Admit(t.Context(), &authv2.Caller{Forge: "github.com", Subject: "1", Login: "ada"})
	require.NoError(t, err)
	require.True(t, got.GetAdmitted())
	require.Equal(t, []string{"github.com/ada"}, got.GetAccounts())

	got, err = c.Admit(t.Context(), &authv2.Caller{Forge: "github.com", Subject: "2"})
	require.NoError(t, err)
	require.False(t, got.GetAdmitted())
	require.Equal(t, "sign up first", got.GetReason())

	_, err = c.Admit(t.Context(), &authv2.Caller{Forge: "github.com", Subject: "broken"})
	require.Error(t, err)

	o, err := c.Open(t.Context(), "tau", "github.com")
	require.NoError(t, err)
	require.Equal(t, "gho_tau", o.GetForge().GetAccessToken())
	_, err = c.Open(t.Context(), "tau", "gitlab.com")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	_, err = Identities(nil)
	require.Error(t, err)
}
