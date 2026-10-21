package cli

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"connectrpc.com/connect"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// fakeAuth is auth's sign-in and user calls as the frontend carries them,
// answering from memory. A session is an access token naming a user. It
// holds no forge credential: a forge sign-in hands it back.
type fakeAuth struct {
	authv2connect.UnimplementedSessionServiceHandler
	authv2connect.UnimplementedUserServiceHandler

	mu        sync.Mutex
	n         int
	sessions  map[string]*fakeSession // access token -> session
	refresh   map[string]string       // refresh token -> access token
	pending   map[string]string       // state -> challenge
	polls     int
	loggedOut []string
	users     []*authv2.User
	// expiresIn is the lifetime of the access tokens it issues, in seconds
	// from now; zero is fifteen minutes.
	expiresIn int64
	// forgeExpires is when the forge credentials it hands back expire; zero
	// is never.
	forgeExpires int64
	renewed      int
	now          func() int64
}

type fakeSession struct {
	user *authv2.User
	// sid is what the real token carries in the clear.
	sid string
}

func newFakeAuth(now func() int64) *fakeAuth {
	a := &fakeAuth{sessions: map[string]*fakeSession{}, refresh: map[string]string{}, pending: map[string]string{}, now: now}
	a.users = []*authv2.User{
		{Id: "u-admin", Name: "root", Role: authv2.Role_ROLE_ADMIN, Builtin: true},
		{Id: "u-dev", Name: "dev", Role: authv2.Role_ROLE_MEMBER},
	}
	// goodToken is an API token: a session nothing renews.
	a.sessions[goodToken] = &fakeSession{user: a.users[1]}
	return a
}

func (a *fakeAuth) valid(token string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.sessions[token]
	return ok
}

func (a *fakeAuth) caller(h interface{ Get(string) string }) (*fakeSession, string, error) {
	raw, ok := strings.CutPrefix(h.Get("Authorization"), "Bearer ")
	if !ok {
		return nil, "", connect.NewError(connect.CodeUnauthenticated, errors.New("no tau token"))
	}
	s, ok := a.sessions[raw]
	if !ok {
		return nil, "", connect.NewError(connect.CodeUnauthenticated, errors.New("sign in again"))
	}
	return s, raw, nil
}

// issue is a new session for u; locked.
func (a *fakeAuth) issue(u *authv2.User) *authv2.Session {
	return a.issueIn(u, fmt.Sprintf("sid-%d", a.n+1))
}

// fakeAccess is a token shaped like the real one: a JWT whose claims a
// client reads to order tokens, signed by nothing.
func fakeAccess(sid string, iat int64, n int) string {
	payload, _ := json.Marshal(map[string]any{"sid": sid, "iat": iat, "jti": n})
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".fake"
}

func (a *fakeAuth) issueIn(u *authv2.User, sid string) *authv2.Session {
	a.n++
	access, refresh := fakeAccess(sid, a.now(), a.n), fmt.Sprintf("taur_%d", a.n)
	a.sessions[access] = &fakeSession{user: u, sid: sid}
	a.refresh[refresh] = access
	ttl := a.expiresIn
	if ttl == 0 {
		ttl = 900
	}
	return &authv2.Session{AccessToken: access, AccessExpiresUnix: a.now() + ttl, RefreshToken: refresh, RefreshExpiresUnix: a.now() + 86400, User: u}
}

func signed(s *authv2.Session) *connect.Response[authv2.SignedIn] {
	return connect.NewResponse(&authv2.SignedIn{Session: s})
}

func (a *fakeAuth) Providers(context.Context, *connect.Request[authv2.ProvidersRequest]) (*connect.Response[authv2.ProvidersResponse], error) {
	return connect.NewResponse(&authv2.ProvidersResponse{Password: true, Providers: []*authv2.Provider{
		{Id: "github.com", Kind: authv2.ProviderKind_PROVIDER_KIND_FORGE, Browser: true, Device: true, Pasted: true},
		{Id: "plain.example", Kind: authv2.ProviderKind_PROVIDER_KIND_FORGE, Pasted: true},
		{Id: "devices.example", Kind: authv2.ProviderKind_PROVIDER_KIND_FORGE, Device: true},
		{Id: "corp", Kind: authv2.ProviderKind_PROVIDER_KIND_OIDC, Browser: true},
	}}), nil
}

func (a *fakeAuth) Login(_ context.Context, req *connect.Request[authv2.LoginRequest]) (*connect.Response[authv2.SignedIn], error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if req.Msg.GetName() != "root" || req.Msg.GetPassword() != "hunter2" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("the name or the password is wrong"))
	}
	return signed(a.issue(a.users[0])), nil
}

func (a *fakeAuth) Refresh(_ context.Context, req *connect.Request[authv2.RefreshRequest]) (*connect.Response[authv2.SignedIn], error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	access, ok := a.refresh[req.Msg.GetRefreshToken()]
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("sign in again"))
	}
	s := a.sessions[access]
	delete(a.refresh, req.Msg.GetRefreshToken())
	return signed(a.issueIn(s.user, s.sid)), nil
}

