package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/services/auth/password"
)

func TestConfigDefaultsAndRefusals(t *testing.T) {
	phc, err := HashAdminPassword("long enough")
	require.NoError(t, err)
	h, err := password.Parse(phc)
	require.NoError(t, err)
	require.Equal(t, password.Default, h.Params)
	_, err = HashAdminPassword("short")
	require.Error(t, err)

	c := Config{Issuer: "tau:x"}
	require.NoError(t, c.defaults())
	require.Equal(t, SignUpInvite, c.SignUp)
	require.Equal(t, DefaultAccessTTL, c.AccessTTL)
	require.Equal(t, DefaultRefreshTTL, c.RefreshTTL)
	require.Equal(t, DefaultInviteTTL, c.InviteTTL)
	require.Equal(t, password.Default, c.Params)

	ok := Config{Issuer: "tau:x", SignUp: SignUpOpen, Admin: Admin{Name: "root"},
		Redirects: []string{"https://console.example/cb", "http://127.0.0.1:8080/cb"},
		Forges:    []ForgeApp{{Host: "github.com", ClientID: "a"}},
		OIDC:      []OIDCProvider{{Name: "corp", Issuer: "https://idp", ClientID: "b"}}}
	require.NoError(t, ok.defaults())

	for name, bad := range map[string]Config{
		"no issuer":       {},
		"sign-up":         {Issuer: "x", SignUp: "sometimes"},
		"redirect scheme": {Issuer: "x", Redirects: []string{"http://console.example/cb"}},
		"redirect frag":   {Issuer: "x", Redirects: []string{"https://console.example/cb#x"}},
		"redirect parse":  {Issuer: "x", Redirects: []string{"::"}},
		"forge":           {Issuer: "x", Forges: []ForgeApp{{Host: "github.com"}}},
		"oidc":            {Issuer: "x", OIDC: []OIDCProvider{{Name: "corp"}}},
		"duplicate": {Issuer: "x", Forges: []ForgeApp{{Host: "corp", ClientID: "a"}},
			OIDC: []OIDCProvider{{Name: "corp", Issuer: "https://idp", ClientID: "b"}}},
	} {
		require.Error(t, bad.defaults(), name)
	}
}

func TestOptionsRefuseNil(t *testing.T) {
	h := newHost(t, newMocknet(t))
	db, err := kvdbmock.New().New(nil, "a", 0)
	require.NoError(t, err)
	for _, o := range []Option{
		withUsers(nil, newVault(), Config{Issuer: "x"}),
		withUsers(db, newVault(), Config{}),
		withForges(nil),
		withHTTPClient(nil),
		withClock(nil),
		withIdentity(nil),
	} {
		_, err := build(h, o)
		require.Error(t, err)
	}
	// A forge app on a host no driver serves, or with no registry at all.
	_, err = build(h, withUsers(db, newVault(), Config{Issuer: "x", Forges: []ForgeApp{{Host: "h", ClientID: "c"}}}))
	require.Error(t, err)
	f := newFx(t)
	_, err = build(h, withUsers(db, newVault(), Config{Issuer: "x", Forges: []ForgeApp{{Host: "nobody.example", ClientID: "c"}}}), withForges(f.reg))
	require.Error(t, err)
}

func TestUsersIdentityAdmits(t *testing.T) {
	f := newFx(t)
	ctx := t.Context()
	admit := func(c *authv2.Caller) *authv2.Admission {
		t.Helper()
		resp, err := f.identity().Admit(ctx, req(c, ""))
		require.NoError(t, err)
		return resp.Msg
	}

	// A stranger on an invite-only cloud.
	a := admit(&authv2.Caller{Forge: testHost, Subject: "42", Login: "alice"})
	require.False(t, a.GetAdmitted())
	require.Contains(t, a.GetReason(), "sign in")

	// A caller carrying a valid tau token is a user, whatever forge account.
	a = admit(&authv2.Caller{Forge: testHost, Subject: "42", Token: f.adminToken()})
	require.True(t, a.GetAdmitted())

	// A bad token is a refusal with a reason, not an error.
	a = admit(&authv2.Caller{Forge: testHost, Subject: "42", Token: "garbage"})
	require.False(t, a.GetAdmitted())
	require.Contains(t, a.GetReason(), "sign in again")

	// A forge identity linked to a user is admitted without a tau token.
	u, err := f.s.create(ctx, newUser{Name: "alice"})
	require.NoError(t, err)
	require.NoError(t, f.s.link(ctx, u.ID, testHost, "42", "alice"))
	require.True(t, admit(&authv2.Caller{Forge: testHost, Subject: "42"}).GetAdmitted())

	// Open sign-up admits anyone the forge vouched for.
	f.s.cfg.SignUp = SignUpOpen
	require.True(t, admit(&authv2.Caller{Forge: testHost, Subject: "99"}).GetAdmitted())

	// A store that cannot answer is an error, never a refusal.
	f.s.cfg.SignUp = SignUpInvite
	f.s.store.db = &failing{Instance: f.db, fail: true}
	_, err = f.identity().Admit(ctx, req(&authv2.Caller{Forge: testHost, Subject: "77"}, ""))
	require.Error(t, err)
	_, err = f.identity().Admit(ctx, req(&authv2.Caller{Forge: testHost, Subject: "77", Token: "tau_000000000000000000000000_x"}, ""))
	require.Error(t, err)

	got, err := AdmitAll{}.Admit(ctx, Caller{})
	require.NoError(t, err)
	require.True(t, got.Admitted)
}

