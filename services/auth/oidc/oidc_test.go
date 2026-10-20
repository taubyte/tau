package oidc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

type issuer struct {
	srv      *httptest.Server
	key      *ecdsa.PrivateKey
	kid      string
	doc      map[string]string
	jwksHits atomic.Int32
	now      time.Time
}

func newIssuer(t *testing.T) *issuer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	is := &issuer{key: key, kid: "k1", now: time.Unix(1_700_000_000, 0)}
	mux := http.NewServeMux()
	is.srv = httptest.NewServer(mux)
	t.Cleanup(is.srv.Close)
	is.doc = map[string]string{
		"issuer":                 is.srv.URL,
		"authorization_endpoint": is.srv.URL + "/authorize",
		"token_endpoint":         is.srv.URL + "/token",
		"jwks_uri":               is.srv.URL + "/jwks",
	}
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(is.doc)
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		is.jwksHits.Add(1)
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &is.key.PublicKey, KeyID: is.kid, Algorithm: "ES256", Use: "sig"},
		}})
	})
	return is
}

func (is *issuer) sign(t *testing.T, mutate func(jwt.MapClaims)) string {
	t.Helper()
	c := jwt.MapClaims{
		"iss": is.srv.URL, "sub": "alice-sub", "aud": "client-1", "nonce": "n1",
		"iat": is.now.Unix(), "exp": is.now.Add(time.Hour).Unix(),
		"email": "Alice@Example.com", "email_verified": true, "preferred_username": "alice",
	}
	if mutate != nil {
		mutate(c)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, c)
	tok.Header["kid"] = is.kid
	s, err := tok.SignedString(is.key)
	require.NoError(t, err)
	return s
}

func (is *issuer) provider(t *testing.T) *Provider {
	t.Helper()
	p, err := Discover(t.Context(), is.srv.Client(), is.srv.URL, func() time.Time { return is.now })
	require.NoError(t, err)
	return p
}

func TestVerify(t *testing.T) {
	is := newIssuer(t)
	p := is.provider(t)
	require.Equal(t, is.srv.URL+"/token", p.Document().TokenEndpoint)

	id, err := p.Verify(t.Context(), is.sign(t, nil), "client-1", "n1")
	require.NoError(t, err)
	require.Equal(t, Identity{Issuer: is.srv.URL, Subject: "alice-sub", Email: "Alice@Example.com", EmailVerified: true, Name: "alice"}, id)

	// The key set is cached.
	_, err = p.Verify(t.Context(), is.sign(t, nil), "client-1", "n1")
	require.NoError(t, err)
	require.EqualValues(t, 1, is.jwksHits.Load())

	// name falls back when there is no preferred username.
	id, err = p.Verify(t.Context(), is.sign(t, func(c jwt.MapClaims) { delete(c, "preferred_username"); c["name"] = "Alice A" }), "client-1", "n1")
	require.NoError(t, err)
	require.Equal(t, "Alice A", id.Name)

	// Two audiences with azp naming this client verify.
	_, err = p.Verify(t.Context(), is.sign(t, func(c jwt.MapClaims) { c["aud"] = []string{"client-1", "other"}; c["azp"] = "client-1" }), "client-1", "n1")
	require.NoError(t, err)
}

func TestVerifyRefuses(t *testing.T) {
	is := newIssuer(t)
	p := is.provider(t)

	_, err := p.Verify(t.Context(), "x", "", "n1")
	require.Error(t, err)

	cases := map[string]func(jwt.MapClaims){
		"wrong nonce":    func(c jwt.MapClaims) { c["nonce"] = "n2" },
		"no nonce":       func(c jwt.MapClaims) { delete(c, "nonce") },
		"wrong issuer":   func(c jwt.MapClaims) { c["iss"] = "https://evil" },
		"wrong audience": func(c jwt.MapClaims) { c["aud"] = "client-2" },
		"expired":        func(c jwt.MapClaims) { c["exp"] = is.now.Add(-2 * Skew).Unix() },
		"no expiry":      func(c jwt.MapClaims) { delete(c, "exp") },
		"no subject":     func(c jwt.MapClaims) { delete(c, "sub") },
		"multi aud no azp": func(c jwt.MapClaims) {
			c["aud"] = []string{"client-1", "other"}
		},
		"azp other": func(c jwt.MapClaims) { c["azp"] = "other" },
	}
	for name, m := range cases {
		_, err := p.Verify(t.Context(), is.sign(t, m), "client-1", "n1")
		require.ErrorIs(t, err, ErrInvalid, name)
	}

	// HS256 is not an accepted algorithm.
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": is.srv.URL, "sub": "a", "aud": "client-1", "nonce": "n1", "exp": is.now.Add(time.Hour).Unix()})
	raw, err := hs.SignedString([]byte("k"))
	require.NoError(t, err)
	_, err = p.Verify(t.Context(), raw, "client-1", "n1")
	require.ErrorIs(t, err, ErrInvalid)
}

