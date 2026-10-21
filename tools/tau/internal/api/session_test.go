package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

// jwt is a token shaped like a tau access token, signed by nothing.
func jwt(sid string, iat int64) string {
	b, _ := json.Marshal(map[string]any{"sid": sid, "iat": iat})
	return "e30." + base64.RawURLEncoding.EncodeToString(b) + ".x"
}

func TestRefreshingRenewsBeforeUse(t *testing.T) {
	f := &fakeSessions{}
	mux := http.NewServeMux()
	mux.Handle(authv2connect.NewSessionServiceHandler(f))
	mux.HandleFunc("GET /job/{jid}", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"j"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	anon, err := New(srv.URL, nil, srv.Client())
	require.NoError(t, err)
	var saved []string
	now := int64(1000)
	r := &Refreshing{Token: jwt("s1", 900), Expires: 1030, Refresh: "taur_1", Sessions: anon.Anonymous(), Now: func() int64 { return now },
		Save: func(token string, _ int64, _ string) { saved = append(saved, token) }}
	c, err := New(srv.URL, r, srv.Client())
	require.NoError(t, err)

	// About to expire: renewed before use.
	_, err = c.Job(t.Context(), "j")
	require.NoError(t, err)
	require.Len(t, f.refreshed, 1)
	require.Equal(t, "taur_1", f.refreshed[0].GetRefreshToken())
	require.Equal(t, []string{"fresh"}, saved)
	require.EqualValues(t, 2000, r.Expires)

	// Not about to expire: used as it is.
	_, err = c.Job(t.Context(), "j")
	require.NoError(t, err)
	require.Len(t, f.refreshed, 1)

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
	require.Equal(t, r.Token, tok)

	require.NotNil(t, c.Users())
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

func TestSupersedes(t *testing.T) {
	a := jwt("s", 10)
	require.True(t, Supersedes(a, ""))
	require.True(t, Supersedes(a, "not-a-jwt"))
	require.False(t, Supersedes("not-a-jwt", a))
	require.True(t, Supersedes(jwt("s", 11), a))
	require.False(t, Supersedes(a, a))
	require.False(t, Supersedes(jwt("other", 99), a))
}
