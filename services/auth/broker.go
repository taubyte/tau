package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/oauth2"

	"github.com/taubyte/tau/pkg/forge"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/services/auth/oidc"
)

// broker.go signs people in through a provider: a forge, through this
// cloud's own OAuth app there, or a bring-your-own OIDC provider.
//
// THE FLOW, for a browser or the CLI's loopback listener:
//
//  1. Authorize: the client names a provider, where to come back to, and a
//     binding challenge — the SHA-256 of a secret only it holds. Auth checks
//     the redirect against the allow-list, makes the state, the PKCE verifier
//     it will send the provider and (OIDC) the nonce, keeps them in a pending
//     row keyed by the state's digest, and returns the provider's URL.
//  2. The provider sends the browser back with a code and the state.
//  3. Exchange: the client sends both and its binding secret. The pending row
//     says which provider, which redirect and which verifier; the binding
//     secret proves the redeemer is whoever started the sign-in, so a code
//     intercepted on the way back is useless to anyone else. Auth redeems the
//     code with its client secret, asks the forge who the token is (or
//     verifies the ID token), signs the user in, and hands back BOTH the tau
//     session and the forge's own token. It keeps neither forge token.
//
// The pending row is replicated, so Exchange may land on another node than
// Authorize did. It is not a one-time primitive — the store has none — and
// does not need to be: the provider redeems a code once, and the row only
// says how.
//
// THE DEVICE FLOW (RFC 8628) is the CLI's other way in on a forge that offers
// one, and a PASTED token is the way in on every forge: a forge's identity
// face is mandatory, so any token the forge accepts says who it is.
//
// A caller names a provider, never a URL: every URL fetched here is one an
// operator configured or a driver derived from its host.

const (
	pendingTTL = 10 * time.Minute
	// pollFor bounds one DevicePoll; the client calls again while pending.
	pollFor = 25 * time.Second
	// callbackPath is the one path a loopback redirect may name.
	callbackPath = "/callback"
)

// provider is one way to sign in, resolved.
type provider struct {
	id     string
	oauth  *oauth2.Config
	pkce   bool
	device bool
	// forge is the host for a forge provider; empty for OIDC.
	forge string
	oidc  *oidc.Provider
	// secret is the vault name of the client secret.
	secret string
}

// providers lists every way to sign in.
func (s *Service) providerList() []*authv2.Provider {
	var out []*authv2.Provider
	apps := map[string]bool{}
	for _, f := range s.cfg.Forges {
		apps[f.Host] = true
		p := &authv2.Provider{Id: f.Host, Name: f.Host, Kind: authv2.ProviderKind_PROVIDER_KIND_FORGE, Pasted: true}
		if o, err := s.forges.OAuth(f.Host); err == nil {
			p.Browser, p.Device = true, o.DeviceURL != ""
		}
		out = append(out, p)
	}
	if s.forges != nil {
		for _, h := range s.forges.Hosts() {
			if !apps[h] {
				out = append(out, &authv2.Provider{Id: h, Name: h, Kind: authv2.ProviderKind_PROVIDER_KIND_FORGE, Pasted: true})
			}
		}
	}
	for _, o := range s.cfg.OIDC {
		out = append(out, &authv2.Provider{Id: o.Name, Name: o.Name, Kind: authv2.ProviderKind_PROVIDER_KIND_OIDC, Browser: true})
	}
	return out
}

func invalid(format string, a ...any) error {
	return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("auth: "+format, a...))
}

