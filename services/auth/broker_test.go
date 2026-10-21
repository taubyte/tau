package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"connectrpc.com/connect"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
)

const loopback = "http://127.0.0.1:53111/callback"

func binding() (verifier, challengeOf string) {
	v := randomToken()
	return v, challenge(v)
}

func (f *fx) browserSignIn(provider, redirect, token, invite string) (*authv2.SignedIn, error) {
	f.t.Helper()
	v, c := binding()
	az, err := f.sessions().Authorize(f.t.Context(), req(&authv2.AuthorizeRequest{Provider: provider, RedirectUri: redirect, BindingChallenge: c, Invite: invite}, ""))
	require.NoError(f.t, err)
	code, state := f.oauth.approve(az.Msg.GetUrl(), token)
	require.Equal(f.t, az.Msg.GetState(), state)
	resp, err := f.sessions().Exchange(f.t.Context(), req(&authv2.ExchangeRequest{State: state, Code: code, BindingVerifier: v}, ""))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func TestForgeBrowserSignIn(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	f.forge.give("gho_alice", identity("42", "alice"))

	in, err := f.browserSignIn(testHost, loopback, "gho_alice", "")
	require.NoError(t, err)
	// The tau session and the forge's own credential come back, apart.
	require.Equal(t, "alice", in.GetSession().GetUser().GetName())
	require.Equal(t, "alice@example.com", in.GetSession().GetUser().GetEmail())
	require.Equal(t, []*authv2.Link{{Issuer: testHost, Subject: "42", Login: "alice"}}, in.GetSession().GetUser().GetLinks())
	require.NotContains(t, in.GetSession().GetAccessToken(), "gho_alice")
	require.NotContains(t, in.GetSession().GetRefreshToken(), "gho_alice")
	require.Equal(t, &authv2.Connection{Host: testHost, Subject: "42", Login: "alice", ExpiresUnix: f.now.Unix() + 3600, Scopes: []string{"repo", "read:user"}, Refreshable: true, SignIn: authv2.SignInLink_SIGN_IN_LINK_THIS_USER}, in.GetConnection())
	ft := in.GetForge()
	require.Equal(t, testHost, ft.GetHost())
	require.Equal(t, "gho_alice", ft.GetAccessToken())
	require.Equal(t, "forge-refresh", ft.GetRefreshToken())
	require.Equal(t, []string{"repo", "read:user"}, ft.GetScopes())
	require.NotZero(t, ft.GetExpiresUnix())

	// The redemption carried the client secret and the PKCE verifier.
	require.Equal(t, "client-1", f.oauth.lastForm.Get("client_id"))
	require.NotEmpty(t, f.oauth.lastForm.Get("code_verifier"))

	// The cloud keeps no forge token.
	keys, err := f.db.List(t.Context(), "/")
	require.NoError(t, err)
	for _, k := range keys {
		v, err := f.db.Get(t.Context(), k)
		require.NoError(t, err)
		require.NotContains(t, string(v), "gho_alice", k)
	}

	// Signing in again is the same user.
	again, err := f.browserSignIn(testHost, "https://console.example/auth/callback", "gho_alice", "")
	require.NoError(t, err)
	require.Equal(t, in.GetSession().GetUser().GetId(), again.GetSession().GetUser().GetId())
}

func TestExchangeIsBoundToTheClientThatStartedIt(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	f.forge.give("gho_alice", identity("42", "alice"))
	ctx := t.Context()

	v, c := binding()
	az, err := f.sessions().Authorize(ctx, req(&authv2.AuthorizeRequest{Provider: testHost, RedirectUri: loopback, BindingChallenge: c}, ""))
	require.NoError(t, err)
	code, state := f.oauth.approve(az.Msg.GetUrl(), "gho_alice")

	// Someone who intercepted the redirect has the code and the state, but not
	// the binding secret.
	_, err = f.sessions().Exchange(ctx, req(&authv2.ExchangeRequest{State: state, Code: code, BindingVerifier: "guess"}, ""))
	require.Equal(t, connect.CodePermissionDenied, code2(t, err))

	// The client that started it still can, once.
	_, err = f.sessions().Exchange(ctx, req(&authv2.ExchangeRequest{State: state, Code: code, BindingVerifier: v}, ""))
	require.NoError(t, err)
	_, err = f.sessions().Exchange(ctx, req(&authv2.ExchangeRequest{State: state, Code: code, BindingVerifier: v}, ""))
	require.Equal(t, connect.CodeInvalidArgument, code2(t, err))

	// An unknown state; an expired one.
	_, err = f.sessions().Exchange(ctx, req(&authv2.ExchangeRequest{State: "nope", Code: code, BindingVerifier: v}, ""))
	require.Equal(t, connect.CodeInvalidArgument, code2(t, err))
	v, c = binding()
	az, err = f.sessions().Authorize(ctx, req(&authv2.AuthorizeRequest{Provider: testHost, RedirectUri: loopback, BindingChallenge: c}, ""))
	require.NoError(t, err)
	code, state = f.oauth.approve(az.Msg.GetUrl(), "gho_alice")
	f.now = f.now.Add(pendingTTL)
	_, err = f.sessions().Exchange(ctx, req(&authv2.ExchangeRequest{State: state, Code: code, BindingVerifier: v}, ""))
	require.Equal(t, connect.CodeInvalidArgument, code2(t, err))
}

func code2(t *testing.T, err error) connect.Code { return code(t, err) }

func TestExchangeRefusals(t *testing.T) {
	f := newFx(t)
	f.forge.give("gho_alice", identity("42", "alice"))
	ctx := t.Context()

	// Invite-only: a stranger is refused.
	_, err := f.browserSignIn(testHost, loopback, "gho_alice", "")
	require.Equal(t, connect.CodePermissionDenied, code(t, err))

	// The forge refuses the code (a wrong secret).
	f.oauth.secret = "rotated"
	_, err = f.browserSignIn(testHost, loopback, "gho_alice", "")
	require.Equal(t, connect.CodePermissionDenied, code(t, err))
	f.oauth.secret = "client-secret"

	// The forge's identity face is down.
	f.forge.broken = true
	_, err = f.browserSignIn(testHost, loopback, "gho_alice", "")
	require.Equal(t, connect.CodeUnavailable, code(t, err))
	f.forge.broken = false

	// The client secret is not in the vault.
	f2 := newFx(t)
	delete(f2.vault.m, VaultProject+"/"+VaultEnvironment+"/"+OAuthSecretName(testHost))
	_, err = f2.browserSignIn(testHost, loopback, "gho_alice", "")
	require.Equal(t, connect.CodeUnavailable, code(t, err))

	// The provider's client id changed between the two halves.
	v, c := binding()
	az, err := f.sessions().Authorize(ctx, req(&authv2.AuthorizeRequest{Provider: testHost, RedirectUri: loopback, BindingChallenge: c}, ""))
	require.NoError(t, err)
	cd, state := f.oauth.approve(az.Msg.GetUrl(), "gho_alice")
	f.s.cfg.Forges[0].ClientID = "client-2"
	_, err = f.sessions().Exchange(ctx, req(&authv2.ExchangeRequest{State: state, Code: cd, BindingVerifier: v}, ""))
	require.Equal(t, connect.CodeInvalidArgument, code(t, err))
}

func TestAuthorizeRefusals(t *testing.T) {
	f := newFx(t)
	ctx := t.Context()
	_, c := binding()
	for _, redirect := range []string{
		"http://example.com/callback",
		"http://127.0.0.1/callback",
		"http://127.0.0.1:5000/other",
		"http://127.0.0.1:5000/callback?x=1",
		"http://localhost:5000/callback",
		"https://127.0.0.1:5000/callback",
		"http://user@127.0.0.1:5000/callback",
		"https://console.example/auth/callback/extra",
		"https://console.example",
		"::",
	} {
		_, err := f.sessions().Authorize(ctx, req(&authv2.AuthorizeRequest{Provider: testHost, RedirectUri: redirect, BindingChallenge: c}, ""))
		require.Equal(t, connect.CodeInvalidArgument, code(t, err), redirect)
	}
	for _, ch := range []string{"", "short", "!!!"} {
		_, err := f.sessions().Authorize(ctx, req(&authv2.AuthorizeRequest{Provider: testHost, RedirectUri: loopback, BindingChallenge: ch}, ""))
		require.Equal(t, connect.CodeInvalidArgument, code(t, err), ch)
	}
	for _, p := range []string{"plain.example", "nobody.example", ""} {
		_, err := f.sessions().Authorize(ctx, req(&authv2.AuthorizeRequest{Provider: p, RedirectUri: loopback, BindingChallenge: c}, ""))
		require.Equal(t, connect.CodeInvalidArgument, code(t, err), p)
	}
	// IPv6 loopback is a loopback.
	_, err := f.sessions().Authorize(ctx, req(&authv2.AuthorizeRequest{Provider: testHost, RedirectUri: "http://[::1]:4000/callback", BindingChallenge: c}, ""))
	require.NoError(t, err)
}

func TestProviders(t *testing.T) {
	f := newFx(t, func(c *Config) {
		c.OIDC = []OIDCProvider{{Name: "corp", Issuer: "https://idp.example", ClientID: "c"}}
	})
	resp, err := f.sessions().Providers(t.Context(), req(&authv2.ProvidersRequest{}, ""))
	require.NoError(t, err)
	require.True(t, resp.Msg.GetPassword())
	byID := map[string]*authv2.Provider{}
	for _, p := range resp.Msg.GetProviders() {
		byID[p.GetId()] = p
	}
	require.Len(t, byID, 3)
	require.True(t, byID[testHost].GetBrowser() && byID[testHost].GetDevice() && byID[testHost].GetPasted())
	require.True(t, byID["plain.example"].GetPasted())
	require.False(t, byID["plain.example"].GetBrowser())
	require.Equal(t, authv2.ProviderKind_PROVIDER_KIND_OIDC, byID["corp"].GetKind())
}

func TestPastedSignIn(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	ctx := t.Context()
	// A forge with no OAuth app still signs people in with a token they paste:
	// its identity face is mandatory.
	plain, err := f.reg.Driver("plain.example")
	require.NoError(t, err)
	plain.(*testDriver).give("pat", identity("7", "carol"))
	in, err := f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: "plain.example", Token: "pat"}, ""))
	require.NoError(t, err)
	require.Equal(t, "carol", in.Msg.GetSession().GetUser().GetName())
	require.Equal(t, "pat", in.Msg.GetForge().GetAccessToken())

	_, err = f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: "plain.example", Token: "wrong"}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
	_, err = f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: "plain.example"}, ""))
	require.Equal(t, connect.CodeInvalidArgument, code(t, err))
	_, err = f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: "nobody.example", Token: "pat"}, ""))
	require.Equal(t, connect.CodeInvalidArgument, code(t, err))
	plain.(*testDriver).give("anon", identity("", "x"))
	_, err = f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: "plain.example", Token: "anon"}, ""))
	require.Equal(t, connect.CodeUnavailable, code(t, err))
}

