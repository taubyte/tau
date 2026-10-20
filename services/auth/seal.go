package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"
	jose "github.com/go-jose/go-jose/v4"
	"google.golang.org/protobuf/proto"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
)

// seal.go encrypts a session's forge connections into its tau tokens.
//
// A SIGNED TOKEN IS READABLE by whoever holds it, and a forge credential must
// not be: so the connections travel as a JWE (RFC 7516) — direct encryption
// with A256GCM under this node's sealing key, the key named in the header's
// kid — and the result rides in the access token's tau_conn claim and in the
// refresh token. Any node opens any seal: it reads the named key from the
// secrets service once, as it reads a pepper.
//
// The plaintext (authv2.Sealed) names the user and the session it belongs to,
// so a seal lifted out of one token opens nowhere else. The cloud stores no
// forge credential: the seal is the only copy, and it is the client's.

// sealType is the JOSE typ every seal carries, so no other JWE auth might
// make is accepted as one.
const sealType = "tau-conn+jwe"

var errSeal = errors.New("auth: the token's connections do not open")

func (s *Service) seal(ctx context.Context, c *authv2.Sealed) (string, error) {
	name, key, err := s.keys.SealKey(ctx)
	if err != nil {
		return "", err
	}
	enc, err := jose.NewEncrypter(jose.A256GCM, jose.Recipient{Algorithm: jose.DIRECT, Key: key, KeyID: name},
		(&jose.EncrypterOptions{}).WithType(sealType))
	if err != nil {
		return "", err
	}
	b, err := proto.Marshal(c)
	if err != nil {
		return "", err
	}
	obj, err := enc.Encrypt(b)
	if err != nil {
		return "", err
	}
	return obj.CompactSerialize()
}

// unseal opens raw and checks it belongs to user's session.
func (s *Service) unseal(ctx context.Context, raw, userID, session string) (*authv2.Sealed, error) {
	obj, err := jose.ParseEncrypted(raw, []jose.KeyAlgorithm{jose.DIRECT}, []jose.ContentEncryption{jose.A256GCM})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errSeal, err)
	}
	if typ, _ := obj.Header.ExtraHeaders[jose.HeaderType].(string); typ != sealType {
		return nil, fmt.Errorf("%w: type %q", errSeal, typ)
	}
	key, err := s.keys.SealKeyNamed(ctx, obj.Header.KeyID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errSeal, err)
	}
	pt, err := obj.Decrypt(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errSeal, err)
	}
	var c authv2.Sealed
	if err := proto.Unmarshal(pt, &c); err != nil {
		return nil, fmt.Errorf("%w: %w", errSeal, err)
	}
	if c.GetUser() != userID || c.GetSession() != session {
		return nil, fmt.Errorf("%w: sealed for another session", errSeal)
	}
	return &c, nil
}

// describe is what a client is told about userID's connections: never a
// credential, and whether each forge account signs in as userID.
//
// THE SIGN-IN LINK IS READ HERE, AS OF NOW, not stamped at connect: who an
// account signs in as is the deterministic winner of the replicated claims
// (store.go winner — earliest claim, lowest user id on a tie), and a claim
// written on another node can change it after the connection was made.
func (s *Service) describe(ctx context.Context, userID string, conns []*authv2.ForgeToken) ([]*authv2.Connection, error) {
	out := make([]*authv2.Connection, 0, len(conns))
	for _, c := range conns {
		d := &authv2.Connection{
			Host: c.GetHost(), Subject: c.GetSubject(), Login: c.GetLogin(), ExpiresUnix: c.GetExpiresUnix(),
			Scopes: c.GetScopes(), Refreshable: c.GetRefreshToken() != "",
		}
		switch u, err := s.linked(ctx, c.GetHost(), c.GetSubject()); {
		case errors.Is(err, ErrNotFound):
		case err != nil:
			return nil, connect.NewError(connect.CodeUnavailable, err)
		case u.ID == userID:
			d.SignIn = authv2.SignInLink_SIGN_IN_LINK_THIS_USER
		default:
			d.SignIn = authv2.SignInLink_SIGN_IN_LINK_OTHER_USER
		}
		out = append(out, d)
	}
	return out, nil
}

// withConnection is conns with c replacing any connection to its host.
func withConnection(conns []*authv2.ForgeToken, c *authv2.ForgeToken) []*authv2.ForgeToken {
	return append(withoutConnection(conns, c.GetHost()), c)
}

func withoutConnection(conns []*authv2.ForgeToken, host string) []*authv2.ForgeToken {
	out := make([]*authv2.ForgeToken, 0, len(conns))
	for _, c := range conns {
		if c.GetHost() != host {
			out = append(out, c)
		}
	}
	return out
}

// newerCredential reports whether a is a newer credential of its connection
// than b: renewed more often, then obtained later, then — two renewals from
// one credential in the same instant — by a total order on the token, so
// every node picks the same one.
func newerCredential(a, b *authv2.ForgeToken) bool {
	if a.GetRenewals() != b.GetRenewals() {
		return a.GetRenewals() > b.GetRenewals()
	}
	if a.GetObtainedUnixNano() != b.GetObtainedUnixNano() {
		return a.GetObtainedUnixNano() > b.GetObtainedUnixNano()
	}
	return a.GetAccessToken() > b.GetAccessToken()
}

// merge is one session's seals made one: per host, the newest credential.
//
// PER HOST AND NOT PER SEAL, because two requests renewing two different
// hosts from one token each hold a seal with one renewal; keeping either
// whole would throw the other's away. Merging cannot resurrect a dropped
// connection: a session's set of hosts never changes — connecting and
// disconnecting mint a new session — so every seal of one session names the
// same hosts.
//
// It returns which of seals to use, when one already holds every winner, so
// an unchanged session is not resealed; or -1, and the connections and
// generation a new seal must be made with.
func merge(seals []*authv2.Sealed) (best int, conns []*authv2.ForgeToken, gen int64) {
	byHost := map[string]*authv2.ForgeToken{}
	for _, sl := range seals {
		gen = max(gen, sl.GetGeneration())
		for _, c := range sl.GetConnections() {
			if cur, ok := byHost[c.GetHost()]; !ok || newerCredential(c, cur) {
				byHost[c.GetHost()] = c
			}
		}
	}
	best = -1
	for i, sl := range seals {
		if holdsAll(sl, byHost) && (best < 0 || sl.GetGeneration() > seals[best].GetGeneration()) {
			best = i
		}
	}
	if best >= 0 {
		return best, seals[best].GetConnections(), seals[best].GetGeneration()
	}
	for _, c := range byHost {
		conns = append(conns, c)
	}
	slices.SortFunc(conns, func(a, b *authv2.ForgeToken) int { return strings.Compare(a.GetHost(), b.GetHost()) })
	return -1, conns, gen + 1
}

func holdsAll(sl *authv2.Sealed, byHost map[string]*authv2.ForgeToken) bool {
	if len(sl.GetConnections()) != len(byHost) {
		return false
	}
	for _, c := range sl.GetConnections() {
		if !proto.Equal(c, byHost[c.GetHost()]) {
			return false
		}
	}
	return true
}
