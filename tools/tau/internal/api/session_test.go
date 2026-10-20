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
func jwt(sid string, gen, iat, exp int64) string {
	b, _ := json.Marshal(map[string]any{"sid": sid, "tau_gen": gen, "iat": iat, "exp": exp})
	return "e30." + base64.RawURLEncoding.EncodeToString(b) + ".x"
}

func TestRefreshingRenewsBeforeUseAndKeepsRenewals(t *testing.T) {
	var header string
	f := &fakeSessions{}
	mux := http.NewServeMux()
	mux.Handle(authv2connect.NewSessionServiceHandler(f))
	mux.HandleFunc("GET /job/{jid}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(RenewedTokenHeader, header)
		w.Write([]byte(`{"id":"j"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	anon, err := New(srv.URL, nil, srv.Client())
	require.NoError(t, err)
	var saved []string
	now := int64(1000)
	old := jwt("s1", 0, 900, 1030)
	r := &Refreshing{Token: old, Expires: 1030, Refresh: "taur_1", Sessions: anon.Anonymous(), Now: func() int64 { return now },
		Save: func(token string, _ int64, _ string) { saved = append(saved, token) }}
	c, err := New(srv.URL, r, srv.Client())
	require.NoError(t, err)

	// About to expire: renewed before use, with the token it holds.
	header = ""
	_, err = c.Job(t.Context(), "j")
	require.NoError(t, err)
	require.Equal(t, []*authv2.RefreshRequest{{RefreshToken: "taur_1", AccessTokens: []string{old}}}, stripped(f.refreshed))
	require.Equal(t, []string{"fresh"}, saved)

	// THE NEWEST BY THE CLOUD'S ORDER is kept, not the last to arrive.
	r.Token, r.Expires = jwt("s1", 0, 1000, 1900), 1900
	a := jwt("s1", 1, 1001, 1901) // renewed github.com
	b := jwt("s1", 1, 1002, 1902) // renewed gitlab.com, from the same token
	header = b
	_, err = c.Job(t.Context(), "j")
	require.NoError(t, err)
	require.Equal(t, b, r.Token)
	require.EqualValues(t, 1902, r.Expires)
	header = a
	_, err = c.Job(t.Context(), "j")
	require.NoError(t, err)
	require.Equal(t, b, r.Token, "an older renewal arriving later does not replace a newer one")
	header = jwt("s0", 9, 2000, 2900)
	_, err = c.Job(t.Context(), "j")
	require.NoError(t, err)
	require.Equal(t, b, r.Token, "a token of another session is a stale answer")
	header = "not-a-jwt"
	_, err = c.Job(t.Context(), "j")
	require.NoError(t, err)
	require.Equal(t, b, r.Token)

	// EVERY RENEWAL rides into the next refresh, so the cloud merges them.
	now = 1900
	f.refreshed = nil
	_, err = r.Access(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{b, b, a}, f.refreshed[0].GetAccessTokens())
	require.Equal(t, "fresh", r.Token)
	require.Empty(t, r.renewals, "a refresh clears them")

	// The renewals kept are bounded.
	r.Token = jwt("s2", 0, 1, 2)
	for i := range maxRenewals + 3 {
		r.Renewed(jwt("s2", int64(i+1), 1, 2))
	}
	require.Len(t, r.renewals, maxRenewals)

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
	Static("x").Renewed("y")
}

func stripped(in []*authv2.RefreshRequest) []*authv2.RefreshRequest {
	out := make([]*authv2.RefreshRequest, 0, len(in))
	for _, r := range in {
		out = append(out, &authv2.RefreshRequest{RefreshToken: r.GetRefreshToken(), AccessTokens: r.GetAccessTokens()})
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

func TestSupersedes(t *testing.T) {
	a := jwt("s", 1, 10, 0)
	require.True(t, Supersedes(a, ""))
	require.True(t, Supersedes(a, "not-a-jwt"))
	require.False(t, Supersedes("not-a-jwt", a))
	require.True(t, Supersedes(jwt("s", 2, 1, 0), a))
	require.True(t, Supersedes(jwt("s", 1, 11, 0), a))
	require.False(t, Supersedes(a, a))
	require.False(t, Supersedes(jwt("other", 9, 99, 0), a))
}
