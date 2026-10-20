package auth

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/oauth2"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
	"github.com/taubyte/tau/services/auth/password"
)

// mount is the one handler rpc.Listen serves.
func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(authv2connect.NewIdentityServiceHandler(&identityHandler{s}))
	mux.Handle(authv2connect.NewSessionServiceHandler(&sessionHandler{s}))
	mux.Handle(authv2connect.NewUserServiceHandler(&userHandler{s}))
	return mux
}

// caller is who the request's bearer token says it is.
func (s *Service) caller(ctx context.Context, h http.Header) (principal, error) {
	raw, ok := strings.CutPrefix(h.Get("Authorization"), "Bearer ")
	if !ok || raw == "" {
		return principal{}, connect.NewError(connect.CodeUnauthenticated, errors.New("auth: no tau token"))
	}
	return s.verify(ctx, raw)
}

// signedIn is the caller when the request carries a tau token, and nil when
// it carries none. A token that does not verify is refused rather than
// ignored: a caller connecting a forge must not be signed in as whoever the
// forge says instead.
func (s *Service) signedIn(ctx context.Context, h http.Header) (*principal, error) {
	if h.Get("Authorization") == "" {
		return nil, nil
	}
	p, err := s.caller(ctx, h)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

type sessionHandler struct{ s *Service }

func (h *sessionHandler) ready() error {
	if h.s.store == nil {
		return errNoUsers
	}
	return nil
}

func (h *sessionHandler) Providers(ctx context.Context, _ *connect.Request[authv2.ProvidersRequest]) (*connect.Response[authv2.ProvidersResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.ProvidersResponse{Providers: h.s.providerList(), Password: true}), nil
}

// errLogin is every password refusal: which half was wrong is not said.
var errLogin = connect.NewError(connect.CodeUnauthenticated, errors.New("auth: the name or the password is wrong"))

func (h *sessionHandler) Login(ctx context.Context, req *connect.Request[authv2.LoginRequest]) (*connect.Response[authv2.SignedIn], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	u, err := h.s.byName(ctx, req.Msg.GetName())
	switch {
	case errors.Is(err, ErrNotFound):
		return nil, errLogin
	case err != nil:
		return nil, connect.NewError(connect.CodeUnavailable, err)
	case u.Disabled || !u.can(capLogin):
		return nil, errLogin
	}
	if err := h.s.checkPassword(ctx, u, req.Msg.GetPassword()); err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, err
		}
		return nil, errLogin
	}
	sess, err := h.s.issue(ctx, u, nil)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.SignedIn{Session: sess}), nil
}

func (h *sessionHandler) Refresh(ctx context.Context, req *connect.Request[authv2.RefreshRequest]) (*connect.Response[authv2.SignedIn], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	sess, err := h.s.refresh(ctx, req.Msg.GetRefreshToken(), req.Msg.GetAccessTokens())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.SignedIn{Session: sess}), nil
}