func TestUnknownKidRefetchesBounded(t *testing.T) {
	is := newIssuer(t)
	p := is.provider(t)
	_, err := p.Verify(t.Context(), is.sign(t, nil), "client-1", "n1")
	require.NoError(t, err)

	// The provider rotates its key: a token under a kid the cached set lacks
	// refetches, but not more often than minKeysRefetch.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	is.key, is.kid = key, "k2"
	_, err = p.Verify(t.Context(), is.sign(t, nil), "client-1", "n1")
	require.ErrorIs(t, err, ErrInvalid)
	require.EqualValues(t, 1, is.jwksHits.Load())

	is.now = is.now.Add(minKeysRefetch)
	_, err = p.Verify(t.Context(), is.sign(t, nil), "client-1", "n1")
	require.NoError(t, err)
	require.EqualValues(t, 2, is.jwksHits.Load())

	// And an expired cache refetches even for a known kid.
	is.now = is.now.Add(keysTTL)
	_, err = p.Verify(t.Context(), is.sign(t, func(c jwt.MapClaims) {}), "client-1", "n1")
	require.NoError(t, err)
	require.EqualValues(t, 3, is.jwksHits.Load())
}

func TestDiscoverRefuses(t *testing.T) {
	_, err := Discover(t.Context(), nil, "http://example.com", nil)
	require.Error(t, err)
	_, err = Discover(t.Context(), nil, "::", nil)
	require.Error(t, err)

	is := newIssuer(t)
	is.doc["issuer"] = "https://someone-else"
	_, err = Discover(t.Context(), is.srv.Client(), is.srv.URL, nil)
	require.ErrorContains(t, err, "reports issuer")

	is = newIssuer(t)
	is.doc["jwks_uri"] = "ftp://x"
	_, err = Discover(t.Context(), is.srv.Client(), is.srv.URL, nil)
	require.ErrorContains(t, err, "jwks_uri")

	is = newIssuer(t)
	_, err = Discover(t.Context(), is.srv.Client(), is.srv.URL+"/missing", nil)
	require.Error(t, err)

	// A redirect is not followed.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, is.srv.URL+"/.well-known/openid-configuration", http.StatusFound)
	})
	redir := httptest.NewServer(mux)
	defer redir.Close()
	_, err = Discover(t.Context(), redir.Client(), redir.URL, nil)
	require.ErrorContains(t, err, "302")

	// Not JSON.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) }))
	defer bad.Close()
	_, err = Discover(t.Context(), bad.Client(), bad.URL, nil)
	require.ErrorContains(t, err, "decoding")

	// Unreachable.
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	_, err = Discover(t.Context(), nil, closed.URL, nil)
	require.Error(t, err)

	// A bad key set URL surfaces at verification.
	is = newIssuer(t)
	is.doc["jwks_uri"] = is.srv.URL + "/nope"
	p := is.provider(t)
	_, err = p.Verify(t.Context(), is.sign(t, nil), "client-1", "n1")
	require.ErrorIs(t, err, ErrInvalid)
	require.True(t, strings.Contains(err.Error(), "404"))
}

func TestClient(t *testing.T) {
	c := Client(nil)
	require.NotNil(t, c.CheckRedirect)
	require.Equal(t, http.ErrUseLastResponse, c.CheckRedirect(nil, nil))
}