// resolve finds a configured provider by id.
func (s *Service) resolve(ctx context.Context, id, redirect string) (*provider, error) {
	for _, f := range s.cfg.Forges {
		if f.Host != id {
			continue
		}
		o, err := s.forges.OAuth(f.Host)
		if err != nil {
			return nil, invalid("%s offers no browser sign-in: %v", id, err)
		}
		style := oauth2.AuthStyleInHeader
		if o.SecretInBody {
			style = oauth2.AuthStyleInParams
		}
		return &provider{
			id: id, forge: f.Host, pkce: o.PKCE, device: o.DeviceURL != "", secret: OAuthSecretName(f.Host),
			oauth: &oauth2.Config{
				ClientID:    f.ClientID,
				Endpoint:    oauth2.Endpoint{AuthURL: o.AuthorizeURL, TokenURL: o.TokenURL, DeviceAuthURL: o.DeviceURL, AuthStyle: style},
				RedirectURL: redirect,
				Scopes:      o.Scopes,
			},
		}, nil
	}
	for _, o := range s.cfg.OIDC {
		if o.Name != id {
			continue
		}
		disc, err := s.discover(ctx, o)
		if err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, err)
		}
		d := disc.Document()
		scopes := o.Scopes
		if len(scopes) == 0 {
			scopes = []string{"email", "profile"}
		}
		return &provider{
			id: id, oidc: disc, pkce: true, secret: OIDCSecretName(o.Name),
			oauth: &oauth2.Config{
				ClientID:    o.ClientID,
				Endpoint:    oauth2.Endpoint{AuthURL: d.AuthorizationEndpoint, TokenURL: d.TokenEndpoint, AuthStyle: oauth2.AuthStyleInParams},
				RedirectURL: redirect,
				Scopes:      append([]string{"openid"}, scopes...),
			},
		}, nil
	}
	return nil, invalid("no sign-in provider %q", id)
}

func (s *Service) discover(ctx context.Context, o OIDCProvider) (*oidc.Provider, error) {
	s.oidcMu.Lock()
	defer s.oidcMu.Unlock()
	if p, ok := s.providers[o.Name]; ok {
		return p, nil
	}
	p, err := oidc.Discover(ctx, s.http, o.Issuer, s.now)
	if err != nil {
		return nil, err
	}
	s.providers[o.Name] = p
	return p, nil
}

// allowedRedirect is a loopback address at /callback on any port, or one of
// the configured console callbacks exactly.
func (s *Service) allowedRedirect(raw string) bool {
	if slices.Contains(s.cfg.Redirects, raw) {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Path != callbackPath || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback() && u.Port() != ""
}

func isLoopback(u *url.URL) bool {
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && ip != nil && ip.IsLoopback()
}

func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (s *Service) authorize(ctx context.Context, req *authv2.AuthorizeRequest) (*authv2.AuthorizeResponse, error) {
	if !s.allowedRedirect(req.GetRedirectUri()) {
		return nil, invalid("redirect %q is not one this cloud returns to", req.GetRedirectUri())
	}
	if b, err := base64.RawURLEncoding.DecodeString(req.GetBindingChallenge()); err != nil || len(b) != sha256.Size {
		return nil, invalid("a binding challenge is base64url(sha256(verifier))")
	}
	p, err := s.resolve(ctx, req.GetProvider(), req.GetRedirectUri())
	if err != nil {
		return nil, err
	}
	now := s.now()
	state := randomToken()
	row := pendingRow{
		Provider: p.id, Redirect: req.GetRedirectUri(), Verifier: oauth2.GenerateVerifier(), Binding: req.GetBindingChallenge(),
		Invite: req.GetInvite(), ClientID: p.oauth.ClientID, Created: now.UnixNano(), Expires: now.Add(pendingTTL).UnixNano(),
	}
	opts := []oauth2.AuthCodeOption{}
	if p.pkce {
		opts = append(opts, oauth2.S256ChallengeOption(row.Verifier))
	}
	if p.oidc != nil {
		row.Nonce = randomToken()
		opts = append(opts, oauth2.SetAuthURLParam("nonce", row.Nonce))
	}
	if err := s.store.put(ctx, prefixPending+digest(state), row); err != nil {
		return nil, err
	}
	return &authv2.AuthorizeResponse{Url: p.oauth.AuthCodeURL(state, opts...), State: state, ExpiresUnix: now.Add(pendingTTL).Unix()}, nil
}

// signedInAs is an identity a provider vouched for.
type signedInAs struct {
	issuer, subject, login, email string
}

func (s *Service) exchange(ctx context.Context, req *authv2.ExchangeRequest) (*authv2.SignedIn, error) {
	key := prefixPending + digest(req.GetState())
	var row pendingRow
	switch err := s.store.get(ctx, key, &row); {
	case errors.Is(err, ErrNotFound):
		return nil, invalid("no sign-in is waiting on that state")
	case err != nil:
		return nil, err
	}
	if s.now().UnixNano() >= row.Expires || row.Exchanged {
		return nil, invalid("that sign-in has expired or was used")
	}
	if subtle.ConstantTimeCompare([]byte(challenge(req.GetBindingVerifier())), []byte(row.Binding)) != 1 {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("auth: the binding verifier does not match the sign-in"))
	}
	row.Exchanged = true
	if err := s.store.put(ctx, key, row); err != nil {
		return nil, err
	}
	p, err := s.resolve(ctx, row.Provider, row.Redirect)
	if err != nil {
		return nil, err
	}
	if p.oauth.ClientID != row.ClientID {
		return nil, invalid("the provider's configuration changed during the sign-in")
	}
	if err := s.withSecret(ctx, p); err != nil {
		return nil, err
	}
	opts := []oauth2.AuthCodeOption{}
	if p.pkce {
		opts = append(opts, oauth2.VerifierOption(row.Verifier))
	}
	tok, err := p.oauth.Exchange(s.oauthContext(ctx), req.GetCode(), opts...)
	if err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("auth: %s refused the code: %w", p.id, err))
	}
	s.store.del(ctx, key)

	var who signedInAs
	if p.oidc != nil {
		raw, _ := tok.Extra("id_token").(string)
		id, err := p.oidc.Verify(ctx, raw, p.oauth.ClientID, row.Nonce)
		if err != nil {
			return nil, connect.NewError(connect.CodePermissionDenied, err)
		}
		who = signedInAs{issuer: id.Issuer, subject: id.Subject, login: id.Name}
		if id.EmailVerified {
			who.email = id.Email
		}
		sess, err := s.signIn(ctx, who, row.Invite)
		if err != nil {
			return nil, err
		}
		return &authv2.SignedIn{Session: sess}, nil
	}
	return s.signInForge(ctx, p.forge, tok, row.Invite)
}

