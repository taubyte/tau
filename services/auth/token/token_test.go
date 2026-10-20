package token

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return priv
}

type fixture struct {
	signer *Signer
	verify *Verifier
	now    time.Time
	pub    map[string]ed25519.PublicKey
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{now: time.Unix(1_700_000_000, 0), pub: map[string]ed25519.PublicKey{}}
	priv := newKey(t)
	s, err := NewSigner("tau:example.com", priv)
	require.NoError(t, err)
	f.signer = s
	f.pub[s.KeyID()] = priv.Public().(ed25519.PublicKey)
	v, err := NewVerifier("tau:example.com", func(kid string) (ed25519.PublicKey, error) {
		if p, ok := f.pub[kid]; ok {
			return p, nil
		}
		return nil, errors.New("unknown kid")
	}, func() time.Time { return f.now })
	require.NoError(t, err)
	f.verify = v
	return f
}

func (f *fixture) claims() Claims {
	return Claims{Subject: "u1", ID: "t1", Issued: f.now, Expires: f.now.Add(15 * time.Minute)}
}

func TestRoundTrip(t *testing.T) {
	f := newFixture(t)
	raw, err := f.signer.Sign(f.claims())
	require.NoError(t, err)
	got, err := f.verify.Verify(raw)
	require.NoError(t, err)
	require.Equal(t, "u1", got.Subject)
	require.Equal(t, "t1", got.ID)
	require.True(t, got.Expires.Equal(f.now.Add(15*time.Minute)))

	// Within skew, still valid; beyond it, not.
	f.now = f.now.Add(15*time.Minute + Skew/2)
	_, err = f.verify.Verify(raw)
	require.NoError(t, err)
	f.now = f.now.Add(Skew)
	_, err = f.verify.Verify(raw)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestConstructors(t *testing.T) {
	_, err := NewSigner("x", ed25519.PrivateKey{1})
	require.Error(t, err)
	_, err = NewSigner("", newKey(t))
	require.Error(t, err)
	_, err = NewVerifier("", nil, nil)
	require.Error(t, err)
	v, err := NewVerifier("x", func(string) (ed25519.PublicKey, error) { return nil, nil }, nil)
	require.NoError(t, err)
	require.NotNil(t, v.now)

	f := newFixture(t)
	_, err = f.signer.Sign(Claims{Subject: "u"})
	require.Error(t, err)
}

func TestRefusals(t *testing.T) {
	f := newFixture(t)
	good := f.claims()

	sign := func(method jwt.SigningMethod, key any, header map[string]any, rc jwt.RegisteredClaims) string {
		tok := jwt.NewWithClaims(method, rc)
		for k, v := range header {
			tok.Header[k] = v
		}
		s, err := tok.SignedString(key)
		require.NoError(t, err)
		return s
	}
	base := func() jwt.RegisteredClaims {
		return jwt.RegisteredClaims{
			Issuer: "tau:example.com", Subject: "u1", Audience: jwt.ClaimStrings{Audience}, ID: "t1",
			IssuedAt: jwt.NewNumericDate(good.Issued), ExpiresAt: jwt.NewNumericDate(good.Expires),
		}
	}
	priv := newKey(t)
	pub := priv.Public().(ed25519.PublicKey)
	kid := KeyID(pub)
	f.pub[kid] = pub
	hdr := map[string]any{"kid": kid, "typ": Type}

	cases := map[string]string{
		"garbage":      "not.a.token",
		"wrong type":   sign(jwt.SigningMethodEdDSA, priv, map[string]any{"kid": kid, "typ": "JWT"}, base()),
		"no kid":       sign(jwt.SigningMethodEdDSA, priv, map[string]any{"typ": Type}, base()),
		"unknown kid":  sign(jwt.SigningMethodEdDSA, priv, map[string]any{"kid": "nope", "typ": Type}, base()),
		"hmac":         sign(jwt.SigningMethodHS256, []byte("secret"), hdr, base()),
		"none":         sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, hdr, base()),
		"wrong issuer": sign(jwt.SigningMethodEdDSA, priv, hdr, func() jwt.RegisteredClaims { c := base(); c.Issuer = "other"; return c }()),
		"wrong aud":    sign(jwt.SigningMethodEdDSA, priv, hdr, func() jwt.RegisteredClaims { c := base(); c.Audience = jwt.ClaimStrings{"x"}; return c }()),
		"no expiry":    sign(jwt.SigningMethodEdDSA, priv, hdr, func() jwt.RegisteredClaims { c := base(); c.ExpiresAt = nil; return c }()),
		"no subject":   sign(jwt.SigningMethodEdDSA, priv, hdr, func() jwt.RegisteredClaims { c := base(); c.Subject = ""; return c }()),
		"no id":        sign(jwt.SigningMethodEdDSA, priv, hdr, func() jwt.RegisteredClaims { c := base(); c.ID = ""; return c }()),
		"no iat":       sign(jwt.SigningMethodEdDSA, priv, hdr, func() jwt.RegisteredClaims { c := base(); c.IssuedAt = nil; return c }()),
		"future iat": sign(jwt.SigningMethodEdDSA, priv, hdr, func() jwt.RegisteredClaims {
			c := base()
			c.IssuedAt = jwt.NewNumericDate(good.Issued.Add(time.Hour))
			return c
		}()),
	}
	// A key published under another key's kid.
	other := newKey(t)
	f.pub["swapped"] = other.Public().(ed25519.PublicKey)
	cases["kid mismatch"] = sign(jwt.SigningMethodEdDSA, other, map[string]any{"kid": "swapped", "typ": Type}, base())

	for name, raw := range cases {
		_, err := f.verify.Verify(raw)
		require.ErrorIs(t, err, ErrInvalid, name)
	}

	// And the control: the same construction, correct, verifies.
	_, err := f.verify.Verify(sign(jwt.SigningMethodEdDSA, priv, hdr, base()))
	require.NoError(t, err)
}

