package auth

import (
	"context"
	"errors"
	"fmt"

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

// describe is what a client is told about its connections.
func describe(conns []*authv2.ForgeToken) []*authv2.Connection {
	out := make([]*authv2.Connection, 0, len(conns))
	for _, c := range conns {
		out = append(out, &authv2.Connection{
			Host: c.GetHost(), Subject: c.GetSubject(), Login: c.GetLogin(), ExpiresUnix: c.GetExpiresUnix(),
			Scopes: c.GetScopes(), Refreshable: c.GetRefreshToken() != "",
		})
	}
	return out
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