// Two users can never share a forge identity silently: a link is a claim of
// its own, and the earlier one wins on every node.
func TestDoubleLinkResolvesToTheEarlier(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	ctx := t.Context()
	f.forge.give("tok", identity("42", "alice"))
	in, err := f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: testHost, Token: "tok"}, ""))
	require.NoError(t, err)
	first := in.Msg.GetSession().GetUser().GetId()

	// Another node, concurrently, created a second user for the same identity.
	f.now = f.now.Add(time.Second)
	second, err := f.s.create(ctx, newUser{Name: "alice"})
	require.NoError(t, err)
	require.NoError(t, f.s.link(ctx, second.ID, testHost, "42", "alice"))

	got, err := f.s.linked(ctx, testHost, "42")
	require.NoError(t, err)
	require.Equal(t, first, got.ID)

	// When the earlier is deleted, the identity falls to the other.
	_, err = f.users().DeleteUser(ctx, req(&authv2.UserRequest{Id: first}, f.adminToken()))
	require.NoError(t, err)
	got, err = f.s.linked(ctx, testHost, "42")
	require.NoError(t, err)
	require.Equal(t, second.ID, got.ID)
}

func TestDeviceSignIn(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	ctx := t.Context()
	f.forge.give("gho_dev", identity("9", "dana"))

	start, err := f.sessions().DeviceStart(ctx, req(&authv2.DeviceStartRequest{Provider: testHost}, ""))
	require.NoError(t, err)
	require.Equal(t, "ABCD-EFGH", start.Msg.GetUserCode())
	require.EqualValues(t, 1, start.Msg.GetIntervalSeconds())

	// Approved while the poll waits.
	go func() {
		time.Sleep(200 * time.Millisecond)
		f.oauth.approveDevice(start.Msg.GetDeviceCode(), "gho_dev")
	}()
	poll, err := f.sessions().DevicePoll(ctx, req(&authv2.DevicePollRequest{Provider: testHost, DeviceCode: start.Msg.GetDeviceCode()}, ""))
	require.NoError(t, err)
	require.False(t, poll.Msg.GetPending())
	require.Equal(t, "dana", poll.Msg.GetSignedIn().GetSession().GetUser().GetName())
	require.Equal(t, "gho_dev", poll.Msg.GetSignedIn().GetForge().GetAccessToken())

	// The code is spent.
	_, err = f.sessions().DevicePoll(ctx, req(&authv2.DevicePollRequest{Provider: testHost, DeviceCode: start.Msg.GetDeviceCode()}, ""))
	require.Equal(t, connect.CodeInvalidArgument, code(t, err))

	// Denied.
	start, err = f.sessions().DeviceStart(ctx, req(&authv2.DeviceStartRequest{Provider: testHost}, ""))
	require.NoError(t, err)
	f.oauth.mu.Lock()
	f.oauth.devices[start.Msg.GetDeviceCode()].denied = true
	f.oauth.mu.Unlock()
	_, err = f.sessions().DevicePoll(ctx, req(&authv2.DevicePollRequest{Provider: testHost, DeviceCode: start.Msg.GetDeviceCode()}, ""))
	require.Equal(t, connect.CodePermissionDenied, code(t, err))

	// Another provider's code, and no device flow at all.
	_, err = f.sessions().DevicePoll(ctx, req(&authv2.DevicePollRequest{Provider: "plain.example", DeviceCode: start.Msg.GetDeviceCode()}, ""))
	require.Equal(t, connect.CodeInvalidArgument, code(t, err))
	f.forge.oauth.DeviceURL = ""
	_, err = f.sessions().DeviceStart(ctx, req(&authv2.DeviceStartRequest{Provider: testHost}, ""))
	require.Equal(t, connect.CodeInvalidArgument, code(t, err))
}

