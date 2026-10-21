package auth

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
)

// A forge sign-in made with a bearer hands back the credential and its
// connection, links the unheld identity, and leaves the caller's session be.
func TestConnectWithABearerReturnsNoSession(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	ctx := t.Context()
	f.forge.give("gho_alice", identity("42", "alice"))
	plain, err := f.reg.Driver("plain.example")
	require.NoError(t, err)
	plain.(*testDriver).give("pat", identity("7", "alice-plain"))

	in, err := f.browserSignIn(testHost, loopback, "gho_alice", "")
	require.NoError(t, err)
	first := in.GetSession()

	got, err := f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: "plain.example", Token: "pat"}, first.GetAccessToken()))
	require.NoError(t, err)
	require.Nil(t, got.Msg.GetSession())
	require.Equal(t, "pat", got.Msg.GetForge().GetAccessToken())
	require.Equal(t, "plain.example", got.Msg.GetForge().GetHost())
	require.Equal(t, &authv2.Connection{Host: "plain.example", Subject: "7", Login: "alice-plain", Scopes: []string{}, SignIn: authv2.SignInLink_SIGN_IN_LINK_THIS_USER}, got.Msg.GetConnection())

	u, err := f.s.linked(ctx, "plain.example", "7")
	require.NoError(t, err)
	require.Equal(t, first.GetUser().GetId(), u.ID)

	// The caller's session is untouched, both halves.
	_, err = f.sessions().Refresh(ctx, req(&authv2.RefreshRequest{RefreshToken: first.GetRefreshToken()}, ""))
	require.NoError(t, err)
	_, err = f.sessions().Whoami(ctx, req(&authv2.WhoamiRequest{}, first.GetAccessToken()))
	require.NoError(t, err)
}

// A token that does not verify is refused, never ignored in favour of a
// fresh sign-in.
func TestConnectWithABadTokenIsRefused(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	f.forge.give("one", identity("42", "alice"))
	_, err := f.sessions().Pasted(t.Context(), req(&authv2.PastedRequest{Provider: testHost, Token: "one"}, "garbage"))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
}

// Connecting with an API token links its user and revokes nothing.
func TestConnectWithAnAPIToken(t *testing.T) {
	f := newFx(t)
	ctx := t.Context()
	u, _ := f.local("bot", authv2.Role_ROLE_MEMBER, authv2.Capability_CAPABILITY_APIKEY)
	tok, err := f.users().CreateToken(ctx, req(&authv2.CreateTokenRequest{Name: "ci", User: u.GetId()}, f.adminToken()))
	require.NoError(t, err)
	key := tok.Msg.GetSecret()

	f.forge.give("gho_bot", identity("99", "bot"))
	in, err := f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: testHost, Token: "gho_bot"}, key))
	require.NoError(t, err)
	require.Nil(t, in.Msg.GetSession())
	require.Equal(t, "gho_bot", in.Msg.GetForge().GetAccessToken())
	linked, err := f.s.linked(ctx, testHost, "99")
	require.NoError(t, err)
	require.Equal(t, u.GetId(), linked.ID)
	_, err = f.sessions().Whoami(ctx, req(&authv2.WhoamiRequest{}, key))
	require.NoError(t, err)
}

// An identity another user holds is connected without being re-linked, and
// the connection says so.
func TestConnectLeavesAnotherUsersLink(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	ctx := t.Context()
	f.forge.give("bob", identity("1", "bob"))
	bob, err := f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: testHost, Token: "bob"}, ""))
	require.NoError(t, err)
	require.Equal(t, authv2.SignInLink_SIGN_IN_LINK_THIS_USER, bob.Msg.GetConnection().GetSignIn())
	_, carol := f.local("carol", authv2.Role_ROLE_MEMBER, authv2.Capability_CAPABILITY_LOGIN)
	in, err := f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: testHost, Token: "bob"}, carol))
	require.NoError(t, err)
	require.Equal(t, authv2.SignInLink_SIGN_IN_LINK_OTHER_USER, in.Msg.GetConnection().GetSignIn())
	u, err := f.s.linked(ctx, testHost, "1")
	require.NoError(t, err)
	require.Equal(t, bob.Msg.GetSession().GetUser().GetId(), u.ID)

	require.NoError(t, f.s.remove(ctx, u))
	d, err := f.s.describe(ctx, "carol", &authv2.ForgeToken{Host: testHost, Subject: "1"})
	require.NoError(t, err)
	require.Equal(t, authv2.SignInLink_SIGN_IN_LINK_NONE, d.GetSignIn())
}

func TestExpiryReadsTheServiceClock(t *testing.T) {
	f := newFx(t)
	require.Zero(t, f.s.expiry(&oauth2.Token{}))
	at := time.Unix(2_000_000_000, 0)
	require.Equal(t, at.Unix(), f.s.expiry(&oauth2.Token{Expiry: at}))
	require.Equal(t, f.now.Unix()+60, f.s.expiry(&oauth2.Token{ExpiresIn: 60, Expiry: at}))
}