func (a *fakeAuth) Logout(_ context.Context, req *connect.Request[authv2.LogoutRequest]) (*connect.Response[authv2.LogoutResponse], error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.loggedOut = append(a.loggedOut, req.Msg.GetRefreshToken())
	if _, raw, err := a.caller(req.Header()); err == nil {
		delete(a.sessions, raw)
	}
	return connect.NewResponse(&authv2.LogoutResponse{}), nil
}

func (a *fakeAuth) Authorize(_ context.Context, req *connect.Request[authv2.AuthorizeRequest]) (*connect.Response[authv2.AuthorizeResponse], error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.n++
	state := fmt.Sprintf("state-%d", a.n)
	a.pending[state] = req.Msg.GetBindingChallenge()
	// The provider would send the browser back here once the user approved.
	u := req.Msg.GetRedirectUri() + "?" + url.Values{"code": {"code-" + req.Msg.GetProvider()}, "state": {state}}.Encode()
	return connect.NewResponse(&authv2.AuthorizeResponse{Url: u, State: state}), nil
}

// connectOrSignIn is a forge sign-in: the forge credential alone when the
// request carries a session, a new session beside it when it does not;
// locked.
func (a *fakeAuth) connectOrSignIn(h interface{ Get(string) string }, host, token string) (*authv2.SignedIn, error) {
	in := &authv2.SignedIn{
		Forge:      &authv2.ForgeToken{Host: host, AccessToken: token, RefreshToken: "forge-refresh", ExpiresUnix: a.forgeExpires, Login: "fake-user"},
		Connection: &authv2.Connection{Host: host, Login: "fake-user", SignIn: authv2.SignInLink_SIGN_IN_LINK_THIS_USER},
	}
	if h.Get("Authorization") != "" {
		if _, _, err := a.caller(h); err != nil {
			return nil, err
		}
		return in, nil
	}
	in.Session = a.issue(a.users[1])
	return in, nil
}

func (a *fakeAuth) Exchange(_ context.Context, req *connect.Request[authv2.ExchangeRequest]) (*connect.Response[authv2.SignedIn], error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	want, ok := a.pending[req.Msg.GetState()]
	sum := sha256.Sum256([]byte(req.Msg.GetBindingVerifier()))
	if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != want {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("the binding verifier does not match"))
	}
	delete(a.pending, req.Msg.GetState())
	host := strings.TrimPrefix(req.Msg.GetCode(), "code-")
	if host == "corp" {
		return signed(a.issue(a.users[1])), nil
	}
	in, err := a.connectOrSignIn(req.Header(), host, forgeToken)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(in), nil
}

func (a *fakeAuth) DeviceStart(_ context.Context, req *connect.Request[authv2.DeviceStartRequest]) (*connect.Response[authv2.DeviceStartResponse], error) {
	return connect.NewResponse(&authv2.DeviceStartResponse{DeviceCode: "dc", UserCode: "ABCD-EFGH", VerificationUri: "https://forge.example/device", IntervalSeconds: 0}), nil
}

func (a *fakeAuth) DevicePoll(_ context.Context, req *connect.Request[authv2.DevicePollRequest]) (*connect.Response[authv2.DevicePollResponse], error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.polls++; a.polls == 1 {
		return connect.NewResponse(&authv2.DevicePollResponse{Pending: true}), nil
	}
	in, err := a.connectOrSignIn(req.Header(), req.Msg.GetProvider(), forgeToken)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.DevicePollResponse{SignedIn: in}), nil
}

func (a *fakeAuth) Pasted(_ context.Context, req *connect.Request[authv2.PastedRequest]) (*connect.Response[authv2.SignedIn], error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if req.Msg.GetToken() != forgeToken {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("the forge does not accept that token"))
	}
	in, err := a.connectOrSignIn(req.Header(), req.Msg.GetProvider(), req.Msg.GetToken())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(in), nil
}

func (a *fakeAuth) Whoami(_ context.Context, req *connect.Request[authv2.WhoamiRequest]) (*connect.Response[authv2.User], error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, _, err := a.caller(req.Header())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(s.user), nil
}

// Renew is a forge refresh through the cloud's OAuth app, for a signed-in
// caller.
func (a *fakeAuth) Renew(_ context.Context, req *connect.Request[authv2.RenewRequest]) (*connect.Response[authv2.ForgeToken], error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.renewed++
	if _, _, err := a.caller(req.Header()); err != nil {
		return nil, err
	}
	if req.Msg.GetRefreshToken() != "forge-refresh" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("the forge refused the refresh"))
	}
	return connect.NewResponse(&authv2.ForgeToken{Host: req.Msg.GetHost(), AccessToken: forgeToken, ExpiresUnix: a.now() + 3600}), nil
}

// ── users ────────────────────────────────────────────────────────────────────

func (a *fakeAuth) admin(h interface{ Get(string) string }) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, _, err := a.caller(h)
	if err != nil {
		return err
	}
	if s.user.GetRole() != authv2.Role_ROLE_ADMIN {
		return connect.NewError(connect.CodePermissionDenied, errors.New("admins only"))
	}
	return nil
}

