package clients

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/discovery"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

type fakeSessions struct {
	authv2connect.UnimplementedSessionServiceHandler
}

func (fakeSessions) Whoami(_ context.Context, req *connect.Request[authv2.WhoamiRequest]) (*connect.Response[authv2.User], error) {
	return connect.NewResponse(&authv2.User{Name: req.Header().Get("Authorization")}), nil
}

func (fakeSessions) Providers(context.Context, *connect.Request[authv2.ProvidersRequest]) (*connect.Response[authv2.ProvidersResponse], error) {
	return connect.NewResponse(&authv2.ProvidersResponse{Providers: []*authv2.Provider{{Id: strings.Repeat("x", maxAuthAnswer)}}}), nil
}

func TestAuthCallsReachAuthUnread(t *testing.T) {
	mn := newMocknet(t)
	caller := newHost(t, mn)
	server := newHost(t, mn)
	mux := http.NewServeMux()
	mux.Handle(authv2connect.NewSessionServiceHandler(fakeSessions{}))
	serve(t, server, AuthProtocol, mux)

	p, err := NewPool(caller, discovery.Static(server.ID()), AuthProtocol, 2)
	require.NoError(t, err)
	t.Cleanup(p.Close)
	a, err := AuthCalls(p)
	require.NoError(t, err)

	h := http.Header{"Content-Type": {"application/json"}, "Authorization": {"Bearer t"}}
	status, rh, body, err := a.Call(t.Context(), authv2connect.SessionServiceWhoamiProcedure, h, []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "application/json", rh.Get("Content-Type"))
	require.JSONEq(t, `{"name":"Bearer t"}`, string(body))

	// A Connect error is an answer, carried as one.
	status, _, _, err = a.Call(t.Context(), authv2connect.SessionServiceLoginProcedure, h, []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusNotImplemented, status)

	// An answer too large is refused.
	_, _, _, err = a.Call(t.Context(), authv2connect.SessionServiceProvidersProcedure, h, []byte(`{}`))
	require.Error(t, err)

	_, err = AuthCalls(nil)
	require.Error(t, err)

	// Nobody serving: unavailable on every peer.
	mn2 := newMocknet(t)
	lonely := newHost(t, mn2)
	gone := newHost(t, mn2)
	p2, err := NewPool(lonely, discovery.Static(gone.ID()), AuthProtocol, 1)
	require.NoError(t, err)
	t.Cleanup(p2.Close)
	a2, err := AuthCalls(p2)
	require.NoError(t, err)
	_, _, _, err = a2.Call(t.Context(), authv2connect.SessionServiceWhoamiProcedure, h, nil)
	require.Error(t, err)
}
