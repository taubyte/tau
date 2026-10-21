package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/services/auth/token"
)

// sessions.go is the credentials auth issues: access tokens, refresh tokens
// and API tokens.
//
// AN ACCESS TOKEN is a short-lived JWT any node verifies (package token). It
// names its user and nothing else; the role is read at verification, so a
// demotion or a disable takes effect at once on every node that has seen it.
//
// A REFRESH TOKEN is an opaque random value stored by its digest. It is not
// rotated on use: rotation needs a compare-and-swap the store does not have,
// and two nodes rotating one token concurrently would each mint a successor.
// Its lifetime is the bound instead — DefaultRefreshTTL — and a sign-out or a
// revocation is a marker of its own that every node consults.
//
// AN API TOKEN is an opaque random value, shown once and stored as SHA-256.
// It carries its id in the clear, so the row is found without a scan and the
// digest compared in constant time.
//
// No credential is ever stored. A revocation is a write, and a node that has
// not yet received it still honours the credential until it does; an access
// token's lifetime bounds that for access, the replication delay for the rest.

const (
	refreshPrefix = "taur_"
	apiKeyPrefix  = "tau_"
	apiKeyIDLen   = 24
)

var errUnauthenticated = connect.NewError(connect.CodeUnauthenticated, errors.New("auth: sign in again"))

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// issue starts a session for u. The refresh token is taur_<random>, stored
// by its digest, which is the session's id.
func (s *Service) issue(ctx context.Context, u user) (*authv2.Session, error) {
	now := s.now()
	random := refreshPrefix + randomToken()
	sid := digest(random)
	expires := now.Add(s.cfg.RefreshTTL)
	if err := s.store.put(ctx, prefixSessions+sid, sessionRow{User: u.ID, Created: now.UnixNano(), Expires: expires.UnixNano()}); err != nil {
		return nil, err
	}
	sess, err := s.access(ctx, u, sid, expires)
	if err != nil {
		return nil, err
	}
	sess.RefreshToken, sess.RefreshExpiresUnix = random, expires.Unix()
	return sess, nil
}

// access mints an access token for u's session sid; it never outlives the
// session.
func (s *Service) access(ctx context.Context, u user, sid string, until time.Time) (*authv2.Session, error) {
	signer, err := s.keys.Signer(ctx, s.cfg.Issuer)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	now := s.now()
	exp := now.Add(s.cfg.AccessTTL)
	if exp.After(until) {
		exp = until
	}
	raw, err := signer.Sign(token.Claims{Subject: u.ID, ID: newID(), Issued: now, Expires: exp, Session: sid})
	if err != nil {
		return nil, err
	}
	p, err := s.toProto(ctx, u)
	if err != nil {
		return nil, err
	}
	return &authv2.Session{AccessToken: raw, AccessExpiresUnix: exp.Unix(), User: p}, nil
}

// principal is who a verified credential is.
type principal struct {
	user    user
	tokenID string
	apiKey  bool
	expires int64 // unix nanoseconds; zero never
	// session is the session an access token was minted from.
	session string
}

// verify says who raw belongs to, as of now.
func (s *Service) verify(ctx context.Context, raw string) (principal, error) {
	if s.store == nil {
		return principal{}, errNoUsers
	}
	var p principal
	var err error
	if strings.HasPrefix(raw, apiKeyPrefix) {
		p, err = s.verifyAPIKey(ctx, raw)
	} else {
		p, err = s.verifyAccess(ctx, raw)
	}
	if err != nil {
		return principal{}, err
	}
	u, err := s.active(ctx, p.user.ID)
	switch {
	case errors.Is(err, ErrNotFound):
		return principal{}, errUnauthenticated
	case err != nil:
		return principal{}, connect.NewError(connect.CodeUnavailable, err)
	}
	p.user = u
	return p, nil
}

func (s *Service) verifyAccess(ctx context.Context, raw string) (principal, error) {
	v, err := s.verifier(ctx)
	if err != nil {
		return principal{}, err
	}
	c, err := v.Verify(raw)
	if err != nil {
		return principal{}, errUnauthenticated
	}
	revoked, err := s.store.revoked(ctx, revokedAccess, c.ID)
	if err != nil {
		return principal{}, connect.NewError(connect.CodeUnavailable, err)
	}
	if revoked {
		return principal{}, errUnauthenticated
	}
	// THE SESSION IS CHECKED TOO, so retiring one (a sign-out)
	// ends every access token minted from it, not only the one presented.
	if c.Session != "" {
		if revoked, err = s.store.revoked(ctx, revokedSession, c.Session); err != nil {
			return principal{}, connect.NewError(connect.CodeUnavailable, err)
		}
		if revoked {
			return principal{}, errUnauthenticated
		}
	}
	return principal{user: user{ID: c.Subject}, tokenID: c.ID, expires: c.Expires.UnixNano(), session: c.Session}, nil
}

func (s *Service) verifier(ctx context.Context) (*token.Verifier, error) {
	return token.NewVerifier(s.cfg.Issuer, func(kid string) (ed25519.PublicKey, error) {
		return s.keys.PublicKey(ctx, kid)
	}, s.now)
}

// splitAPIKey reads tau_<id>_<secret>.
func splitAPIKey(raw string) (string, bool) {
	rest, ok := strings.CutPrefix(raw, apiKeyPrefix)
	if !ok || len(rest) < apiKeyIDLen+2 || rest[apiKeyIDLen] != '_' {
		return "", false
	}
	id := rest[:apiKeyIDLen]
	if _, err := hex.DecodeString(id); err != nil {
		return "", false
	}
	return id, true
}