// Renew refreshes the client's credential with the cloud's OAuth app.
func TestRenew(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	ctx := t.Context()
	f.forge.give("gho_alice", identity("42", "alice"))
	in, err := f.browserSignIn(testHost, loopback, "gho_alice", "")
	require.NoError(t, err)
	access := in.GetSession().GetAccessToken()
	require.Equal(t, "forge-refresh", in.GetForge().GetRefreshToken())

	got, err := f.sessions().Renew(ctx, req(&authv2.RenewRequest{Host: testHost, RefreshToken: "forge-refresh"}, access))
	require.NoError(t, err)
	require.Equal(t, "renewed-forge-refresh", got.Msg.GetAccessToken())
	require.Equal(t, "forge-refresh", got.Msg.GetRefreshToken())
	require.Equal(t, testHost, got.Msg.GetHost())
	require.Equal(t, f.now.Unix()+3600, got.Msg.GetExpiresUnix())
	require.Equal(t, []string{"repo", "read:user"}, got.Msg.GetScopes())
	require.Equal(t, "refresh_token", f.oauth.lastForm.Get("grant_type"))
	require.Equal(t, "client-secret", f.oauth.lastForm.Get("client_secret"))

	// A forge that rotates hands back a new refresh token; the spent one is
	// then refused with the words the caller acts on.
	f.oauth.rotate = true
	rot, err := f.sessions().Renew(ctx, req(&authv2.RenewRequest{Host: testHost, RefreshToken: "forge-refresh"}, access))
	require.NoError(t, err)
	require.Equal(t, "forge-refresh-1", rot.Msg.GetRefreshToken())
	_, err = f.sessions().Renew(ctx, req(&authv2.RenewRequest{Host: testHost, RefreshToken: "forge-refresh"}, access))
	require.Equal(t, connect.CodeFailedPrecondition, code(t, err))
	require.Contains(t, err.Error(), "reconnect "+testHost)
	require.Contains(t, err.Error(), "rotated")

	_, err = f.sessions().Renew(ctx, req(&authv2.RenewRequest{Host: "nowhere.example", RefreshToken: "x"}, access))
	require.Equal(t, connect.CodeFailedPrecondition, code(t, err))
	_, err = f.sessions().Renew(ctx, req(&authv2.RenewRequest{Host: testHost}, access))
	require.Equal(t, connect.CodeInvalidArgument, code(t, err))
	_, err = f.sessions().Renew(ctx, req(&authv2.RenewRequest{Host: testHost, RefreshToken: "forge-refresh"}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
	_, err = (&sessionHandler{newTestService(t)}).Renew(ctx, req(&authv2.RenewRequest{}, ""))
	require.ErrorIs(t, err, errNoUsers)
}

// Renewal needs the OAuth app's secret.
func TestRenewWithoutTheSecret(t *testing.T) {
	f := newFx(t)
	delete(f.vault.m, vaultProject+"/"+vaultEnvironment+"/"+OAuthSecretName(testHost))
	_, tok := f.local("dave", authv2.Role_ROLE_MEMBER, authv2.Capability_CAPABILITY_LOGIN)
	_, err := f.sessions().Renew(t.Context(), req(&authv2.RenewRequest{Host: testHost, RefreshToken: "forge-refresh"}, tok))
	require.Equal(t, connect.CodeUnavailable, code(t, err))
}

// Signing out retires the session the access token came from, too.
func TestLogoutRetiresTheCallersSession(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	ctx := t.Context()
	f.forge.give("gho_alice", identity("42", "alice"))
	in, err := f.browserSignIn(testHost, loopback, "gho_alice", "")
	require.NoError(t, err)
	s := in.GetSession()
	re, err := f.sessions().Refresh(ctx, req(&authv2.RefreshRequest{RefreshToken: s.GetRefreshToken()}, ""))
	require.NoError(t, err)
	require.Equal(t, s.GetRefreshToken(), re.Msg.GetSession().GetRefreshToken())
	_, err = f.sessions().Logout(ctx, req(&authv2.LogoutRequest{}, s.GetAccessToken()))
	require.NoError(t, err)
	_, err = f.sessions().Whoami(ctx, req(&authv2.WhoamiRequest{}, re.Msg.GetSession().GetAccessToken()))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err), "a sibling access token of the session stops too")
	_, err = f.sessions().Refresh(ctx, req(&authv2.RefreshRequest{RefreshToken: s.GetRefreshToken()}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
	_, err = f.sessions().Refresh(ctx, req(&authv2.RefreshRequest{RefreshToken: "garbage"}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
}
