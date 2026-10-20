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
//
// WITHIN ONE REQUEST, a frontend that opens a second host opens it from the
// token the first renewal returned, not the one the caller sent, so the
// token it hands back holds both (frontend/authz.go). ACROSS REQUESTS, two
// renewals of two hosts from one token each hold one; the client keeps every
// token it is handed until it refreshes, and Refresh merges their seals per
// host (seal.go merge), so neither is lost.
//
// A FORGE THAT ROTATES ITS REFRESH TOKEN (Bitbucket Cloud) invalidates the
// old one on each renewal, and the cloud stores neither: the only copy of the
// new one is the token that renewal returned. A renewal whose response was
// lost, or two concurrent renewals of which the client kept the other, leave
// a seal holding a refresh token the forge no longer honours. That is not
// repaired, because repairing it needs the stored credential this cloud
// refuses to keep: the next renewal is refused by the forge, and the caller
// is told to reconnect that host (errReconnect). It costs one `tau forge
// connect`, and it is never a silent wrong answer.

// renewBefore is how close to its expiry a forge credential is renewed.
const renewBefore = time.Minute

// connectForge adds ft to as's session. The forge identity is linked to the
// caller when no user holds it yet, so a later sign-in with that forge finds
// them; a link another user holds is left alone — repository access is the
// forge's to grant, whoever the identity is linked to.
//
// WHO A FORGE ACCOUNT SIGNS IN AS is the deterministic winner of its link
// claims (store.go winner: earliest claim timestamp, then lowest user id),
// not whoever reached a node first; this check before linking is UX only.
// Two users linking one account concurrently both write a claim and every
// node resolves them alike; the loser keeps the connection, described as
// SIGN_IN_LINK_OTHER_USER.
func (s *Service) connectForge(ctx context.Context, as principal, who signedInAs, ft *authv2.ForgeToken) (*authv2.SignedIn, error) {
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
	return s.reissue(ctx, as, withConnection(conns, ft))
}

// disconnect drops host from as's session.
func (s *Service) disconnect(ctx context.Context, as principal, host string) (*authv2.SignedIn, error) {
	conns, err := s.connections(ctx, as)
	if err != nil {
		return nil, err
	}
	return s.reissue(ctx, as, withoutConnection(conns, host))
}

// reissue starts a session for as's user holding conns and retires the one
// they came with: THE AUTHENTICATED SESSION ITSELF, by its id, so its refresh
// token stops renewing and every access token minted from it stops verifying
// (verifyAccess checks the session) — whether or not the caller hands its
// refresh token back.
//
// THE RETIREMENT IS EVENTUAL. A node that has not yet received the marker
// keeps refreshing the old session until it does; the access TTL does not
// bound that, the session's own expiry does (RefreshTTL, 24h by default).
func (s *Service) reissue(ctx context.Context, as principal, conns []*authv2.ForgeToken) (*authv2.SignedIn, error) {
	sess, err := s.issue(ctx, as.user, conns)
	if err != nil {
		return nil, err
	}
	if as.apiKey {
		return &authv2.SignedIn{Session: sess}, nil
	}
	if err := s.store.revoke(ctx, revokedAccess, as.tokenID, s.now(), as.expires); err != nil {
		return nil, err
	}
	if as.session != "" {
		until := s.now().Add(s.cfg.RefreshTTL).UnixNano()
		var row sessionRow
		if s.store.get(ctx, prefixSessions+as.session, &row) == nil {
			until = row.Expires
		}
		if err := s.store.revoke(ctx, revokedSession, as.session, s.now(), until); err != nil {
			return nil, err
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
	current, err := s.sealOf(ctx, p)
	if err != nil {
		return nil, err
	}
	conns := current.GetConnections()
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
			return nil, errReconnect(host, "has expired and cannot be renewed", nil)
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
		gen := current.GetGeneration() + 1
		sealed, err := s.seal(ctx, &authv2.Sealed{User: p.user.ID, Session: p.session, SealedUnixNano: s.now().UnixNano(), Connections: next, Generation: gen})
		if err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, err)
		}
		sess, err := s.access(ctx, p.user, p.session, sealed, next, gen, time.Unix(0, row.Expires))
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
		return nil, errReconnect(c.GetHost(), "has expired and this cloud cannot renew it", nil)
	}
	if err := s.withSecret(ctx, p); err != nil {
		return nil, err
	}
	tok, err := p.oauth.TokenSource(s.oauthContext(ctx), &oauth2.Token{RefreshToken: c.GetRefreshToken(), Expiry: time.Unix(1, 0)}).Token()
	if err != nil {
		// A ROTATED REFRESH TOKEN LANDS HERE: one a concurrent or lost
		// renewal already spent, which the forge no longer honours.
		return nil, errReconnect(c.GetHost(), "was refused renewal by the forge (its refresh token was revoked, or rotated by another renewal)", err)
	}
	n := &authv2.ForgeToken{
		Host: c.GetHost(), AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, TokenType: tok.Type(),
		Scopes: c.GetScopes(), Subject: c.GetSubject(), Login: c.GetLogin(),
		Renewals: c.GetRenewals() + 1, ObtainedUnixNano: s.now().UnixNano(),
	}
	// A forge that does not rotate its refresh token keeps the old one.
	if n.RefreshToken == "" {
		n.RefreshToken = c.GetRefreshToken()
	}
	n.ExpiresUnix = s.expiry(tok)
	return n, nil
}

// errReconnect is a connection that only reconnecting fixes, said in the
// words the caller acts on: "reconnect <host>".
func errReconnect(host, why string, cause error) error {
	msg := fmt.Sprintf("auth: the %s connection %s: reconnect %s (tau forge connect %s)", host, why, host, host)
	if cause != nil {
		msg += ": " + cause.Error()
	}
	return connect.NewError(connect.CodeFailedPrecondition, errors.New(msg))
}