func TestDevicePollPending(t *testing.T) {
	defer func(d time.Duration) { pollFor = d }(pollFor)
	pollFor = 1500 * time.Millisecond
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	start, err := f.sessions().DeviceStart(t.Context(), req(&authv2.DeviceStartRequest{Provider: testHost}, ""))
	require.NoError(t, err)
	poll, err := f.sessions().DevicePoll(t.Context(), req(&authv2.DevicePollRequest{Provider: testHost, DeviceCode: start.Msg.GetDeviceCode()}, ""))
	require.NoError(t, err)
	require.True(t, poll.Msg.GetPending())
}

// ─────────────────────────────── OIDC ───────────────────────────────

type idp struct {
	key *ecdsa.PrivateKey
	url string
}

func (f *fx) withOIDC(t *testing.T) *idp {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	p := &idp{key: key, url: f.oauth.srv.URL}
	mux := f.oauth.srv.Config.Handler.(*http.ServeMux)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer": p.url, "authorization_endpoint": p.url + "/authorize", "token_endpoint": p.url + "/token", "jwks_uri": p.url + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k", Algorithm: "ES256", Use: "sig"}}})
	})
	f.oauth.idToken = func(nonce string) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
			"iss": p.url, "sub": "corp-sub-1", "aud": "oidc-client", "nonce": nonce,
			"iat": f.now.Unix(), "exp": f.now.Add(time.Hour).Unix(),
			"email": "Erin@Corp.example", "email_verified": true, "preferred_username": "erin",
		})
		tok.Header["kid"] = "k"
		s, err := tok.SignedString(key)
		require.NoError(t, err)
		return s
	}
	f.s.cfg.OIDC = []OIDCProvider{{Name: "corp", Issuer: p.url, ClientID: "oidc-client"}}
	f.vault.m[VaultProject+"/"+VaultEnvironment+"/"+OIDCSecretName("corp")] = []byte(f.oauth.secret)
	return p
}

