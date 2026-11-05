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

func (fakeIdentity) Verify(_ context.Context, req *connect.Request[authv2.VerifyRequest]) (*connect.Response[authv2.Principal], error) {
	if req.Msg.GetToken() != "tok" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("revoked"))
	}
	return connect.NewResponse(&authv2.Principal{User: &authv2.User{Id: "user-1"}}), nil
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

	user, err := c.Caller(t.Context(), "tok")
	require.NoError(t, err)
	require.Equal(t, "user-1", user)
	_, err = c.Caller(t.Context(), "bad")
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	_, err = Identities(nil)
	require.Error(t, err)
}
