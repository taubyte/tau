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
// also carry the session it was minted from and an opaque sealed value (the
// user's forge connections, encrypted by auth): a signature makes a token
// tamper-proof, not secret, so anything private in it is sealed before it
// gets here.
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
	"slices"
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

// Signer signs with one key.
type Signer struct {
	key    ed25519.PrivateKey
	kid    string
	issuer string
}

// NewSigner signs as issuer with key.
func NewSigner(issuer string, key ed25519.PrivateKey) (*Signer, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("token: not an ed25519 private key")
	}
	if issuer == "" {
		return nil, errors.New("token: no issuer")
	}
	return &Signer{key: key, kid: KeyID(key.Public().(ed25519.PublicKey)), issuer: issuer}, nil
}

// KeyID is the kid this signer's tokens carry.
func (s *Signer) KeyID() string { return s.kid }

// Claims are what a token says.
type Claims struct {
	Subject string
	ID      string
	Issued  time.Time
	Expires time.Time
	// Session is the session the token was minted from; empty for none.
	Session string
	// Sealed is carried as given and never read here.
	Sealed string
	// Generation orders two tokens of one session (authv2.Sealed.generation);
	// it is in the clear so a client can compare without opening the seal.
	Generation int64
}

// claims is the JWT body: the registered claims plus tau's two.
type claims struct {
	jwt.RegisteredClaims
	Session string `json:"sid,omitempty"`
	Sealed  string `json:"tau_conn,omitempty"`
	Gen     int64  `json:"tau_gen,omitempty"`
}

// Sign mints a token.
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
	}, Session: c.Session, Sealed: c.Sealed, Gen: c.Generation})
	t.Header["kid"] = s.kid
	t.Header["typ"] = Type
	return t.SignedString(s.key)
}

// Keys resolves a kid to the public key that signs under it.
type Keys func(kid string) (ed25519.PublicKey, error)

// Verifier checks tokens issued by issuer.
type Verifier struct {
	issuer string
	keys   Keys
	now    func() time.Time
}

// NewVerifier verifies tokens issued by issuer, with keys from keys.
func NewVerifier(issuer string, keys Keys, now func() time.Time) (*Verifier, error) {
	if issuer == "" || keys == nil {
		return nil, errors.New("token: a verifier needs an issuer and keys")
	}
	if now == nil {
		now = time.Now
	}
	return &Verifier{issuer: issuer, keys: keys, now: now}, nil
}

// Verify checks raw and returns its claims.
func (v *Verifier) Verify(raw string) (Claims, error) {
	return v.verify(raw, false)
}

// VerifyExpired checks raw as Verify does except for its expiry: it is for
// reading what an expired token carried, never for admitting its holder.
func (v *Verifier) VerifyExpired(raw string) (Claims, error) {
	return v.verify(raw, true)
}

func (v *Verifier) verify(raw string, expired bool) (Claims, error) {
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
	if expired {
		// The expiry is the one claim skipped; the rest are checked below.
		opts = append(opts, jwt.WithoutClaimsValidation())
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
	if expired && (rc.Issuer != v.issuer || !slices.Contains(rc.Audience, Audience) || rc.ExpiresAt == nil ||
		rc.IssuedAt == nil || rc.IssuedAt.After(v.now().Add(Skew))) {
		return Claims{}, fmt.Errorf("%w: issuer, audience or times", ErrInvalid)
	}
	if rc.Subject == "" || rc.ID == "" || rc.IssuedAt == nil {
		return Claims{}, fmt.Errorf("%w: missing subject, id or issue time", ErrInvalid)
	}
	return Claims{Subject: rc.Subject, ID: rc.ID, Issued: rc.IssuedAt.Time, Expires: rc.ExpiresAt.Time, Session: rc.Session, Sealed: rc.Sealed, Generation: rc.Gen}, nil
}