// failing is a kvdb that stops answering when told to.
type failing struct {
	kvdb.Instance
	fail     bool
	failPuts bool
}

var errStore = errors.New("store down")

func (f *failing) Get(ctx context.Context, k string) ([]byte, error) {
	if f.fail {
		return nil, errStore
	}
	return f.Instance.Get(ctx, k)
}

func (f *failing) Put(ctx context.Context, k string, v []byte) error {
	if f.fail || f.failPuts {
		return errStore
	}
	return f.Instance.Put(ctx, k, v)
}

func (f *failing) Delete(ctx context.Context, k string) error {
	if f.fail || f.failPuts {
		return errStore
	}
	return f.Instance.Delete(ctx, k)
}

func (f *failing) List(ctx context.Context, p string) ([]string, error) {
	if f.fail {
		return nil, errStore
	}
	return f.Instance.List(ctx, p)
}

func TestStoreOutagesAreErrors(t *testing.T) {
	f := newFx(t, func(c *Config) { c.SignUp = SignUpOpen })
	ctx := t.Context()
	admin := f.adminToken()
	u, tok := f.local("dev", authv2.Role_ROLE_MEMBER, authv2.Capability_CAPABILITY_LOGIN, authv2.Capability_CAPABILITY_APIKEY)
	api, err := f.users().CreateToken(ctx, req(&authv2.CreateTokenRequest{Name: "x"}, tok))
	require.NoError(t, err)
	in, err := f.sessions().Login(ctx, req(&authv2.LoginRequest{Name: "dev", Password: "dev password"}, ""))
	require.NoError(t, err)
	refresh := in.Msg.GetSession().GetRefreshToken()
	inv, err := f.users().CreateInvite(ctx, req(&authv2.CreateInviteRequest{}, admin))
	require.NoError(t, err)
	f.forge.give("tok", identity("5", "eve"))
	v, c := binding()
	az, err := f.sessions().Authorize(ctx, req(&authv2.AuthorizeRequest{Provider: testHost, RedirectUri: loopback, BindingChallenge: c}, ""))
	require.NoError(t, err)
	cd, state := f.oauth.approve(az.Msg.GetUrl(), "tok")
	dev, err := f.sessions().DeviceStart(ctx, req(&authv2.DeviceStartRequest{Provider: testHost}, ""))
	require.NoError(t, err)

	db := &failing{Instance: f.db}
	f.s.store.db = db
	other := newTestService(t, withUsers(db, f.vault, f.s.cfg), withForges(f.reg), withClock(func() time.Time { return f.now }))

	calls := map[string]func() error{
		"login": func() error {
			_, err := f.sessions().Login(ctx, req(&authv2.LoginRequest{Name: "dev", Password: "dev password"}, ""))
			return err
		},
		"whoami": func() error { _, err := f.sessions().Whoami(ctx, req(&authv2.WhoamiRequest{}, tok)); return err },
		"api key": func() error {
			_, err := f.identity().Verify(ctx, req(&authv2.VerifyRequest{Token: api.Msg.GetSecret()}, ""))
			return err
		},
		"refresh": func() error {
			_, err := f.sessions().Refresh(ctx, req(&authv2.RefreshRequest{RefreshToken: refresh}, ""))
			return err
		},
		"logout": func() error {
			_, err := f.sessions().Logout(ctx, req(&authv2.LogoutRequest{RefreshToken: refresh}, ""))
			return err
		},
		"exchange": func() error {
			_, err := f.sessions().Exchange(ctx, req(&authv2.ExchangeRequest{State: state, Code: cd, BindingVerifier: v}, ""))
			return err
		},
		"authorize": func() error {
			_, err := f.sessions().Authorize(ctx, req(&authv2.AuthorizeRequest{Provider: testHost, RedirectUri: loopback, BindingChallenge: c}, ""))
			return err
		},
		"device start": func() error {
			_, err := f.sessions().DeviceStart(ctx, req(&authv2.DeviceStartRequest{Provider: testHost}, ""))
			return err
		},
		"device poll": func() error {
			_, err := f.sessions().DevicePoll(ctx, req(&authv2.DevicePollRequest{Provider: testHost, DeviceCode: dev.Msg.GetDeviceCode()}, ""))
			return err
		},
		"pasted": func() error {
			_, err := f.sessions().Pasted(ctx, req(&authv2.PastedRequest{Provider: testHost, Token: "tok", Invite: inv.Msg.GetCode()}, ""))
			return err
		},
		"list users": func() error { _, err := f.users().ListUsers(ctx, req(&authv2.ListUsersRequest{}, admin)); return err },
		"sign a fresh key": func() error {
			_, err := (&sessionHandler{other}).Login(ctx, req(&authv2.LoginRequest{Name: "root", Password: f.adminP}, ""))
			return err
		},
		"pepper by name": func() error {
			_, err := f.s.keys.PepperNamed(ctx, "not-a-pepper")
			return err
		},
		"list invites": func() error { _, err := f.s.listInvites(ctx); return err },
		"list keys":    func() error { _, err := f.s.listAPIKeys(ctx, u.GetId()); return err },
		"revoke key":   func() error { return f.s.revokeAPIKey(ctx, user{ID: u.GetId()}, api.Msg.GetId()) },
		"remove":       func() error { return f.s.remove(ctx, user{ID: u.GetId(), Name: "dev", Local: true}) },
		"list":         func() error { _, err := f.s.list(ctx); return err },
		"links":        func() error { _, err := f.s.links(ctx, u.GetId()); return err },
		"create": func() error {
			_, err := f.s.create(ctx, newUser{Name: "new", Claim: true})
			return err
		},
		"by name": func() error { _, err := f.s.byName(ctx, "dev"); return err },
		"user":    func() error { _, err := f.s.user(ctx, u.GetId()); return err },
	}
	db.fail = true
	for name, call := range calls {
		require.Error(t, call(), name)
	}

	// Reads answer, writes do not.
	db.fail, db.failPuts = false, true
	for name, call := range map[string]func() error{
		"login": calls["login"],
		"create": func() error {
			_, err := f.s.create(ctx, newUser{Name: "new", Email: "n@x", Role: roleAdmin, Claim: true})
			return err
		},
		"set role": func() error {
			_, err := f.users().SetRole(ctx, req(&authv2.SetRoleRequest{Id: u.GetId()}, admin))
			return err
		},
		"set disabled": func() error {
			_, err := f.users().SetDisabled(ctx, req(&authv2.SetDisabledRequest{Id: u.GetId()}, admin))
			return err
		},
		"delete user": func() error {
			_, err := f.users().DeleteUser(ctx, req(&authv2.UserRequest{Id: u.GetId()}, admin))
			return err
		},
		"set password": func() error {
			_, err := f.users().SetPassword(ctx, req(&authv2.SetPasswordRequest{Password: "another password"}, tok))
			return err
		},
		"create invite": func() error {
			_, err := f.users().CreateInvite(ctx, req(&authv2.CreateInviteRequest{}, admin))
			return err
		},
		"delete invite": func() error {
			_, err := f.users().DeleteInvite(ctx, req(&authv2.UserRequest{Id: inv.Msg.GetId()}, admin))
			return err
		},
		"create token": func() error {
			_, err := f.users().CreateToken(ctx, req(&authv2.CreateTokenRequest{}, tok))
			return err
		},
		"revoke token": func() error {
			_, err := f.users().RevokeToken(ctx, req(&authv2.RevokeTokenRequest{Id: api.Msg.GetId()}, tok))
			return err
		},
		"logout":    calls["logout"],
		"authorize": calls["authorize"],
		"pasted":    calls["pasted"],
		"device":    calls["device start"],
		"link":      func() error { return f.s.link(ctx, u.GetId(), "i", "s", "l") },
		"sign":      calls["sign a fresh key"],
	} {
		require.Error(t, call(), name)
	}
	var ce *connect.Error
	require.False(t, errors.As(errStore, &ce))
}