func TestVerifyExpired(t *testing.T) {
	f := newFixture(t)
	c := f.claims()
	c.Session, c.Sealed, c.Generation = "sid", "opaque", 3
	raw, err := f.signer.Sign(c)
	require.NoError(t, err)
	f.now = f.now.Add(time.Hour)
	_, err = f.verify.Verify(raw)
	require.ErrorIs(t, err, ErrInvalid)
	got, err := f.verify.VerifyExpired(raw)
	require.NoError(t, err)
	require.Equal(t, "sid", got.Session)
	require.Equal(t, "opaque", got.Sealed)
	require.EqualValues(t, 3, got.Generation)

	// Everything but the expiry is still checked.
	other, err := NewSigner("tau:other", newKey(t))
	require.NoError(t, err)
	f.pub[other.KeyID()] = other.key.Public().(ed25519.PublicKey)
	raw, err = other.Sign(c)
	require.NoError(t, err)
	_, err = f.verify.VerifyExpired(raw)
	require.ErrorIs(t, err, ErrInvalid)
	c.Issued, c.Expires = f.now.Add(time.Hour), f.now.Add(2*time.Hour)
	raw, err = f.signer.Sign(c)
	require.NoError(t, err)
	_, err = f.verify.VerifyExpired(raw)
	require.ErrorIs(t, err, ErrInvalid, "issued in the future")
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims{RegisteredClaims: jwt.RegisteredClaims{Issuer: "tau:example.com", Subject: "u", ID: "i", Audience: jwt.ClaimStrings{"x"}, IssuedAt: jwt.NewNumericDate(f.now), ExpiresAt: jwt.NewNumericDate(f.now)}})
	tok.Header["kid"], tok.Header["typ"] = f.signer.KeyID(), Type
	raw, err = tok.SignedString(f.signer.key)
	require.NoError(t, err)
	_, err = f.verify.VerifyExpired(raw)
	require.ErrorIs(t, err, ErrInvalid, "another audience")
}
