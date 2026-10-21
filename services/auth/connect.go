package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/oauth2"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
)

// connect.go is a forge credential handed to the client: linking the forge
// account it was obtained with, describing it, and renewing it.
//
// THE CLOUD KEEPS NO FORGE CREDENTIAL. A forge sign-in returns the one it
// obtained, and the client holds it; Renew refreshes it with this cloud's
// OAuth app, whose client secret only the cloud holds.

// connectForge links the forge identity to the caller when no user holds it
// yet, so a later sign-in with that forge finds them; a link another user
// holds is left alone. The caller's session is untouched.
//
// WHO A FORGE ACCOUNT SIGNS IN AS is the deterministic winner of its link
// claims (store.go winner: earliest claim timestamp, then lowest user id),
// not whoever reached a node first; this check before linking is UX only.
func (s *Service) connectForge(ctx context.Context, as principal, who signedInAs, ft *authv2.ForgeToken) (*authv2.SignedIn, error) {
	switch _, err := s.linked(ctx, who.issuer, who.subject); {
	case errors.Is(err, ErrNotFound):
		if err := s.link(ctx, as.user.ID, who.issuer, who.subject, who.login); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return s.forgeSignedIn(ctx, as.user.ID, nil, ft)
}

// forgeSignedIn is a forge sign-in's answer for userID: sess, and ft with its
// description.
func (s *Service) forgeSignedIn(ctx context.Context, userID string, sess *authv2.Session, ft *authv2.ForgeToken) (*authv2.SignedIn, error) {
	c, err := s.describe(ctx, userID, ft)
	if err != nil {
		return nil, err
	}
	return &authv2.SignedIn{Session: sess, Forge: ft, Connection: c}, nil
}

// describe is ft as a client is told about it, and whether its forge account
// signs in as userID, read as of now from the replicated link claims.
func (s *Service) describe(ctx context.Context, userID string, ft *authv2.ForgeToken) (*authv2.Connection, error) {
	d := &authv2.Connection{
		Host: ft.GetHost(), Subject: ft.GetSubject(), Login: ft.GetLogin(), ExpiresUnix: ft.GetExpiresUnix(),
		Scopes: ft.GetScopes(), Refreshable: ft.GetRefreshToken() != "",
	}
	switch u, err := s.linked(ctx, ft.GetHost(), ft.GetSubject()); {
	case errors.Is(err, ErrNotFound):
	case err != nil:
		return nil, connect.NewError(connect.CodeUnavailable, err)
	case u.ID == userID:
		d.SignIn = authv2.SignInLink_SIGN_IN_LINK_THIS_USER
	default:
		d.SignIn = authv2.SignInLink_SIGN_IN_LINK_OTHER_USER
	}
	return d, nil
}

// renew refreshes a client's forge credential at host with this cloud's
// OAuth app there.
func (s *Service) renew(ctx context.Context, host, refreshToken string) (*authv2.ForgeToken, error) {
	if refreshToken == "" {
		return nil, invalid("no refresh token")
	}
	p, err := s.resolve(ctx, host, "")
	if err != nil || p.forge == "" {
		return nil, errReconnect(host, "cannot be renewed by this cloud", nil)
	}
	if err := s.withSecret(ctx, p); err != nil {
		return nil, err
	}
	tok, err := p.oauth.TokenSource(s.oauthContext(ctx), &oauth2.Token{RefreshToken: refreshToken, Expiry: time.Unix(1, 0)}).Token()
	if err != nil {
		return nil, errReconnect(host, "was refused renewal by the forge (its refresh token was revoked, or rotated by another renewal)", err)
	}
	n := &authv2.ForgeToken{Host: host, AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, TokenType: tok.Type(), ExpiresUnix: s.expiry(tok), Scopes: scopes(tok)}
	// A forge that does not rotate its refresh token keeps the old one.
	if n.RefreshToken == "" {
		n.RefreshToken = refreshToken
	}
	return n, nil
}

// errReconnect is a credential that only signing in with the forge again
// fixes, said in the words the caller acts on.
func errReconnect(host, why string, cause error) error {
	msg := fmt.Sprintf("auth: the %s credential %s: reconnect %s (tau forge connect %s)", host, why, host, host)
	if cause != nil {
		msg += ": " + cause.Error()
	}
	return connect.NewError(connect.CodeFailedPrecondition, errors.New(msg))
}
