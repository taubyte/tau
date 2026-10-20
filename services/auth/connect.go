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

// connect.go is a user's forge connections: adding one, dropping one, and
// opening one for the build frontend.
//
// A CONNECTION LIVES ONLY IN THE CALLER'S TOKENS (seal.go), one per forge
// host. Changing the set is minting a new session holding the new set; the
// session the caller came with is revoked in its favour, so the old tokens —
// which still hold the old set — stop working once the revocation replicates.
//
// A FORGE CREDENTIAL THAT EXPIRES is renewed where it is used: Open refreshes
// it at the forge with this cloud's OAuth app and returns a renewed access
// token beside it, which the frontend hands back to the caller in the
// Tau-Renewed-Token response header. The client keeps the newest access token
// it was given and sends it with its next Refresh, so the renewal survives
// the access token's expiry.

// renewBefore is how close to its expiry a forge credential is renewed.
const renewBefore = time.Minute

// connectForge adds ft to as's session. The forge identity is linked to the
// caller when no user holds it yet, so a later sign-in with that forge finds
// them; a link another user holds is left alone — repository access is the
// forge's to grant, whoever the identity is linked to.
func (s *Service) connectForge(ctx context.Context, as principal, who signedInAs, ft *authv2.ForgeToken, refresh string) (*authv2.SignedIn, error) {
	conns, err := s.connections(ctx, as)
	if err != nil {
		return nil, err
	}
	switch _, err := s.linked(ctx, who.issuer, who.subject); {
	case errors.Is(err, ErrNotFound):
		if err := s.link(ctx, as.user.ID, who.issuer, who.subject, who.login); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return s.reissue(ctx, as, withConnection(conns, ft), refresh)
}

// disconnect drops host from as's session.
func (s *Service) disconnect(ctx context.Context, as principal, host, refresh string) (*authv2.SignedIn, error) {
	conns, err := s.connections(ctx, as)
	if err != nil {
		return nil, err
	}
	return s.reissue(ctx, as, withoutConnection(conns, host), refresh)
}

// reissue starts a session for as's user holding conns and retires the one
// they came with.
func (s *Service) reissue(ctx context.Context, as principal, conns []*authv2.ForgeToken, refresh string) (*authv2.SignedIn, error) {
	sess, err := s.issue(ctx, as.user, conns)
	if err != nil {
		return nil, err
	}
	if !as.apiKey {
		if err := s.store.revoke(ctx, revokedAccess, as.tokenID, s.now(), as.expires); err != nil {
			return nil, err
		}
	}
	if random, _, ok := splitRefresh(refresh); ok {
		var row sessionRow
		// Only the caller's own session is retired: a refresh token is not a
		// way to sign someone else out.
		if s.store.get(ctx, prefixSessions+digest(random), &row) == nil && row.User == as.user.ID {
			if err := s.logout(ctx, refresh); err != nil {
				return nil, err
			}
		}
	}
	return &authv2.SignedIn{Session: sess}, nil
}

// open is the credential raw carries for host, renewed at the forge when it
// is about to expire and the forge can renew it.
func (s *Service) open(ctx context.Context, raw, host string) (*authv2.Opened, error) {
	p, err := s.verify(ctx, raw)
	if err != nil {
		return nil, err
	}
	conns, err := s.connections(ctx, p)
	if err != nil {
		return nil, err
	}
	u, err := s.toProto(ctx, p.user)
	if err != nil {
		return nil, err
	}
	out := &authv2.Opened{Principal: &authv2.Principal{User: u, TokenId: p.tokenID}}
	for _, c := range conns {
		if c.GetHost() != host {
			continue
		}
		exp := c.GetExpiresUnix()
		if exp == 0 || s.now().Add(renewBefore).Unix() < exp {
			out.Forge = c
			return out, nil
		}
		if c.GetRefreshToken() == "" {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("auth: the %s connection has expired: connect it again", host))
		}
		renewed, err := s.renew(ctx, c)
		if err != nil {
			return nil, err
		}
		row, err := s.session(ctx, p.session)
		if err != nil {
			return nil, err
		}
		next := withConnection(conns, renewed)
		sealed, err := s.seal(ctx, &authv2.Sealed{User: p.user.ID, Session: p.session, SealedUnixNano: s.now().UnixNano(), Connections: next})
		if err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, err)
		}
		sess, err := s.access(ctx, p.user, p.session, sealed, next, time.Unix(0, row.Expires))
		if err != nil {
			return nil, err
		}
		out.Forge, out.RenewedToken, out.RenewedExpiresUnix = renewed, sess.GetAccessToken(), sess.GetAccessExpiresUnix()
		return out, nil
	}
	return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("auth: not connected to %s: connect it first", host))
}

// renew refreshes c at its forge with this cloud's OAuth app there.
func (s *Service) renew(ctx context.Context, c *authv2.ForgeToken) (*authv2.ForgeToken, error) {
	p, err := s.resolve(ctx, c.GetHost(), "")
	if err != nil || p.forge == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("auth: the %s connection has expired and this cloud cannot renew it: connect it again", c.GetHost()))
	}
	if err := s.withSecret(ctx, p); err != nil {
		return nil, err
	}
	tok, err := p.oauth.TokenSource(s.oauthContext(ctx), &oauth2.Token{RefreshToken: c.GetRefreshToken(), Expiry: time.Unix(1, 0)}).Token()
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("auth: %s would not renew the connection: connect it again: %w", c.GetHost(), err))
	}
	n := &authv2.ForgeToken{
		Host: c.GetHost(), AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, TokenType: tok.Type(),
		Scopes: c.GetScopes(), Subject: c.GetSubject(), Login: c.GetLogin(),
	}
	// A forge that does not rotate its refresh token keeps the old one.
	if n.RefreshToken == "" {
		n.RefreshToken = c.GetRefreshToken()
	}
	n.ExpiresUnix = s.expiry(tok)
	return n, nil
}
