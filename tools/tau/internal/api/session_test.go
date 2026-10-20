package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

type fakeSessions struct {
	authv2connect.UnimplementedSessionServiceHandler
	refreshed []*authv2.RefreshRequest
	refuse    bool
	broken    bool
}

func (f *fakeSessions) Refresh(_ context.Context, req *connect.Request[authv2.RefreshRequest]) (*connect.Response[authv2.SignedIn], error) {
	f.refreshed = append(f.refreshed, req.Msg)
	switch {
	case f.refuse:
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("sign in again"))
	case f.broken:
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("down"))
	}
	return connect.NewResponse(&authv2.SignedIn{Session: &authv2.Session{AccessToken: "fresh", AccessExpiresUnix: 2000, RefreshToken: "taur_2"}}), nil
}

func (f *fakeSessions) OpenConnection(_ context.Context, req *connect.Request[authv2.OpenConnectionRequest]) (*connect.Response[authv2.Opened], error) {
	if req.Header().Get("Authorization") != "Bearer fresh" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no"))
	}
	if req.Msg.GetHost() != "github.com" {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("not connected"))
	}
	return connect.NewResponse(&authv2.Opened{Forge: &authv2.ForgeToken{AccessToken: "gho"}, RenewedToken: "fresher"}), nil
}

func TestRefreshingRenewsBeforeUseAndKeepsRenewals(t *testing.T) {
	f := &fakeSessions{}
	mux := http.NewServeMux()
	mux.Handle(authv2connect.NewSessionServiceHandler(f))
	mux.HandleFunc("GET /job/{jid}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(RenewedTokenHeader, "via-header")
		w.Write([]byte(`{"id":"j"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	anon, err := New(srv.URL, nil, srv.Client())
	require.NoError(t, err)
	var saved []string
	now := int64(1000)
	r := &Refreshing{Token: "old", Expires: 1030, Refresh: "taur_1", Sessions: anon.Anonymous(), Now: func() int64 { return now },
		Save: func(token string, _ int64, _ string) { saved = append(saved, token) }}
	c, err := New(srv.URL, r, srv.Client())
	require.NoError(t, err)

	got, err := c.OpenConnection(t.Context(), "github.com")
	require.NoError(t, err)
	require.Equal(t, "gho", got)
	require.Equal(t, []*authv2.RefreshRequest{{RefreshToken: "taur_1", AccessToken: "old"}}, stripped(f.refreshed))
	require.Equal(t, []string{"fresh", "fresher"}, saved)

	_, err = c.OpenConnection(t.Context(), "gitlab.com")
	require.Error(t, err, "fresher is not a token the fake takes")

	r.Token = "fresh"
	_, err = c.OpenConnection(t.Context(), "gitlab.com")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	_, err = c.Job(t.Context(), "j")
	require.NoError(t, err)
	require.Equal(t, "via-header", r.Token)

	// Refused is signed out; unreachable is said as it is.
	r.Expires, f.refuse = 1, true
	_, err = r.Access(t.Context())
	require.True(t, IsStatus(err, http.StatusUnauthorized))
	f.refuse, f.broken = false, true
	_, err = r.Access(t.Context())
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	_, err = c.Job(t.Context(), "j")
	require.Error(t, err)

	// Nothing to renew with: the token as it is.
	r.Refresh = ""
	tok, err := r.Access(t.Context())
	require.NoError(t, err)
	require.Equal(t, "via-header", tok)

	require.NotNil(t, c.Users())
	Static("x").Renewed("y")
}

func stripped(in []*authv2.RefreshRequest) []*authv2.RefreshRequest {
	out := make([]*authv2.RefreshRequest, 0, len(in))
	for _, r := range in {
		out = append(out, &authv2.RefreshRequest{RefreshToken: r.GetRefreshToken(), AccessToken: r.GetAccessToken()})
	}
	return out
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("offline")
}

func TestATransportThatFails(t *testing.T) {
	c, err := New("http://127.0.0.1:1", Static("t"), &http.Client{Transport: failingTransport{}})
	require.NoError(t, err)
	_, err = c.Job(t.Context(), "j")
	require.ErrorContains(t, err, "offline")
}
