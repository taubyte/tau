package auth

import (
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
)

func hosts(cs []*authv2.Connection) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.GetHost())
	}
	return out
}

// Connecting a second forge while signed in keeps the first, is the same
// user, and retires the session the caller came with.
func TestConnectKeepsEveryConnection(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	ctx := t.Context()
	f.forge.give("gho_alice", identity("42", "alice"))
	plain, err := f.reg.Driver("plain.example")
	require.NoError(t, err)
	plain.(*testDriver).give("pat", identity("7", "alice-plain"))

	in, err := f.browserSignIn(testHost, loopback, "gho_alice", "")
	require.NoError(t, err)
	first := in.GetSession()

	both, err := f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: "plain.example", Token: "pat", RefreshToken: first.GetRefreshToken()}, first.GetAccessToken()))
	require.NoError(t, err)
	s := both.Msg.GetSession()
	require.Equal(t, first.GetUser().GetId(), s.GetUser().GetId())
	require.ElementsMatch(t, []string{testHost, "plain.example"}, hosts(s.GetConnections()))
	require.Equal(t, "gho_alice", f.open(s.GetAccessToken(), testHost).GetForge().GetAccessToken())
	require.Equal(t, "pat", f.open(s.GetAccessToken(), "plain.example").GetForge().GetAccessToken())

	// The unlinked identity is now the caller's: signing in with it finds them.
	u, err := f.s.linked(ctx, "plain.example", "7")
	require.NoError(t, err)
	require.Equal(t, first.GetUser().GetId(), u.ID)

	// The old session is retired, both halves.
	_, err = f.sessions().Refresh(ctx, req(&authv2.RefreshRequest{RefreshToken: first.GetRefreshToken()}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
	_, err = f.sessions().Whoami(ctx, req(&authv2.WhoamiRequest{}, first.GetAccessToken()))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))

	// The new refresh token carries both.
	re, err := f.sessions().Refresh(ctx, req(&authv2.RefreshRequest{RefreshToken: s.GetRefreshToken()}, ""))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{testHost, "plain.example"}, hosts(re.Msg.GetSession().GetConnections()))

	// Listing.
	ls, err := f.sessions().Connections(ctx, req(&authv2.ConnectionsRequest{}, s.GetAccessToken()))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{testHost, "plain.example"}, hosts(ls.Msg.GetConnections()))
	_, err = f.sessions().Connections(ctx, req(&authv2.ConnectionsRequest{}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))

	// Disconnecting drops one, keeps the other.
	one, err := f.sessions().Disconnect(ctx, req(&authv2.DisconnectRequest{Host: testHost, RefreshToken: s.GetRefreshToken()}, s.GetAccessToken()))
	require.NoError(t, err)
	require.Equal(t, []string{"plain.example"}, hosts(one.Msg.GetSession().GetConnections()))
	_, err = f.identity().Open(ctx, connect.NewRequest(&authv2.OpenRequest{Token: one.Msg.GetSession().GetAccessToken(), Host: testHost}))
	require.Equal(t, connect.CodeNotFound, code(t, err))
	_, err = f.sessions().Disconnect(ctx, req(&authv2.DisconnectRequest{Host: testHost}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
}

// Connecting replaces the connection to the same host.
func TestConnectReplacesItsHost(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	ctx := t.Context()
	f.forge.give("one", identity("42", "alice"))
	f.forge.give("two", identity("42", "alice"))
	in, err := f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: testHost, Token: "one"}, ""))
	require.NoError(t, err)
	again, err := f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: testHost, Token: "two"}, in.Msg.GetSession().GetAccessToken()))
	require.NoError(t, err)
	require.Len(t, again.Msg.GetSession().GetConnections(), 1)
	require.Equal(t, "two", f.open(again.Msg.GetSession().GetAccessToken(), testHost).GetForge().GetAccessToken())
}