func (a *fakeAuth) find(id string) (*authv2.User, error) {
	for _, u := range a.users {
		if u.GetId() == id {
			return u, nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("no such user"))
}

func (a *fakeAuth) ListUsers(_ context.Context, req *connect.Request[authv2.ListUsersRequest]) (*connect.Response[authv2.ListUsersResponse], error) {
	if err := a.admin(req.Header()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.ListUsersResponse{Users: a.users}), nil
}

func (a *fakeAuth) CreateUser(_ context.Context, req *connect.Request[authv2.CreateUserRequest]) (*connect.Response[authv2.User], error) {
	if err := a.admin(req.Header()); err != nil {
		return nil, err
	}
	u := &authv2.User{Id: "u-" + req.Msg.GetName(), Name: req.Msg.GetName(), Email: req.Msg.GetEmail(), Role: req.Msg.GetRole(), Capabilities: req.Msg.GetCapabilities()}
	a.users = append(a.users, u)
	return connect.NewResponse(u), nil
}

func (a *fakeAuth) DeleteUser(_ context.Context, req *connect.Request[authv2.UserRequest]) (*connect.Response[authv2.Empty], error) {
	if err := a.admin(req.Header()); err != nil {
		return nil, err
	}
	if _, err := a.find(req.Msg.GetId()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.Empty{}), nil
}

func (a *fakeAuth) SetRole(_ context.Context, req *connect.Request[authv2.SetRoleRequest]) (*connect.Response[authv2.User], error) {
	if err := a.admin(req.Header()); err != nil {
		return nil, err
	}
	u, err := a.find(req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	u.Role = req.Msg.GetRole()
	return connect.NewResponse(u), nil
}

func (a *fakeAuth) SetDisabled(_ context.Context, req *connect.Request[authv2.SetDisabledRequest]) (*connect.Response[authv2.User], error) {
	if err := a.admin(req.Header()); err != nil {
		return nil, err
	}
	u, err := a.find(req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	u.Disabled = req.Msg.GetDisabled()
	return connect.NewResponse(u), nil
}

func (a *fakeAuth) SetPassword(_ context.Context, req *connect.Request[authv2.SetPasswordRequest]) (*connect.Response[authv2.Empty], error) {
	a.mu.Lock()
	_, _, err := a.caller(req.Header())
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if len(req.Msg.GetPassword()) < 4 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("too short"))
	}
	return connect.NewResponse(&authv2.Empty{}), nil
}

func (a *fakeAuth) CreateInvite(_ context.Context, req *connect.Request[authv2.CreateInviteRequest]) (*connect.Response[authv2.Invite], error) {
	if err := a.admin(req.Header()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.Invite{Id: "inv-1", Role: req.Msg.GetRole(), Note: req.Msg.GetNote(), Code: "taui_code", ExpiresUnix: 1700604800}), nil
}

func (a *fakeAuth) ListInvites(_ context.Context, req *connect.Request[authv2.Empty]) (*connect.Response[authv2.ListInvitesResponse], error) {
	if err := a.admin(req.Header()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.ListInvitesResponse{Invites: []*authv2.Invite{{Id: "inv-1", Role: authv2.Role_ROLE_MEMBER, Note: "for ada", ExpiresUnix: 1700604800}}}), nil
}

func (a *fakeAuth) DeleteInvite(_ context.Context, req *connect.Request[authv2.UserRequest]) (*connect.Response[authv2.Empty], error) {
	if err := a.admin(req.Header()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.Empty{}), nil
}

func (a *fakeAuth) CreateToken(_ context.Context, req *connect.Request[authv2.CreateTokenRequest]) (*connect.Response[authv2.Token], error) {
	a.mu.Lock()
	s, _, err := a.caller(req.Header())
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.Token{Id: "tok-1", Name: req.Msg.GetName(), User: s.user.GetId(), Secret: "tau_secret", CreatedUnix: 1700000000, ExpiresUnix: req.Msg.GetTtlSeconds()}), nil
}

func (a *fakeAuth) ListTokens(_ context.Context, req *connect.Request[authv2.ListTokensRequest]) (*connect.Response[authv2.ListTokensResponse], error) {
	a.mu.Lock()
	s, _, err := a.caller(req.Header())
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.ListTokensResponse{Tokens: []*authv2.Token{
		{Id: "tok-1", Name: "ci", User: s.user.GetId(), CreatedUnix: 1700000000},
		{Id: "tok-2", Name: "deploy", User: s.user.GetId(), CreatedUnix: 1700000000, ExpiresUnix: 1700086400},
	}}), nil
}

func (a *fakeAuth) RevokeToken(_ context.Context, req *connect.Request[authv2.RevokeTokenRequest]) (*connect.Response[authv2.Empty], error) {
	a.mu.Lock()
	_, _, err := a.caller(req.Header())
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if req.Msg.GetId() != "tok-1" {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such token"))
	}
	return connect.NewResponse(&authv2.Empty{}), nil
}