func TestOIDCSignIn(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	p := f.withOIDC(t)

	v, c := binding()
	az, err := f.sessions().Authorize(t.Context(), req(&authv2.AuthorizeRequest{Provider: "corp", RedirectUri: loopback, BindingChallenge: c}, ""))
	require.NoError(t, err)
	u, err := url.Parse(az.Msg.GetUrl())
	require.NoError(t, err)
	require.Contains(t, u.Query().Get("scope"), "openid")
	require.NotEmpty(t, u.Query().Get("nonce"))

	cd, state := f.oauth.approve(az.Msg.GetUrl(), "opaque-access")
	in, err := f.sessions().Exchange(t.Context(), req(&authv2.ExchangeRequest{State: state, Code: cd, BindingVerifier: v}, ""))
	require.NoError(t, err)
	user := in.Msg.GetSession().GetUser()
	require.Equal(t, "erin", user.GetName())
	require.Equal(t, "erin@corp.example", user.GetEmail())
	require.Equal(t, []*authv2.Link{{Issuer: p.url, Subject: "corp-sub-1", Login: "erin"}}, user.GetLinks())
	require.Nil(t, in.Msg.GetForge(), "an OIDC sign-in obtains no forge credential")
	require.Nil(t, in.Msg.GetConnection())

	// A token whose nonce is not the one sent is refused.
	f.oauth.idToken = func(string) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{"iss": p.url, "sub": "x", "aud": "oidc-client", "nonce": "replayed", "iat": f.now.Unix(), "exp": f.now.Add(time.Hour).Unix()})
		tok.Header["kid"] = "k"
		s, _ := tok.SignedString(p.key)
		return s
	}
	_, err = f.browserSignIn("corp", loopback, "x", "")
	require.Equal(t, connect.CodePermissionDenied, code(t, err))
}

func TestOIDCDiscoveryFailure(t *testing.T) {
	f := newFx(t)
	f.s.cfg.OIDC = []OIDCProvider{{Name: "down", Issuer: f.oauth.srv.URL + "/nothing", ClientID: "c"}}
	_, c := binding()
	_, err := f.sessions().Authorize(t.Context(), req(&authv2.AuthorizeRequest{Provider: "down", RedirectUri: loopback, BindingChallenge: c}, ""))
	require.Equal(t, connect.CodeUnavailable, code(t, err))
	var ce *connect.Error
	require.True(t, errors.As(err, &ce))
}