// A token that does not verify is refused, never ignored in favour of a
// fresh sign-in.
func TestConnectWithABadTokenIsRefused(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	f.forge.give("one", identity("42", "alice"))
	_, err := f.sessions().Pasted(t.Context(), req(&authv2.PastedRequest{Provider: testHost, Token: "one"}, "garbage"))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
}

// An API token holds no connection; connecting with one starts a session.
func TestConnectWithAnAPIToken(t *testing.T) {
	f := newFx(t)
	ctx := t.Context()
	u, _ := f.local("bot", authv2.Role_ROLE_MEMBER, authv2.Capability_CAPABILITY_APIKEY)
	tok, err := f.users().CreateToken(ctx, req(&authv2.CreateTokenRequest{Name: "ci", User: u.GetId()}, f.adminToken()))
	require.NoError(t, err)
	key := tok.Msg.GetSecret()
	_, err = f.identity().Open(ctx, connect.NewRequest(&authv2.OpenRequest{Token: key, Host: testHost}))
	require.Equal(t, connect.CodeNotFound, code(t, err))

	f.forge.give("gho_bot", identity("99", "bot"))
	in, err := f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: testHost, Token: "gho_bot"}, key))
	require.NoError(t, err)
	require.Equal(t, u.GetId(), in.Msg.GetSession().GetUser().GetId())
	require.Equal(t, "gho_bot", f.open(in.Msg.GetSession().GetAccessToken(), testHost).GetForge().GetAccessToken())
	// The API token is not revoked by it.
	_, err = f.sessions().Whoami(ctx, req(&authv2.WhoamiRequest{}, key))
	require.NoError(t, err)
}

// An expiring forge credential is renewed where it is used, and the renewal
// rides into the next Refresh through the newest access token.
func TestOpenRenewsAnExpiringConnection(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen; c.AccessTTL = 2 * time.Hour })
	ctx := t.Context()
	f.forge.give("gho_alice", identity("42", "alice"))
	in, err := f.browserSignIn(testHost, loopback, "gho_alice", "")
	require.NoError(t, err)
	s := in.GetSession()

	o := f.open(s.GetAccessToken(), testHost)
	require.Empty(t, o.GetRenewedToken(), "a fresh credential is not renewed")

	f.now = f.now.Add(time.Hour)
	o = f.open(s.GetAccessToken(), testHost)
	require.Equal(t, "renewed-forge-refresh", o.GetForge().GetAccessToken())
	require.NotEmpty(t, o.GetRenewedToken())
	require.Equal(t, "renewed-forge-refresh", f.open(o.GetRenewedToken(), testHost).GetForge().GetAccessToken())
	require.Equal(t, "refresh_token", f.oauth.lastForm.Get("grant_type"))
	require.Equal(t, "client-secret", f.oauth.lastForm.Get("client_secret"))

	// The refresh token alone still holds the old credential; with the newest
	// access token, even expired, Refresh keeps the renewal.
	f.now = f.now.Add(3 * time.Hour)
	re, err := f.sessions().Refresh(ctx, req(&authv2.RefreshRequest{RefreshToken: s.GetRefreshToken(), AccessToken: o.GetRenewedToken()}, ""))
	require.NoError(t, err)
	got := re.Msg.GetSession()
	require.NotEqual(t, s.GetRefreshToken(), got.GetRefreshToken(), "the refresh token carries the newer seal")
	require.Equal(t, strings.SplitN(s.GetRefreshToken(), ".", 2)[0], strings.SplitN(got.GetRefreshToken(), ".", 2)[0], "the session is the same")
	conns, err := f.s.connections(ctx, mustVerify(t, f, got.GetAccessToken()))
	require.NoError(t, err)
	require.Equal(t, "renewed-forge-refresh", conns[0].GetAccessToken())

	// An access token of another session does not lend its seal.
	other, err := f.browserSignIn(testHost, loopback, "gho_alice", "")
	require.NoError(t, err)
	re, err = f.sessions().Refresh(ctx, req(&authv2.RefreshRequest{RefreshToken: s.GetRefreshToken(), AccessToken: other.GetSession().GetAccessToken()}, ""))
	require.NoError(t, err)
	require.Equal(t, s.GetRefreshToken(), re.Msg.GetSession().GetRefreshToken())
}

