// Package token issues and verifies tau's access tokens: JWTs signed with
// Ed25519 (RFC 8037 EdDSA), and nothing else.
//
// ANY NODE VERIFIES WHAT ANY NODE SIGNED. Each auth node signs with its own
// key, held in the secrets service, and names it in the header's kid; the kid
// is derived from the public key, so two keys can never share one. A verifier
// is handed a lookup from kid to public key and asks it for every token.
//
// A token says who and until when, never what: a role is read from the
// store at verification, so a demotion does not wait for an expiry. It may
// also carry the session it was minted from. A signature makes a token
// tamper-proof, not secret, so nothing private goes in it.
//
// The checks follow RFC 8725: one algorithm, fixed by the verifier and never
// read from the token; an explicit type in the header so another JWT a node
// signs cannot be replayed as an access token; issuer and audience pinned;
// bounded clock skew.
package token

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Type is the JOSE typ every access token carries (RFC 9068).
const Type = "at+jwt"

// Audience is the one audience a tau access token is for.
const Audience = "tau"

// Skew is how far apart two nodes' clocks may be.
const Skew = 30 * time.Second

// ErrInvalid is a token that does not verify, for whatever reason. The reason
// is wrapped for logs and never shown to the caller.
var ErrInvalid = errors.New("token: invalid")

// KeyID is the kid a public key is named by: the first 16 bytes of its
// SHA-256, base64url.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return base64.RawURLEncoding.EncodeToString(sum[:16])
}

type Signer struct {
	key    ed25519.PrivateKey
	kid    string
	issuer string
}

func NewSigner(issuer string, key ed25519.PrivateKey) (*Signer, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("token: not an ed25519 private key")
	}
	if issuer == "" {
		return nil, errors.New("token: no issuer")
	}
	return &Signer{key: key, kid: KeyID(key.Public().(ed25519.PublicKey)), issuer: issuer}, nil
}

func (s *Signer) KeyID() string { return s.kid }

type Claims struct {
	Subject string
	ID      string
	Issued  time.Time
	Expires time.Time
	// Session is the session the token was minted from; empty for none.
	Session string
}

type claims struct {
	jwt.RegisteredClaims
	Session string `json:"sid,omitempty"`
}

func (s *Signer) Sign(c Claims) (string, error) {
	if c.Subject == "" || c.ID == "" || !c.Expires.After(c.Issued) {
		return "", errors.New("token: a token needs a subject, an id and an expiry after its issue")
	}
	t := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims{RegisteredClaims: jwt.RegisteredClaims{
		Issuer:    s.issuer,
		Subject:   c.Subject,
		Audience:  jwt.ClaimStrings{Audience},
		ID:        c.ID,
		IssuedAt:  jwt.NewNumericDate(c.Issued),
		NotBefore: jwt.NewNumericDate(c.Issued),
		ExpiresAt: jwt.NewNumericDate(c.Expires),
	}, Session: c.Session})
	t.Header["kid"] = s.kid
	t.Header["typ"] = Type
	return t.SignedString(s.key)
}

// Keys resolves a kid to the public key that signs under it.
type Keys func(kid string) (ed25519.PublicKey, error)

type Verifier struct {
	issuer string
	keys   Keys
	now    func() time.Time
}

func NewVerifier(issuer string, keys Keys, now func() time.Time) (*Verifier, error) {
	if issuer == "" || keys == nil {
		return nil, errors.New("token: a verifier needs an issuer and keys")
	}
	if now == nil {
		now = time.Now
	}
	return &Verifier{issuer: issuer, keys: keys, now: now}, nil
}

func (v *Verifier) Verify(raw string) (Claims, error) {
	var rc claims
	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(Skew),
		jwt.WithTimeFunc(v.now),
	}
	parsed, err := jwt.ParseWithClaims(raw, &rc, func(t *jwt.Token) (any, error) {
		if typ, _ := t.Header["typ"].(string); typ != Type {
			return nil, fmt.Errorf("type %q", typ)
		}
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("no kid")
		}
		pub, err := v.keys(kid)
		if err != nil {
			return nil, err
		}
		if len(pub) != ed25519.PublicKeySize || KeyID(pub) != kid {
			return nil, errors.New("key does not match its kid")
		}
		return pub, nil
	}, opts...)
	if err != nil || !parsed.Valid {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if rc.Subject == "" || rc.ID == "" || rc.IssuedAt == nil {
		return Claims{}, fmt.Errorf("%w: missing subject, id or issue time", ErrInvalid)
	}
	return Claims{Subject: rc.Subject, ID: rc.ID, Issued: rc.IssuedAt.Time, Expires: rc.ExpiresAt.Time, Session: rc.Session}, nil
}
