// Package oidc is the client half of OpenID Connect that a bring-your-own
// provider needs: discovery, and verification of an ID token.
//
// EVERY CHECK OIDC CORE §3.1.3.7 NAMES IS MADE HERE, explicitly, because a
// library that verifies a signature does not know the nonce this cloud sent or
// the client it registered: the issuer exactly as configured, the audience
// containing this client, azp when there is more than one audience, an expiry,
// and the nonce. The issuer a discovery document reports must be the one asked
// for, so a document cannot redirect trust to another issuer.
//
// The provider is an operator's configuration, never a caller's choice, so the
// only URLs fetched are ones an operator named or that the named issuer's own
// document returned. Redirects are not followed.
package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
)

// Document is the part of a discovery document a sign-in reads.
type Document struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	DeviceEndpoint        string `json:"device_authorization_endpoint"`
}

// maxBody bounds a document or a key set.
const maxBody = 1 << 20

// Algorithms are the signing algorithms accepted on an ID token.
var Algorithms = []string{"RS256", "RS384", "RS512", "PS256", "ES256", "ES384", "EdDSA"}

// Skew is the clock skew allowed on an ID token.
const Skew = time.Minute

// Provider is one discovered issuer.
type Provider struct {
	doc    Document
	client *http.Client
	now    func() time.Time

	mu      sync.Mutex
	keys    jose.JSONWebKeySet
	fetched time.Time
}

// keysTTL is how long a key set is trusted before it is fetched again. A kid
// the cached set lacks refetches at once, at most every minKeysRefetch.
const (
	keysTTL        = time.Hour
	minKeysRefetch = 10 * time.Second
)

// Client returns client with redirects refused, which is how every fetch here
// is made.
func Client(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

// Discover fetches issuer's discovery document.
func Discover(ctx context.Context, client *http.Client, issuer string, now func() time.Time) (*Provider, error) {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" && !loopback(u) || u.Host == "" {
		return nil, fmt.Errorf("oidc: issuer %q is not an https URL", issuer)
	}
	if now == nil {
		now = time.Now
	}
	p := &Provider{client: Client(client), now: now}
	if err := p.get(ctx, strings.TrimSuffix(issuer, "/")+"/.well-known/openid-configuration", &p.doc); err != nil {
		return nil, err
	}
	if p.doc.Issuer != issuer {
		return nil, fmt.Errorf("oidc: discovery for %q reports issuer %q", issuer, p.doc.Issuer)
	}
	for name, v := range map[string]string{"authorization_endpoint": p.doc.AuthorizationEndpoint, "token_endpoint": p.doc.TokenEndpoint, "jwks_uri": p.doc.JWKSURI} {
		eu, err := url.Parse(v)
		if v == "" || err != nil || eu.Scheme != "https" && !loopback(eu) {
			return nil, fmt.Errorf("oidc: %s %q is not an https URL", name, v)
		}
	}
	return p, nil
}

// loopback admits plain http to a loopback host, which is what a test issuer
// and a provider on the same machine are.
func loopback(u *url.URL) bool {
	if u.Scheme != "http" {
		return false
	}
	h := u.Hostname()
	return h == "127.0.0.1" || h == "::1" || h == "localhost"
}

// Document is what discovery returned.
func (p *Provider) Document() Document { return p.doc }

func (p *Provider) get(ctx context.Context, u string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("oidc: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("oidc: fetching %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oidc: fetching %s: %s", u, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("oidc: reading %s: %w", u, err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("oidc: decoding %s: %w", u, err)
	}
	return nil
}

// key returns the public key for kid, refetching the set when it is stale or
// does not hold kid.
func (p *Provider) key(ctx context.Context, kid string) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	find := func() any {
		for _, k := range p.keys.Keys {
			if (kid == "" || k.KeyID == kid) && k.Use != "enc" && k.Valid() {
				return k.Key
			}
		}
		return nil
	}
	if k := find(); k != nil && now.Sub(p.fetched) < keysTTL {
		return k, nil
	}
	if now.Sub(p.fetched) >= minKeysRefetch {
		var set jose.JSONWebKeySet
		if err := p.get(ctx, p.doc.JWKSURI, &set); err != nil {
			return nil, err
		}
		p.keys, p.fetched = set, now
	}
	if k := find(); k != nil {
		return k, nil
	}
	return nil, fmt.Errorf("oidc: no signing key %q", kid)
}

// Identity is what a verified ID token says.
type Identity struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

type idClaims struct {
	jwt.RegisteredClaims
	Nonce         string `json:"nonce"`
	AZP           string `json:"azp"`
	Email         string `json:"email"`
	EmailVerified *bool  `json:"email_verified"`
	Name          string `json:"name"`
	PreferredName string `json:"preferred_username"`
}

// ErrInvalid is an ID token that does not verify.
var ErrInvalid = errors.New("oidc: invalid id token")

// Verify checks raw as an ID token issued to clientID in answer to a request
// that sent nonce.
func (p *Provider) Verify(ctx context.Context, raw, clientID, nonce string) (Identity, error) {
	if clientID == "" || nonce == "" {
		return Identity{}, errors.New("oidc: verifying needs the client id and the nonce sent")
	}
	var c idClaims
	parsed, err := jwt.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return p.key(ctx, kid)
	},
		jwt.WithValidMethods(Algorithms),
		jwt.WithIssuer(p.doc.Issuer),
		jwt.WithAudience(clientID),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(Skew),
		jwt.WithTimeFunc(p.now),
	)
	if err != nil || !parsed.Valid {
		return Identity{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if c.Subject == "" {
		return Identity{}, fmt.Errorf("%w: no subject", ErrInvalid)
	}
	if c.Nonce != nonce {
		return Identity{}, fmt.Errorf("%w: nonce mismatch", ErrInvalid)
	}
	if len(c.Audience) > 1 && c.AZP != clientID || c.AZP != "" && c.AZP != clientID {
		return Identity{}, fmt.Errorf("%w: authorized party %q", ErrInvalid, c.AZP)
	}
	name := c.PreferredName
	if name == "" {
		name = c.Name
	}
	return Identity{
		Issuer:        c.Issuer,
		Subject:       c.Subject,
		Email:         c.Email,
		EmailVerified: c.EmailVerified != nil && *c.EmailVerified,
		Name:          name,
	}, nil
}