// A credential the forge cannot renew is a reason to connect again.
func TestOpenAnExpiredConnectionThatCannotRenew(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen; c.AccessTTL = 2 * time.Hour })
	ctx := t.Context()
	f.forge.give("gho_alice", identity("42", "alice"))
	in, err := f.browserSignIn(testHost, loopback, "gho_alice", "")
	require.NoError(t, err)
	access := in.GetSession().GetAccessToken()

	// The forge refuses the refresh token.
	f.oauth.secret = "rotated"
	f.now = f.now.Add(time.Hour)
	_, err = f.identity().Open(ctx, connect.NewRequest(&authv2.OpenRequest{Token: access, Host: testHost}))
	require.Equal(t, connect.CodeFailedPrecondition, code(t, err))

	// No refresh token at all.
	conns, err := f.s.connections(ctx, mustVerify(t, f, access))
	require.NoError(t, err)
	require.Equal(t, connect.CodeFailedPrecondition, openStripped(t, f, access, conns))
}

func mustVerify(t *testing.T, f *fx, raw string) principal {
	p, err := f.s.verify(t.Context(), raw)
	require.NoError(t, err)
	return p
}

// openStripped re-seals p's connections without refresh tokens and opens one.
func openStripped(t *testing.T, f *fx, raw string, conns []*authv2.ForgeToken) connect.Code {
	p := mustVerify(t, f, raw)
	for _, c := range conns {
		c.RefreshToken = ""
	}
	row, err := f.s.session(t.Context(), p.session)
	require.NoError(t, err)
	sealed, err := f.s.seal(t.Context(), &authv2.Sealed{User: p.user.ID, Session: p.session, SealedUnixNano: f.now.UnixNano(), Connections: conns})
	require.NoError(t, err)
	sess, err := f.s.access(t.Context(), p.user, p.session, sealed, conns, time.Unix(0, row.Expires))
	require.NoError(t, err)
	_, err = f.identity().Open(t.Context(), connect.NewRequest(&authv2.OpenRequest{Token: sess.GetAccessToken(), Host: conns[0].GetHost()}))
	return code(t, err)
}

// A seal opens only for its own session, and only under a sealing key.
func TestSealIsBound(t *testing.T) {
	f := newFx(t)
	ctx := t.Context()
	raw, err := f.s.seal(ctx, &authv2.Sealed{User: "u", Session: "s"})
	require.NoError(t, err)
	_, err = f.s.unseal(ctx, raw, "u", "s")
	require.NoError(t, err)
	_, err = f.s.unseal(ctx, raw, "u", "other")
	require.ErrorIs(t, err, errSeal)
	_, err = f.s.unseal(ctx, raw, "v", "s")
	require.ErrorIs(t, err, errSeal)
	_, err = f.s.unseal(ctx, "not.a.jwe", "u", "s")
	require.ErrorIs(t, err, errSeal)
	// Tampered ciphertext.
	parts := strings.Split(raw, ".")
	parts[3] = "AAAA" + parts[3][4:]
	_, err = f.s.unseal(ctx, strings.Join(parts, "."), "u", "s")
	require.ErrorIs(t, err, errSeal)
	_, err = f.s.keys.SealKeyNamed(ctx, "pepper-x")
	require.Error(t, err)

	// The node that sealed is not the only one that opens: another auth node
	// over the same store and vault opens it, with its own key for new seals.
	g := newTestService(t, WithUsers(f.db, f.vault, f.s.cfg), WithForges(f.reg), WithClock(func() time.Time { return f.now }))
	_, err = g.unseal(ctx, raw, "u", "s")
	require.NoError(t, err)
	theirs, err := g.seal(ctx, &authv2.Sealed{User: "u", Session: "s"})
	require.NoError(t, err)
	_, err = f.s.unseal(ctx, theirs, "u", "s")
	require.NoError(t, err)
	a, _, err := f.s.keys.SealKey(ctx)
	require.NoError(t, err)
	b, _, err := g.keys.SealKey(ctx)
	require.NoError(t, err)
	require.NotEqual(t, a, b, "each node seals with its own key")
}