func (h *sessionHandler) Logout(ctx context.Context, req *connect.Request[authv2.LogoutRequest]) (*connect.Response[authv2.LogoutResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	// The access token the call carries is revoked too, when it has one.
	if p, err := h.s.caller(ctx, req.Header()); err == nil && !p.apiKey {
		if err := h.s.store.revoke(ctx, revokedAccess, p.tokenID, h.s.now(), p.expires); err != nil {
			return nil, err
		}
		if p.session != "" {
			if err := h.s.store.revoke(ctx, revokedSession, p.session, h.s.now(), h.s.now().Add(h.s.cfg.RefreshTTL).UnixNano()); err != nil {
				return nil, err
			}
		}
	}
	if err := h.s.logout(ctx, req.Msg.GetRefreshToken()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.LogoutResponse{}), nil
}

func (h *sessionHandler) Authorize(ctx context.Context, req *connect.Request[authv2.AuthorizeRequest]) (*connect.Response[authv2.AuthorizeResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	resp, err := h.s.authorize(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (h *sessionHandler) Exchange(ctx context.Context, req *connect.Request[authv2.ExchangeRequest]) (*connect.Response[authv2.SignedIn], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	as, err := h.s.signedIn(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	resp, err := h.s.exchange(ctx, as, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (h *sessionHandler) DeviceStart(ctx context.Context, req *connect.Request[authv2.DeviceStartRequest]) (*connect.Response[authv2.DeviceStartResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	resp, err := h.s.deviceStart(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (h *sessionHandler) DevicePoll(ctx context.Context, req *connect.Request[authv2.DevicePollRequest]) (*connect.Response[authv2.DevicePollResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	as, err := h.s.signedIn(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	resp, err := h.s.devicePoll(ctx, as, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (h *sessionHandler) Pasted(ctx context.Context, req *connect.Request[authv2.PastedRequest]) (*connect.Response[authv2.SignedIn], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	if req.Msg.GetToken() == "" {
		return nil, invalid("no token")
	}
	as, err := h.s.signedIn(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	signed, err := h.s.signInForge(ctx, as, req.Msg.GetProvider(), &oauth2.Token{AccessToken: req.Msg.GetToken()}, req.Msg.GetInvite())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(signed), nil
}

func (h *sessionHandler) Disconnect(ctx context.Context, req *connect.Request[authv2.DisconnectRequest]) (*connect.Response[authv2.SignedIn], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	p, err := h.s.caller(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	signed, err := h.s.disconnect(ctx, p, req.Msg.GetHost())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(signed), nil
}

func (h *sessionHandler) Connections(ctx context.Context, req *connect.Request[authv2.ConnectionsRequest]) (*connect.Response[authv2.ConnectionsResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	p, err := h.s.caller(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	conns, err := h.s.connections(ctx, p)
	if err != nil {
		return nil, err
	}
	described, err := h.s.describe(ctx, p.user.ID, conns)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.ConnectionsResponse{Connections: described}), nil
}

func (h *sessionHandler) Whoami(ctx context.Context, req *connect.Request[authv2.WhoamiRequest]) (*connect.Response[authv2.User], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	p, err := h.s.caller(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	u, err := h.s.toProto(ctx, p.user)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(u), nil
}

type userHandler struct{ s *Service }

// as is the caller; admin says they must be an admin.
func (h *userHandler) as(ctx context.Context, hd http.Header, admin bool) (user, error) {
	if h.s.store == nil {
		return user{}, errNoUsers
	}
	p, err := h.s.caller(ctx, hd)
	if err != nil {
		return user{}, err
	}
	if admin && p.user.Role != roleAdmin {
		return user{}, connect.NewError(connect.CodePermissionDenied, errors.New("auth: that is an admin's to do"))
	}
	return p.user, nil
}

func (h *userHandler) target(ctx context.Context, id string) (user, error) {
	u, err := h.s.user(ctx, id)
	switch {
	case errors.Is(err, ErrNotFound):
		return user{}, connect.NewError(connect.CodeNotFound, errors.New("auth: no such user"))
	case err != nil:
		return user{}, connect.NewError(connect.CodeUnavailable, err)
	}
	return u, nil
}

var errBuiltin = connect.NewError(connect.CodeFailedPrecondition, errors.New("auth: the built-in admin is configured, not managed"))

func (h *userHandler) reply(ctx context.Context, id string) (*connect.Response[authv2.User], error) {
	u, err := h.target(ctx, id)
	if err != nil {
		return nil, err
	}
	p, err := h.s.toProto(ctx, u)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(p), nil
}

func (h *userHandler) ListUsers(ctx context.Context, req *connect.Request[authv2.ListUsersRequest]) (*connect.Response[authv2.ListUsersResponse], error) {
	if _, err := h.as(ctx, req.Header(), true); err != nil {
		return nil, err
	}
	us, err := h.s.list(ctx)
	if err != nil {
		return nil, err
	}
	out := &authv2.ListUsersResponse{}
	for _, u := range us {
		p, err := h.s.toProto(ctx, u)
		if err != nil {
			return nil, err
		}
		out.Users = append(out.Users, p)
	}
	return connect.NewResponse(out), nil
}

func role(r authv2.Role) string {
	if r == authv2.Role_ROLE_ADMIN {
		return roleAdmin
	}
	return roleMember
}

func (h *userHandler) CreateUser(ctx context.Context, req *connect.Request[authv2.CreateUserRequest]) (*connect.Response[authv2.User], error) {
	if _, err := h.as(ctx, req.Header(), true); err != nil {
		return nil, err
	}
	m := req.Msg
	var caps []string
	for _, c := range m.GetCapabilities() {
		switch c {
		case authv2.Capability_CAPABILITY_LOGIN:
			caps = append(caps, capLogin)
		case authv2.Capability_CAPABILITY_APIKEY:
			caps = append(caps, capAPIKey)
		default:
			return nil, invalid("unknown capability %v", c)
		}
	}
	if len(caps) == 0 {
		return nil, invalid("a local user needs the login or the apikey capability")
	}
	login := slices.Contains(caps, capLogin)
	if login {
		if err := password.Check(m.GetPassword()); err != nil {
			return nil, invalid("%v", err)
		}
	} else if m.GetPassword() != "" {
		return nil, invalid("a user without the login capability has no password")
	}
	u, err := h.s.create(ctx, newUser{Name: m.GetName(), Email: m.GetEmail(), Role: role(m.GetRole()), Local: true, Caps: caps, Claim: true})
	if err != nil {
		return nil, err
	}
	if login {
		if err := h.s.setPassword(ctx, u.ID, m.GetPassword()); err != nil {
			return nil, err
		}
	}
	return h.reply(ctx, u.ID)
}

func (h *userHandler) DeleteUser(ctx context.Context, req *connect.Request[authv2.UserRequest]) (*connect.Response[authv2.Empty], error) {
	if _, err := h.as(ctx, req.Header(), true); err != nil {
		return nil, err
	}
	u, err := h.target(ctx, req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	if u.Builtin {
		return nil, errBuiltin
	}
	if err := h.s.remove(ctx, u); err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.Empty{}), nil
}

func (h *userHandler) SetRole(ctx context.Context, req *connect.Request[authv2.SetRoleRequest]) (*connect.Response[authv2.User], error) {
	if _, err := h.as(ctx, req.Header(), true); err != nil {
		return nil, err
	}
	u, err := h.target(ctx, req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	if u.Builtin {
		return nil, errBuiltin
	}
	if err := h.s.store.put(ctx, userKey(u.ID, fieldRole), roleRow{Role: role(req.Msg.GetRole())}); err != nil {
		return nil, err
	}
	return h.reply(ctx, u.ID)
}

func (h *userHandler) SetDisabled(ctx context.Context, req *connect.Request[authv2.SetDisabledRequest]) (*connect.Response[authv2.User], error) {
	if _, err := h.as(ctx, req.Header(), true); err != nil {
		return nil, err
	}
	u, err := h.target(ctx, req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	if u.Builtin {
		return nil, errBuiltin
	}
	if err := h.s.store.put(ctx, userKey(u.ID, fieldDisabled), flagRow{Set: req.Msg.GetDisabled()}); err != nil {
		return nil, err
	}
	return h.reply(ctx, u.ID)
}

func (h *userHandler) SetPassword(ctx context.Context, req *connect.Request[authv2.SetPasswordRequest]) (*connect.Response[authv2.Empty], error) {
	id := req.Msg.GetId()
	me, err := h.as(ctx, req.Header(), id != "")
	if err != nil {
		return nil, err
	}
	if id == "" {
		id = me.ID
	}
	u, err := h.target(ctx, id)
	if err != nil {
		return nil, err
	}
	if u.Builtin {
		return nil, errBuiltin
	}
	if !u.can(capLogin) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("auth: that user signs in through a provider and has no password"))
	}
	if err := h.s.setPassword(ctx, u.ID, req.Msg.GetPassword()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.Empty{}), nil
}

func (h *userHandler) CreateInvite(ctx context.Context, req *connect.Request[authv2.CreateInviteRequest]) (*connect.Response[authv2.Invite], error) {
	me, err := h.as(ctx, req.Header(), true)
	if err != nil {
		return nil, err
	}
	inv, err := h.s.createInvite(ctx, me, role(req.Msg.GetRole()), time.Duration(req.Msg.GetTtlSeconds())*time.Second, req.Msg.GetNote())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(inv), nil
}

func (h *userHandler) ListInvites(ctx context.Context, req *connect.Request[authv2.Empty]) (*connect.Response[authv2.ListInvitesResponse], error) {
	if _, err := h.as(ctx, req.Header(), true); err != nil {
		return nil, err
	}
	invs, err := h.s.listInvites(ctx)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.ListInvitesResponse{Invites: invs}), nil
}

func (h *userHandler) DeleteInvite(ctx context.Context, req *connect.Request[authv2.UserRequest]) (*connect.Response[authv2.Empty], error) {
	if _, err := h.as(ctx, req.Header(), true); err != nil {
		return nil, err
	}
	if err := h.s.deleteInvite(ctx, req.Msg.GetId()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.Empty{}), nil
}

func (h *userHandler) CreateToken(ctx context.Context, req *connect.Request[authv2.CreateTokenRequest]) (*connect.Response[authv2.Token], error) {
	forUser := req.Msg.GetUser()
	me, err := h.as(ctx, req.Header(), forUser != "")
	if err != nil {
		return nil, err
	}
	u := me
	if forUser != "" {
		if u, err = h.target(ctx, forUser); err != nil {
			return nil, err
		}
	}
	t, err := h.s.createAPIKey(ctx, u, req.Msg.GetName(), time.Duration(req.Msg.GetTtlSeconds())*time.Second)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(t), nil
}

func (h *userHandler) ListTokens(ctx context.Context, req *connect.Request[authv2.ListTokensRequest]) (*connect.Response[authv2.ListTokensResponse], error) {
	of := req.Msg.GetUser()
	me, err := h.as(ctx, req.Header(), false)
	if err != nil {
		return nil, err
	}
	if of == "" {
		of = me.ID
	} else if of != me.ID && me.Role != roleAdmin {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("auth: that is an admin's to do"))
	}
	ts, err := h.s.listAPIKeys(ctx, of)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.ListTokensResponse{Tokens: ts}), nil
}

func (h *userHandler) RevokeToken(ctx context.Context, req *connect.Request[authv2.RevokeTokenRequest]) (*connect.Response[authv2.Empty], error) {
	me, err := h.as(ctx, req.Header(), false)
	if err != nil {
		return nil, err
	}
	if err := h.s.revokeAPIKey(ctx, me, req.Msg.GetId()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.Empty{}), nil
}