func (s *Service) verifyAPIKey(ctx context.Context, raw string) (principal, error) {
	id, ok := splitAPIKey(raw)
	if !ok {
		return principal{}, errUnauthenticated
	}
	var row apiKeyRow
	switch err := s.store.get(ctx, prefixAPIKeys+id, &row); {
	case errors.Is(err, ErrNotFound):
		return principal{}, errUnauthenticated
	case err != nil:
		return principal{}, connect.NewError(connect.CodeUnavailable, err)
	}
	sum := sha256.Sum256([]byte(raw))
	if subtle.ConstantTimeCompare(sum[:], row.Sum) != 1 {
		return principal{}, errUnauthenticated
	}
	if row.Expires != 0 && s.now().UnixNano() >= row.Expires {
		return principal{}, errUnauthenticated
	}
	revoked, err := s.store.revoked(ctx, revokedAPIKey, id)
	if err != nil {
		return principal{}, connect.NewError(connect.CodeUnavailable, err)
	}
	if revoked {
		return principal{}, errUnauthenticated
	}
	u, err := s.user(ctx, row.User)
	if err == nil && !u.can(capAPIKey) {
		return principal{}, errUnauthenticated
	}
	return principal{user: user{ID: row.User}, tokenID: id, apiKey: true, expires: row.Expires}, nil
}

// session is the live session sid: not expired, not revoked.
func (s *Service) session(ctx context.Context, sid string) (sessionRow, error) {
	var row sessionRow
	switch err := s.store.get(ctx, prefixSessions+sid, &row); {
	case errors.Is(err, ErrNotFound):
		return row, errUnauthenticated
	case err != nil:
		return row, connect.NewError(connect.CodeUnavailable, err)
	}
	if s.now().UnixNano() >= row.Expires {
		return row, errUnauthenticated
	}
	revoked, err := s.store.revoked(ctx, revokedSession, sid)
	if err != nil {
		return row, connect.NewError(connect.CodeUnavailable, err)
	}
	if revoked {
		return row, errUnauthenticated
	}
	return row, nil
}

// refresh mints a new access token from a refresh token, for the same
// session.
func (s *Service) refresh(ctx context.Context, raw string) (*authv2.Session, error) {
	if !strings.HasPrefix(raw, refreshPrefix) {
		return nil, errUnauthenticated
	}
	row, err := s.session(ctx, digest(raw))
	if err != nil {
		return nil, err
	}
	u, err := s.active(ctx, row.User)
	if err != nil {
		return nil, errUnauthenticated
	}
	sess, err := s.access(ctx, u, digest(raw), time.Unix(0, row.Expires))
	if err != nil {
		return nil, err
	}
	sess.RefreshToken, sess.RefreshExpiresUnix = raw, row.Expires/1e9
	return sess, nil
}

// logout revokes a refresh token's session.
func (s *Service) logout(ctx context.Context, raw string) error {
	d := digest(raw)
	var row sessionRow
	if err := s.store.get(ctx, prefixSessions+d, &row); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	return s.store.revoke(ctx, revokedSession, d, s.now(), row.Expires)
}

// createAPIKey mints an API token for u.
func (s *Service) createAPIKey(ctx context.Context, u user, name string, ttl time.Duration) (*authv2.Token, error) {
	if !u.can(capAPIKey) {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("auth: %s may not hold API tokens", u.Name))
	}
	if len(name) > 128 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("auth: a token name is at most 128 bytes"))
	}
	id := newID()[:apiKeyIDLen]
	raw := apiKeyPrefix + id + "_" + randomToken()
	sum := sha256.Sum256([]byte(raw))
	now := s.now()
	row := apiKeyRow{User: u.ID, Name: name, Sum: sum[:], Created: now.UnixNano()}
	if ttl > 0 {
		row.Expires = now.Add(ttl).UnixNano()
	}
	if err := s.store.put(ctx, prefixAPIKeys+id, row); err != nil {
		return nil, err
	}
	if err := s.store.put(ctx, userKey(u.ID, fieldAPIKeys)+"/"+id, claim{Created: row.Created}); err != nil {
		return nil, err
	}
	t := apiKeyProto(id, row)
	t.Secret = raw
	return t, nil
}

func apiKeyProto(id string, row apiKeyRow) *authv2.Token {
	t := &authv2.Token{Id: id, Name: row.Name, User: row.User, CreatedUnix: row.Created / 1e9}
	if row.Expires != 0 {
		t.ExpiresUnix = row.Expires / 1e9
	}
	return t
}

func (s *Service) listAPIKeys(ctx context.Context, userID string) ([]*authv2.Token, error) {
	prefix := userKey(userID, fieldAPIKeys) + "/"
	ids, err := s.store.children(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var out []*authv2.Token
	for _, id := range ids {
		revoked, err := s.store.revoked(ctx, revokedAPIKey, id)
		if err != nil {
			return nil, err
		}
		var row apiKeyRow
		if revoked || s.store.get(ctx, prefixAPIKeys+id, &row) != nil {
			continue
		}
		out = append(out, apiKeyProto(id, row))
	}
	return out, nil
}

// revokeAPIKey revokes an API token; by is who asks, and must own it or be an
// admin.
func (s *Service) revokeAPIKey(ctx context.Context, by user, id string) error {
	var row apiKeyRow
	switch err := s.store.get(ctx, prefixAPIKeys+id, &row); {
	case errors.Is(err, ErrNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("auth: no such token"))
	case err != nil:
		return err
	}
	if row.User != by.ID && by.Role != roleAdmin {
		return connect.NewError(connect.CodeNotFound, errors.New("auth: no such token"))
	}
	return s.store.revoke(ctx, revokedAPIKey, id, s.now(), row.Expires)
}