// An identity another user holds is connected without being re-linked.
func TestConnectLeavesAnotherUsersLink(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	ctx := t.Context()
	f.forge.give("bob", identity("1", "bob"))
	bob, err := f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: testHost, Token: "bob"}, ""))
	require.NoError(t, err)
	_, carol := f.local("carol", authv2.Role_ROLE_MEMBER, authv2.Capability_CAPABILITY_LOGIN)
	in, err := f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: testHost, Token: "bob"}, carol))
	require.NoError(t, err)
	require.Equal(t, "carol", in.Msg.GetSession().GetUser().GetName())
	u, err := f.s.linked(ctx, testHost, "1")
	require.NoError(t, err)
	require.Equal(t, bob.Msg.GetSession().GetUser().GetId(), u.ID)
	// Carol cannot retire Bob's session by naming his refresh token.
	_, err = f.sessions().Disconnect(ctx, req(&authv2.DisconnectRequest{Host: testHost, RefreshToken: bob.Msg.GetSession().GetRefreshToken()}, in.Msg.GetSession().GetAccessToken()))
	require.NoError(t, err)
	_, err = f.sessions().Refresh(ctx, req(&authv2.RefreshRequest{RefreshToken: bob.Msg.GetSession().GetRefreshToken()}, ""))
	require.NoError(t, err)
}

func TestConnectionsNeedUsers(t *testing.T) {
	s := newTestService(t)
	h := &sessionHandler{s}
	_, err := h.Disconnect(t.Context(), req(&authv2.DisconnectRequest{}, ""))
	require.ErrorIs(t, err, errNoUsers)
	_, err = h.Connections(t.Context(), req(&authv2.ConnectionsRequest{}, ""))
	require.ErrorIs(t, err, errNoUsers)
	_, err = (&identityHandler{s}).Open(t.Context(), connect.NewRequest(&authv2.OpenRequest{}))
	require.ErrorIs(t, err, errNoUsers)
}

func TestExpiryReadsTheServiceClock(t *testing.T) {
	f := newFx(t)
	require.Zero(t, f.s.expiry(&oauth2.Token{}))
	at := time.Unix(2_000_000_000, 0)
	require.Equal(t, at.Unix(), f.s.expiry(&oauth2.Token{Expiry: at}))
	require.Equal(t, f.now.Unix()+60, f.s.expiry(&oauth2.Token{ExpiresIn: 60, Expiry: at}))
}

func TestOpenRefusesABadToken(t *testing.T) {
	f := newFx(t)
	_, err := f.identity().Open(t.Context(), connect.NewRequest(&authv2.OpenRequest{Token: "x", Host: testHost}))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
}

func TestOpenConnectionIsTheCallersOwn(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	f.forge.give("gho_alice", identity("42", "alice"))
	in, err := f.sessions().Pasted(t.Context(), req(&authv2.PastedRequest{Provider: testHost, Token: "gho_alice"}, ""))
	require.NoError(t, err)
	o, err := f.sessions().OpenConnection(t.Context(), req(&authv2.OpenConnectionRequest{Host: testHost}, in.Msg.GetSession().GetAccessToken()))
	require.NoError(t, err)
	require.Equal(t, "gho_alice", o.Msg.GetForge().GetAccessToken())
	_, err = f.sessions().OpenConnection(t.Context(), req(&authv2.OpenConnectionRequest{Host: testHost}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
	_, err = (&sessionHandler{newTestService(t)}).OpenConnection(t.Context(), req(&authv2.OpenConnectionRequest{}, ""))
	require.ErrorIs(t, err, errNoUsers)
}