// withSecret reads the provider's client secret from the vault.
func (s *Service) withSecret(ctx context.Context, p *provider) error {
	secret, err := s.keys.Secret(ctx, p.secret)
	if err != nil {
		return connect.NewError(connect.CodeUnavailable, err)
	}
	p.oauth.ClientSecret = string(secret)
	return nil
}

func (s *Service) oauthContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, s.http)
}

// signInForge asks host who tok is and signs them in.
func (s *Service) signInForge(ctx context.Context, host string, tok *oauth2.Token, invite string) (*authv2.SignedIn, error) {
	sess, err := s.signInToken(ctx, host, tok.AccessToken, invite)
	if err != nil {
		return nil, err
	}
	ft := &authv2.ForgeToken{Host: host, AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, TokenType: tok.Type()}
	if !tok.Expiry.IsZero() {
		ft.ExpiresUnix = tok.Expiry.Unix()
	}
	if sc, ok := tok.Extra("scope").(string); ok && sc != "" {
		ft.Scopes = strings.FieldsFunc(sc, func(r rune) bool { return r == ' ' || r == ',' })
	}
	return &authv2.SignedIn{Session: sess, Forge: ft}, nil
}

// signInToken signs in whoever a forge says the token is.
func (s *Service) signInToken(ctx context.Context, host, token, invite string) (*authv2.Session, error) {
	if s.forges == nil {
		return nil, invalid("no forge %q", host)
	}
	sess, err := s.forges.Host(host, forge.Token(token))
	if err != nil {
		return nil, invalid("%v", err)
	}
	who, err := sess.Whoami(ctx)
	var refused *forge.CredentialError
	switch {
	case errors.As(err, &refused):
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("auth: %s does not accept that token", host))
	case err != nil:
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("auth: asking %s who the token is: %w", host, err))
	case who.Subject == "":
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("auth: %s named no account", host))
	}
	return s.signIn(ctx, signedInAs{issuer: host, subject: who.Subject, login: who.Login, email: who.Email}, invite)
}

