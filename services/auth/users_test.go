package auth

import (
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
)

func TestBuiltinAdminLogsInAndIsNeverStored(t *testing.T) {
	f := newFx(t)
	ctx := t.Context()

	resp, err := f.sessions().Login(ctx, req(&authv2.LoginRequest{Name: "ROOT", Password: f.adminP}, ""))
	require.NoError(t, err)
	sess := resp.Msg.GetSession()
	require.Equal(t, AdminID, sess.GetUser().GetId())
	require.True(t, sess.GetUser().GetBuiltin())
	require.Equal(t, authv2.Role_ROLE_ADMIN, sess.GetUser().GetRole())
	require.Equal(t, f.now.Add(DefaultAccessTTL).Unix(), sess.GetAccessExpiresUnix())

	// Nothing about the admin is a row.
	keys, err := f.db.List(ctx, prefixUsers)
	require.NoError(t, err)
	require.Empty(t, keys)

	// Wrong password, unknown name: the same refusal.
	_, err = f.sessions().Login(ctx, req(&authv2.LoginRequest{Name: "root", Password: "wrong password"}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
	_, err2 := f.sessions().Login(ctx, req(&authv2.LoginRequest{Name: "nobody", Password: "wrong password"}, ""))
	require.Equal(t, err.Error(), err2.Error())

	// Whoami answers for the token.
	who, err := f.sessions().Whoami(ctx, req(&authv2.WhoamiRequest{}, sess.GetAccessToken()))
	require.NoError(t, err)
	require.Equal(t, "root", who.Msg.GetName())

	// The admin is not managed through the API.
	for _, call := range []func() error{
		func() error {
			_, err := f.users().SetRole(ctx, req(&authv2.SetRoleRequest{Id: AdminID}, sess.GetAccessToken()))
			return err
		},
		func() error {
			_, err := f.users().SetDisabled(ctx, req(&authv2.SetDisabledRequest{Id: AdminID, Disabled: true}, sess.GetAccessToken()))
			return err
		},
		func() error {
			_, err := f.users().DeleteUser(ctx, req(&authv2.UserRequest{Id: AdminID}, sess.GetAccessToken()))
			return err
		},
		func() error {
			_, err := f.users().SetPassword(ctx, req(&authv2.SetPasswordRequest{Password: "a new password"}, sess.GetAccessToken()))
			return err
		},
		func() error {
			_, err := f.users().CreateToken(ctx, req(&authv2.CreateTokenRequest{Name: "ci"}, sess.GetAccessToken()))
			return err
		},
	} {
		require.Error(t, call())
	}
}

func TestDisabledAdmin(t *testing.T) {
	f := newFx(t)
	tok := f.adminToken()
	f.s.cfg.Admin.Disabled = true
	_, err := f.sessions().Login(t.Context(), req(&authv2.LoginRequest{Name: "root", Password: f.adminP}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
	// A token it already holds stops verifying too.
	_, err = f.sessions().Whoami(t.Context(), req(&authv2.WhoamiRequest{}, tok))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
}

func TestLocalUsers(t *testing.T) {
	f := newFx(t)
	ctx := t.Context()
	admin := f.adminToken()

	u, tok := f.local("ci-bot", authv2.Role_ROLE_MEMBER, authv2.Capability_CAPABILITY_LOGIN, authv2.Capability_CAPABILITY_APIKEY)
	require.Equal(t, "ci-bot", u.GetName())
	require.ElementsMatch(t, []authv2.Capability{authv2.Capability_CAPABILITY_LOGIN, authv2.Capability_CAPABILITY_APIKEY}, u.GetCapabilities())

	// The stored hash is peppered and names the pepper.
	var h passwordRow
	require.NoError(t, f.s.store.get(ctx, userKey(u.GetId(), fieldPassword), &h))
	require.Contains(t, h.Pepper, secretPepper+f.s.host.ID().String())
	require.Equal(t, cheap, h.Params)

	// A member is not an admin.
	_, err := f.users().ListUsers(ctx, req(&authv2.ListUsersRequest{}, tok))
	require.Equal(t, connect.CodePermissionDenied, code(t, err))

	// Names are unique, case-insensitively, and the admin's is taken.
	for _, name := range []string{"CI-BOT", "root"} {
		_, err = f.users().CreateUser(ctx, req(&authv2.CreateUserRequest{Name: name, Capabilities: []authv2.Capability{authv2.Capability_CAPABILITY_APIKEY}}, admin))
		require.Equal(t, connect.CodeAlreadyExists, code(t, err), name)
	}
	for _, bad := range []*authv2.CreateUserRequest{
		{Name: "Bad Name", Capabilities: []authv2.Capability{authv2.Capability_CAPABILITY_APIKEY}},
		{Name: "nocaps"},
		{Name: "weird", Capabilities: []authv2.Capability{9}},
		{Name: "shortpw", Capabilities: []authv2.Capability{authv2.Capability_CAPABILITY_LOGIN}, Password: "short"},
		{Name: "machine", Capabilities: []authv2.Capability{authv2.Capability_CAPABILITY_APIKEY}, Password: "no password here"},
		{Name: "mail", Email: "a/b@x", Capabilities: []authv2.Capability{authv2.Capability_CAPABILITY_APIKEY}},
	} {
		_, err = f.users().CreateUser(ctx, req(bad, admin))
		require.Equal(t, connect.CodeInvalidArgument, code(t, err), bad.GetName())
	}

	// A machine account cannot log in with a password.
	m, _ := f.local("machine", authv2.Role_ROLE_MEMBER, authv2.Capability_CAPABILITY_APIKEY)
	_, err = f.sessions().Login(ctx, req(&authv2.LoginRequest{Name: "machine", Password: "anything at all"}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
	_, err = f.users().SetPassword(ctx, req(&authv2.SetPasswordRequest{Id: m.GetId(), Password: "a new password"}, admin))
	require.Equal(t, connect.CodeFailedPrecondition, code(t, err))

	// A user changes their own password; the old one stops working.
	_, err = f.users().SetPassword(ctx, req(&authv2.SetPasswordRequest{Password: "a brand new password"}, tok))
	require.NoError(t, err)
	_, err = f.sessions().Login(ctx, req(&authv2.LoginRequest{Name: "ci-bot", Password: "ci-bot password"}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
	_, err = f.sessions().Login(ctx, req(&authv2.LoginRequest{Name: "ci-bot", Password: "a brand new password"}, ""))
	require.NoError(t, err)
	// But not someone else's.
	_, err = f.users().SetPassword(ctx, req(&authv2.SetPasswordRequest{Id: m.GetId(), Password: "a new password"}, tok))
	require.Equal(t, connect.CodePermissionDenied, code(t, err))

	// Promotion is read at verification: the same token is an admin's now.
	_, err = f.users().SetRole(ctx, req(&authv2.SetRoleRequest{Id: u.GetId(), Role: authv2.Role_ROLE_ADMIN}, admin))
	require.NoError(t, err)
	list, err := f.users().ListUsers(ctx, req(&authv2.ListUsersRequest{}, tok))
	require.NoError(t, err)
	require.Len(t, list.Msg.GetUsers(), 3)

	// Disabling takes effect at once, on the token already issued.
	_, err = f.users().SetDisabled(ctx, req(&authv2.SetDisabledRequest{Id: u.GetId(), Disabled: true}, admin))
	require.NoError(t, err)
	_, err = f.sessions().Whoami(ctx, req(&authv2.WhoamiRequest{}, tok))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
	_, err = f.sessions().Login(ctx, req(&authv2.LoginRequest{Name: "ci-bot", Password: "a brand new password"}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
	_, err = f.users().SetDisabled(ctx, req(&authv2.SetDisabledRequest{Id: u.GetId(), Disabled: false}, admin))
	require.NoError(t, err)
	_, err = f.sessions().Whoami(ctx, req(&authv2.WhoamiRequest{}, tok))
	require.NoError(t, err)

	// Deleting frees the name.
	_, err = f.users().DeleteUser(ctx, req(&authv2.UserRequest{Id: u.GetId()}, admin))
	require.NoError(t, err)
	_, err = f.sessions().Whoami(ctx, req(&authv2.WhoamiRequest{}, tok))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
	f.local("ci-bot", authv2.Role_ROLE_MEMBER, authv2.Capability_CAPABILITY_APIKEY)

	for _, id := range []string{"nope", "a/b", u.GetId()} {
		_, err = f.users().SetRole(ctx, req(&authv2.SetRoleRequest{Id: id}, admin))
		require.Equal(t, connect.CodeNotFound, code(t, err), id)
	}
}

// Two nodes creating one name at once both succeed, and every node resolves
// the name to the same, earlier, user.
func TestConcurrentNameClaimsResolveTheSameEverywhere(t *testing.T) {
	f := newFx(t)
	ctx := t.Context()
	first, _ := f.local("shared", authv2.Role_ROLE_MEMBER, authv2.Capability_CAPABILITY_LOGIN)

	// The second node's write, which its guard could not see.
	f.now = f.now.Add(time.Second)
	second := newID()
	require.NoError(t, f.s.store.put(ctx, userKey(second, fieldProfile), profileRow{Name: "shared", Local: true, Capabilities: []string{capLogin}, Created: f.now.UnixNano()}))
	require.NoError(t, f.s.store.put(ctx, nameKey("shared")+second, claim{Created: f.now.UnixNano()}))

	got, err := f.s.byName(ctx, "shared")
	require.NoError(t, err)
	require.Equal(t, first.GetId(), got.ID)

	// A tie on time goes to the lower id.
	var c claim
	require.NoError(t, f.s.store.get(ctx, nameKey("shared")+first.GetId(), &c))
	require.NoError(t, f.s.store.put(ctx, nameKey("shared")+second, c))
	got, err = f.s.byName(ctx, "shared")
	require.NoError(t, err)
	require.Equal(t, min(first.GetId(), second), got.ID)
}

func TestAPITokens(t *testing.T) {
	f := newFx(t)
	ctx := t.Context()
	admin := f.adminToken()
	_, tok := f.local("dev", authv2.Role_ROLE_MEMBER, authv2.Capability_CAPABILITY_LOGIN, authv2.Capability_CAPABILITY_APIKEY)
	machine, _ := f.local("robot", authv2.Role_ROLE_MEMBER, authv2.Capability_CAPABILITY_APIKEY)

	made, err := f.users().CreateToken(ctx, req(&authv2.CreateTokenRequest{Name: "laptop", TtlSeconds: 3600}, tok))
	require.NoError(t, err)
	secret := made.Msg.GetSecret()
	require.Regexp(t, `^tau_[0-9a-f]{24}_`, secret)

	// Only the digest is stored.
	var row apiKeyRow
	require.NoError(t, f.s.store.get(ctx, prefixAPIKeys+made.Msg.GetId(), &row))
	require.NotContains(t, string(row.Sum), secret)

	who, err := f.identity().Verify(ctx, req(&authv2.VerifyRequest{Token: secret}, ""))
	require.NoError(t, err)
	require.Equal(t, "dev", who.Msg.GetUser().GetName())
	require.Equal(t, made.Msg.GetId(), who.Msg.GetTokenId())

	// A token for a machine account, made by an admin.
	robot, err := f.users().CreateToken(ctx, req(&authv2.CreateTokenRequest{Name: "ci", User: machine.GetId()}, admin))
	require.NoError(t, err)
	_, err = f.identity().Verify(ctx, req(&authv2.VerifyRequest{Token: robot.Msg.GetSecret()}, ""))
	require.NoError(t, err)
	// A member cannot mint one for someone else.
	_, err = f.users().CreateToken(ctx, req(&authv2.CreateTokenRequest{User: machine.GetId()}, tok))
	require.Equal(t, connect.CodePermissionDenied, code(t, err))
	_, err = f.users().CreateToken(ctx, req(&authv2.CreateTokenRequest{User: "nope"}, admin))
	require.Equal(t, connect.CodeNotFound, code(t, err))

	// Listing: own, and an admin's view of another's; not a member's.
	list, err := f.users().ListTokens(ctx, req(&authv2.ListTokensRequest{}, tok))
	require.NoError(t, err)
	require.Len(t, list.Msg.GetTokens(), 1)
	require.Empty(t, list.Msg.GetTokens()[0].GetSecret())
	list, err = f.users().ListTokens(ctx, req(&authv2.ListTokensRequest{User: machine.GetId()}, admin))
	require.NoError(t, err)
	require.Len(t, list.Msg.GetTokens(), 1)
	_, err = f.users().ListTokens(ctx, req(&authv2.ListTokensRequest{User: machine.GetId()}, tok))
	require.Equal(t, connect.CodePermissionDenied, code(t, err))

	// A forged secret with a real id does not verify; nor does garbage.
	forged := secret[:len("tau_")+apiKeyIDLen+1] + "AAAA"
	for _, bad := range []string{forged, "tau_short", "tau_zzzzzzzzzzzzzzzzzzzzzzzz_x", "tau_000000000000000000000000_x"} {
		_, err = f.identity().Verify(ctx, req(&authv2.VerifyRequest{Token: bad}, ""))
		require.Equal(t, connect.CodeUnauthenticated, code(t, err), bad)
	}

	// Revoking: a member may not revoke another's token, but their own.
	_, err = f.users().RevokeToken(ctx, req(&authv2.RevokeTokenRequest{Id: robot.Msg.GetId()}, tok))
	require.Equal(t, connect.CodeNotFound, code(t, err))
	_, err = f.users().RevokeToken(ctx, req(&authv2.RevokeTokenRequest{Id: "nope"}, tok))
	require.Equal(t, connect.CodeNotFound, code(t, err))
	_, err = f.users().RevokeToken(ctx, req(&authv2.RevokeTokenRequest{Id: made.Msg.GetId()}, tok))
	require.NoError(t, err)
	_, err = f.identity().Verify(ctx, req(&authv2.VerifyRequest{Token: secret}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
	list, err = f.users().ListTokens(ctx, req(&authv2.ListTokensRequest{}, tok))
	require.NoError(t, err)
	require.Empty(t, list.Msg.GetTokens())

	// Expiry.
	short, err := f.users().CreateToken(ctx, req(&authv2.CreateTokenRequest{Name: "short", TtlSeconds: 60}, tok))
	require.NoError(t, err)
	require.Equal(t, f.now.Add(time.Minute).Unix(), short.Msg.GetExpiresUnix())
	f.now = f.now.Add(time.Minute)
	_, err = f.identity().Verify(ctx, req(&authv2.VerifyRequest{Token: short.Msg.GetSecret()}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))

	_, err = f.users().CreateToken(ctx, req(&authv2.CreateTokenRequest{Name: string(make([]byte, 129))}, tok))
	require.Equal(t, connect.CodeInvalidArgument, code(t, err))

	// A login-only local user may not hold API tokens.
	_, loginOnly := f.local("human", authv2.Role_ROLE_MEMBER, authv2.Capability_CAPABILITY_LOGIN)
	_, err = f.users().CreateToken(ctx, req(&authv2.CreateTokenRequest{Name: "x"}, loginOnly))
	require.Equal(t, connect.CodePermissionDenied, code(t, err))
}

func TestRefreshAndLogout(t *testing.T) {
	f := newFx(t)
	ctx := t.Context()
	f.local("dev", authv2.Role_ROLE_MEMBER, authv2.Capability_CAPABILITY_LOGIN)
	in, err := f.sessions().Login(ctx, req(&authv2.LoginRequest{Name: "dev", Password: "dev password"}, ""))
	require.NoError(t, err)
	sess := in.Msg.GetSession()
	require.Equal(t, f.now.Add(DefaultRefreshTTL).Unix(), sess.GetRefreshExpiresUnix())

	// The access token expires; the refresh token renews it.
	f.now = f.now.Add(DefaultAccessTTL + time.Minute)
	_, err = f.sessions().Whoami(ctx, req(&authv2.WhoamiRequest{}, sess.GetAccessToken()))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
	re, err := f.sessions().Refresh(ctx, req(&authv2.RefreshRequest{RefreshToken: sess.GetRefreshToken()}, ""))
	require.NoError(t, err)
	fresh := re.Msg.GetSession()
	require.Equal(t, sess.GetRefreshToken(), fresh.GetRefreshToken(), "a refresh token is not rotated")
	_, err = f.sessions().Whoami(ctx, req(&authv2.WhoamiRequest{}, fresh.GetAccessToken()))
	require.NoError(t, err)

	for _, bad := range []string{"", "nope", "taur_unknown"} {
		_, err = f.sessions().Refresh(ctx, req(&authv2.RefreshRequest{RefreshToken: bad}, ""))
		require.Equal(t, connect.CodeUnauthenticated, code(t, err), bad)
	}

	// Logout revokes the refresh token and the access token it carries.
	_, err = f.sessions().Logout(ctx, req(&authv2.LogoutRequest{RefreshToken: sess.GetRefreshToken()}, fresh.GetAccessToken()))
	require.NoError(t, err)
	_, err = f.sessions().Refresh(ctx, req(&authv2.RefreshRequest{RefreshToken: sess.GetRefreshToken()}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
	_, err = f.sessions().Whoami(ctx, req(&authv2.WhoamiRequest{}, fresh.GetAccessToken()))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
	// Logging out twice, or an unknown token, is not an error.
	_, err = f.sessions().Logout(ctx, req(&authv2.LogoutRequest{RefreshToken: "taur_unknown"}, ""))
	require.NoError(t, err)

	// A refresh token outlives nothing past its expiry.
	in, err = f.sessions().Login(ctx, req(&authv2.LoginRequest{Name: "dev", Password: "dev password"}, ""))
	require.NoError(t, err)
	f.now = f.now.Add(DefaultRefreshTTL)
	_, err = f.sessions().Refresh(ctx, req(&authv2.RefreshRequest{RefreshToken: in.Msg.GetSession().GetRefreshToken()}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
}

// A token signed on one node verifies on another, which holds a different
// key, because the kid resolves through the shared store.
func TestTokensVerifyAcrossNodes(t *testing.T) {
	f := newFx(t)
	tok := f.adminToken()
	other := newTestService(t, WithUsers(f.db, f.vault, f.s.cfg), WithForges(f.reg), WithClock(func() time.Time { return f.now }))
	who, err := (&sessionHandler{other}).Whoami(t.Context(), req(&authv2.WhoamiRequest{}, tok))
	require.NoError(t, err)
	require.Equal(t, AdminID, who.Msg.GetId())

	// And the other node signs with its own key.
	resp, err := (&sessionHandler{other}).Login(t.Context(), req(&authv2.LoginRequest{Name: "root", Password: f.adminP}, ""))
	require.NoError(t, err)
	k1, err := f.s.keys.Signer(t.Context(), "tau:test")
	require.NoError(t, err)
	k2, err := other.keys.Signer(t.Context(), "tau:test")
	require.NoError(t, err)
	require.NotEqual(t, k1.KeyID(), k2.KeyID())
	_, err = f.sessions().Whoami(t.Context(), req(&authv2.WhoamiRequest{}, resp.Msg.GetSession().GetAccessToken()))
	require.NoError(t, err)

	// A token from another cloud does not verify here.
	cfg := f.s.cfg
	cfg.Issuer = "tau:elsewhere"
	elsewhere := newTestService(t, WithUsers(f.db, f.vault, cfg), WithForges(f.reg), WithClock(func() time.Time { return f.now }))
	foreign, err := (&sessionHandler{elsewhere}).Login(t.Context(), req(&authv2.LoginRequest{Name: "root", Password: f.adminP}, ""))
	require.NoError(t, err)
	_, err = f.sessions().Whoami(t.Context(), req(&authv2.WhoamiRequest{}, foreign.Msg.GetSession().GetAccessToken()))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
}

func TestInvites(t *testing.T) {
	f := newFx(t)
	ctx := t.Context()
	admin := f.adminToken()
	_, member := f.local("dev", authv2.Role_ROLE_MEMBER, authv2.Capability_CAPABILITY_LOGIN)

	_, err := f.users().CreateInvite(ctx, req(&authv2.CreateInviteRequest{}, member))
	require.Equal(t, connect.CodePermissionDenied, code(t, err))
	_, err = f.users().CreateInvite(ctx, req(&authv2.CreateInviteRequest{Note: string(make([]byte, 257))}, admin))
	require.Equal(t, connect.CodeInvalidArgument, code(t, err))

	inv, err := f.users().CreateInvite(ctx, req(&authv2.CreateInviteRequest{Role: authv2.Role_ROLE_ADMIN, Note: "for alice"}, admin))
	require.NoError(t, err)
	require.NotEmpty(t, inv.Msg.GetCode())
	require.Equal(t, f.now.Add(DefaultInviteTTL).Unix(), inv.Msg.GetExpiresUnix())

	list, err := f.users().ListInvites(ctx, req(&authv2.Empty{}, admin))
	require.NoError(t, err)
	require.Len(t, list.Msg.GetInvites(), 1)
	require.Empty(t, list.Msg.GetInvites()[0].GetCode(), "a code is shown once")
	require.Equal(t, "for alice", list.Msg.GetInvites()[0].GetNote())

	// Redeemed by a pasted sign-in: the new user has the invite's role.
	f.forge.give("alice-token", identity("1", "alice"))
	in, err := f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: testHost, Token: "alice-token", Invite: inv.Msg.GetCode()}, ""))
	require.NoError(t, err)
	require.Equal(t, authv2.Role_ROLE_ADMIN, in.Msg.GetSession().GetUser().GetRole())

	// Used once, it admits no one else.
	f.forge.give("bob-token", identity("2", "bob"))
	_, err = f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: testHost, Token: "bob-token", Invite: inv.Msg.GetCode()}, ""))
	require.Equal(t, connect.CodePermissionDenied, code(t, err))

	// An expired invite is refused; a deleted one too.
	inv2, err := f.users().CreateInvite(ctx, req(&authv2.CreateInviteRequest{TtlSeconds: 60}, admin))
	require.NoError(t, err)
	f.now = f.now.Add(time.Minute)
	_, err = f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: testHost, Token: "bob-token", Invite: inv2.Msg.GetCode()}, ""))
	require.Equal(t, connect.CodePermissionDenied, code(t, err))
	admin = f.adminToken()
	inv3, err := f.users().CreateInvite(ctx, req(&authv2.CreateInviteRequest{}, admin))
	require.NoError(t, err)
	_, err = f.users().DeleteInvite(ctx, req(&authv2.UserRequest{Id: inv3.Msg.GetId()}, admin))
	require.NoError(t, err)
	_, err = f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: testHost, Token: "bob-token", Invite: inv3.Msg.GetCode()}, ""))
	require.Equal(t, connect.CodePermissionDenied, code(t, err))
	_, err = f.users().ListInvites(ctx, req(&authv2.Empty{}, member))
	require.Error(t, err)
	_, err = f.users().DeleteInvite(ctx, req(&authv2.UserRequest{}, member))
	require.Error(t, err)
}

func TestNoStoreAnswersAdmissionOnly(t *testing.T) {
	s := newTestService(t)
	_, err := (&sessionHandler{s}).Providers(t.Context(), req(&authv2.ProvidersRequest{}, ""))
	require.Equal(t, connect.CodeFailedPrecondition, code(t, err))
	_, err = (&userHandler{s}).ListUsers(t.Context(), req(&authv2.ListUsersRequest{}, "x"))
	require.Equal(t, connect.CodeFailedPrecondition, code(t, err))
	_, err = (&identityHandler{s}).Verify(t.Context(), req(&authv2.VerifyRequest{Token: "x"}, ""))
	require.Equal(t, connect.CodeFailedPrecondition, code(t, err))
	for _, h := range []func() error{
		func() error {
			_, err := (&sessionHandler{s}).Login(t.Context(), req(&authv2.LoginRequest{}, ""))
			return err
		},
		func() error {
			_, err := (&sessionHandler{s}).Refresh(t.Context(), req(&authv2.RefreshRequest{}, ""))
			return err
		},
		func() error {
			_, err := (&sessionHandler{s}).Logout(t.Context(), req(&authv2.LogoutRequest{}, ""))
			return err
		},
		func() error {
			_, err := (&sessionHandler{s}).Authorize(t.Context(), req(&authv2.AuthorizeRequest{}, ""))
			return err
		},
		func() error {
			_, err := (&sessionHandler{s}).Exchange(t.Context(), req(&authv2.ExchangeRequest{}, ""))
			return err
		},
		func() error {
			_, err := (&sessionHandler{s}).DeviceStart(t.Context(), req(&authv2.DeviceStartRequest{}, ""))
			return err
		},
		func() error {
			_, err := (&sessionHandler{s}).DevicePoll(t.Context(), req(&authv2.DevicePollRequest{}, ""))
			return err
		},
		func() error {
			_, err := (&sessionHandler{s}).Pasted(t.Context(), req(&authv2.PastedRequest{}, ""))
			return err
		},
		func() error {
			_, err := (&sessionHandler{s}).Whoami(t.Context(), req(&authv2.WhoamiRequest{}, ""))
			return err
		},
	} {
		require.Equal(t, connect.CodeFailedPrecondition, code(t, h()))
	}
}

func TestVaultOutageIsUnavailable(t *testing.T) {
	f := newFx(t)
	f.vault.fail = errors.New("no members")
	_, err := f.sessions().Login(t.Context(), req(&authv2.LoginRequest{Name: "root", Password: f.adminP}, ""))
	require.Equal(t, connect.CodeUnavailable, code(t, err))
	_, err = f.users().CreateUser(t.Context(), req(&authv2.CreateUserRequest{}, "x"))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
}

func TestCallerNeedsABearer(t *testing.T) {
	f := newFx(t)
	_, err := f.sessions().Whoami(t.Context(), req(&authv2.WhoamiRequest{}, ""))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
	_, err = f.sessions().Whoami(t.Context(), req(&authv2.WhoamiRequest{}, "garbage"))
	require.Equal(t, connect.CodeUnauthenticated, code(t, err))
}