// signIn maps an identity to its user, creating one when sign-up allows it.
// An identity is never linked to an existing user by email: an email says
// where to write, not who someone is.
func (s *Service) signIn(ctx context.Context, who signedInAs, invite string) (*authv2.Session, error) {
	u, err := s.linked(ctx, who.issuer, who.subject)
	switch {
	case err == nil:
		return s.issue(ctx, u)
	case !errors.Is(err, ErrNotFound):
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	role := roleMember
	inv, err := s.invite(ctx, invite)
	switch {
	case err == nil:
		role = inv.Role
	case !errors.Is(err, ErrNotFound):
		return nil, connect.NewError(connect.CodeUnavailable, err)
	case invite != "":
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("auth: that invite is not valid"))
	case s.cfg.SignUp != SignUpOpen:
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("auth: this cloud is invite-only; ask an admin for an invite"))
	}
	u, err = s.create(ctx, newUser{Name: who.login, Email: who.email, Role: role})
	if err != nil {
		return nil, err
	}
	if err := s.link(ctx, u.ID, who.issuer, who.subject, who.login); err != nil {
		return nil, err
	}
	if inv.ID != "" {
		if err := s.redeem(ctx, inv, u.ID); err != nil {
			return nil, err
		}
	}
	return s.issue(ctx, u)
}

func (s *Service) deviceStart(ctx context.Context, req *authv2.DeviceStartRequest) (*authv2.DeviceStartResponse, error) {
	p, err := s.resolve(ctx, req.GetProvider(), "")
	if err != nil {
		return nil, err
	}
	if !p.device {
		return nil, invalid("%s offers no device sign-in", p.id)
	}
	if err := s.withSecret(ctx, p); err != nil {
		return nil, err
	}
	da, err := p.oauth.DeviceAuth(s.oauthContext(ctx))
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("auth: starting a device sign-in on %s: %w", p.id, err))
	}
	expires := da.Expiry
	if expires.IsZero() {
		expires = s.now().Add(pendingTTL)
	}
	if err := s.store.put(ctx, prefixDevice+digest(da.DeviceCode), deviceRow{Provider: p.id, Interval: da.Interval, Expires: expires.UnixNano()}); err != nil {
		return nil, err
	}
	return &authv2.DeviceStartResponse{
		DeviceCode: da.DeviceCode, UserCode: da.UserCode, VerificationUri: da.VerificationURI,
		VerificationUriComplete: da.VerificationURIComplete, IntervalSeconds: da.Interval, ExpiresUnix: expires.Unix(),
	}, nil
}

func (s *Service) devicePoll(ctx context.Context, req *authv2.DevicePollRequest) (*authv2.DevicePollResponse, error) {
	key := prefixDevice + digest(req.GetDeviceCode())
	var row deviceRow
	switch err := s.store.get(ctx, key, &row); {
	case errors.Is(err, ErrNotFound):
		return nil, invalid("no device sign-in is waiting on that code")
	case err != nil:
		return nil, err
	}
	if row.Provider != req.GetProvider() || s.now().UnixNano() >= row.Expires {
		return nil, invalid("that device sign-in has expired or is another provider's")
	}
	p, err := s.resolve(ctx, row.Provider, "")
	if err != nil {
		return nil, err
	}
	if err := s.withSecret(ctx, p); err != nil {
		return nil, err
	}
	wait, cancel := context.WithTimeout(ctx, pollFor)
	defer cancel()
	tok, err := p.oauth.DeviceAccessToken(s.oauthContext(wait), &oauth2.DeviceAuthResponse{
		DeviceCode: req.GetDeviceCode(), Interval: row.Interval, Expiry: time.Unix(0, row.Expires),
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return &authv2.DevicePollResponse{Pending: true, IntervalSeconds: row.Interval}, nil
		}
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("auth: %s refused the device sign-in: %w", p.id, err))
	}
	s.store.del(ctx, key)
	signed, err := s.signInForge(ctx, p.forge, tok, req.GetInvite())
	if err != nil {
		return nil, err
	}
	return &authv2.DevicePollResponse{SignedIn: signed}, nil
}
