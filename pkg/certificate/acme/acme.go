// Package acme issues the certificates a cloud's own zone needs from an ACME
// CA, with golang.org/x/crypto/acme speaking the protocol.
//
// # The challenge is HTTP-01, answered statelessly
//
// A generated name resolves to every gateway node serving it, and a CA
// validating a challenge reaches whichever one its resolver picks — not
// necessarily the node that asked. An HTTP-01 answer is the token and the
// account key's thumbprint and nothing else, so a node holding the cloud's
// account key answers ANY node's challenge from the token in the URL: no
// shared state, no coordination, nothing to replicate. TLS-ALPN-01 carries no
// token in the handshake, so its answer is per-order state on one node, and
// DNS-01 would need the dns service to serve records written at issuance and
// replicated to every dns node before the CA looks — new replicated state for
// the one thing HTTP-01 already does.
//
// What HTTP-01 cannot do is a wildcard. A generated name is two labels under
// the zone (<resource>.<project>.<zone>), which one wildcard does not cover
// anyway, so each name gets its own certificate, on its first handshake; the
// operator's wildcard, when one is supplied, still answers what it covers.
//
// # What a node keeps
//
// Each certificate and its key, and a minted account key, live in the
// directory the node gives New (under its root), so a restart reissues
// nothing. A certificate is renewed once two thirds of its lifetime has gone,
// on a handshake that finds it so, while it keeps being served.
//
// The ceiling: each gateway node issues and keeps its own certificate for a
// name, so a name served by N nodes costs N issuances. Sharing issued
// certificates across nodes is the upgrade, and it is a store, not a protocol
// change.
package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	xacme "golang.org/x/crypto/acme"
	"golang.org/x/sync/singleflight"

	"github.com/taubyte/tau/pkg/certificate"
)

// ChallengePrefix is the path a CA fetches an HTTP-01 answer from.
const ChallengePrefix = "/.well-known/acme-challenge/"

// DefaultTimeout bounds one issuance, from the order to the certificate.
const DefaultTimeout = 2 * time.Minute

var (
	ErrDisabled = errors.New("acme: issuance is not enabled")
	ErrNoDir    = errors.New("acme: issued certificates need a directory to be kept in")
)

// Issuer issues and keeps certificates for names.
type Issuer struct {
	client  *xacme.Client
	dir     string
	account *xacme.Account
	timeout time.Duration
	now     func() time.Time
	logf    func(string, ...any)

	regMu      sync.Mutex
	registered bool

	mu     sync.Mutex
	certs  map[string]*tls.Certificate
	flight singleflight.Group
}

// Option configures an Issuer.
type Option func(*Issuer)

// WithClock injects the clock renewal is decided by. Tests use it.
func WithClock(now func() time.Time) Option { return func(i *Issuer) { i.now = now } }

// WithTimeout bounds one issuance. Default DefaultTimeout.
func WithTimeout(d time.Duration) Option { return func(i *Issuer) { i.timeout = d } }

// WithLogger is where renewal failures, which no caller waits on, are said.
func WithLogger(logf func(string, ...any)) Option { return func(i *Issuer) { i.logf = logf } }

// New builds an issuer from an operator's config, keeping what it issues in
// dir. It reaches no CA: the account is registered on the first issuance.
func New(cfg certificate.ACME, dir string, opts ...Option) (*Issuer, error) {
	if !cfg.Enabled {
		return nil, ErrDisabled
	}
	if dir == "" {
		return nil, ErrNoDir
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("acme: %w", err)
	}

	key, err := accountKey(cfg.AccountKey, filepath.Join(dir, "account.key"))
	if err != nil {
		return nil, err
	}
	httpClient, err := transport(cfg)
	if err != nil {
		return nil, err
	}
	account, err := accountOf(cfg)
	if err != nil {
		return nil, err
	}

	directory := cfg.Directory
	if directory == "" {
		directory = certificate.LetsEncrypt
	}
	i := &Issuer{
		client:  &xacme.Client{Key: key, DirectoryURL: directory, HTTPClient: httpClient},
		dir:     dir,
		account: account,
		timeout: DefaultTimeout,
		now:     time.Now,
		logf:    func(string, ...any) {},
		certs:   map[string]*tls.Certificate{},
	}
	for _, opt := range opts {
		opt(i)
	}
	return i, nil
}

func accountOf(cfg certificate.ACME) (*xacme.Account, error) {
	a := &xacme.Account{}
	if cfg.Email != "" {
		a.Contact = []string{"mailto:" + cfg.Email}
	}
	switch {
	case cfg.EAB.KeyID == "" && cfg.EAB.HMAC == "":
	case cfg.EAB.KeyID == "" || cfg.EAB.HMAC == "":
		return nil, errors.New("acme: an external account binding needs both its key id and its hmac key")
	default:
		mac, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(cfg.EAB.HMAC, "="))
		if err != nil {
			return nil, fmt.Errorf("acme: the external account binding's hmac key is not base64url: %w", err)
		}
		a.ExternalAccountBinding = &xacme.ExternalAccountBinding{KID: cfg.EAB.KeyID, Key: mac}
	}
	return a, nil
}

// transport is how the directory is reached: the system's roots plus the
// operator's, or no verification when that was asked for.
func transport(cfg certificate.ACME) (*http.Client, error) {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.InsecureSkipVerify}
	if cfg.RootCA != "" {
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(cfg.RootCA)) {
			return nil, errors.New("acme: the root CA holds no certificate")
		}
		t.TLSClientConfig.RootCAs = pool
	}
	return &http.Client{Transport: t, Timeout: time.Minute}, nil
}

// accountKey is the configured key, or the one kept at path, or a new one
// kept there.
func accountKey(configured, path string) (crypto.Signer, error) {
	if configured != "" {
		return parseKey([]byte(configured))
	}
	if data, err := os.ReadFile(path); err == nil {
		return parseKey(data)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := write(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})); err != nil {
		return nil, err
	}
	return key, nil
}

func parseKey(data []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("acme: the account key is not PEM")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		if s, ok := key.(crypto.Signer); ok {
			switch s.(type) {
			case *rsa.PrivateKey, *ecdsa.PrivateKey:
				return s, nil
			}
		}
		return nil, fmt.Errorf("acme: an account key of type %T cannot sign ACME requests", key)
	}
	return nil, fmt.Errorf("acme: unsupported account key type %q", block.Type)
}

// Challenge is the HTTP-01 answer for token, derived from the account key
// alone. False for a token no CA would send.
func (i *Issuer) Challenge(token string) (string, bool) {
	if token == "" || strings.Trim(token, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
		return "", false
	}
	answer, err := i.client.HTTP01ChallengeResponse(token)
	return answer, err == nil
}

// Certificate is a certificate for name: the one kept, or one issued now.
func (i *Issuer) Certificate(ctx context.Context, name string) (tls.Certificate, error) {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if !hostname(name) {
		return tls.Certificate{}, fmt.Errorf("acme: %q is not a name a certificate can be issued for", name)
	}

	if cert := i.kept(name); cert != nil {
		if i.due(cert.Leaf) {
			// STILL VALID, so it keeps answering while its successor is
			// issued; a renewal that fails is retried on a later handshake.
			go func() {
				if _, err := i.issueOnce(name); err != nil {
					i.logf("acme: renewing %s: %v", name, err)
				}
			}()
		}
		return *cert, nil
	}

	res := i.flight.DoChan(name, func() (any, error) { return i.issue(name) })
	select {
	case <-ctx.Done():
		return tls.Certificate{}, fmt.Errorf("acme: issuing %s: %w", name, ctx.Err())
	case r := <-res:
		if r.Err != nil {
			return tls.Certificate{}, r.Err
		}
		return *r.Val.(*tls.Certificate), nil
	}
}

func (i *Issuer) issueOnce(name string) (*tls.Certificate, error) {
	v, err, _ := i.flight.Do(name, func() (any, error) { return i.issue(name) })
	if err != nil {
		return nil, err
	}
	return v.(*tls.Certificate), nil
}

// hostname is a DNS name, which is also what makes it safe as a file name.
func hostname(name string) bool {
	if name == "" || len(name) > 253 || strings.HasPrefix(name, ".") || strings.Contains(name, "..") {
		return false
	}
	return strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-.") == ""
}

// kept is the certificate in memory or on disk, unexpired, or nil.
func (i *Issuer) kept(name string) *tls.Certificate {
	i.mu.Lock()
	cert := i.certs[name]
	i.mu.Unlock()
	if cert == nil {
		data, err := os.ReadFile(i.path(name))
		if err != nil {
			return nil
		}
		parsed, err := certificate.KeyPair(data)
		if err != nil {
			return nil
		}
		if parsed.Leaf == nil {
			if parsed.Leaf, err = x509.ParseCertificate(parsed.Certificate[0]); err != nil {
				return nil
			}
		}
		cert = &parsed
		i.remember(name, cert)
	}
	if !i.now().Before(cert.Leaf.NotAfter) {
		return nil
	}
	return cert
}

func (i *Issuer) remember(name string, cert *tls.Certificate) {
	i.mu.Lock()
	i.certs[name] = cert
	i.mu.Unlock()
}

// due is a certificate with less than a third of its lifetime left.
func (i *Issuer) due(leaf *x509.Certificate) bool {
	lifetime := leaf.NotAfter.Sub(leaf.NotBefore)
	return leaf.NotAfter.Sub(i.now()) < lifetime/3
}

func (i *Issuer) path(name string) string { return filepath.Join(i.dir, name+".pem") }

func (i *Issuer) register(ctx context.Context) error {
	i.regMu.Lock()
	defer i.regMu.Unlock()
	if i.registered {
		return nil
	}
	if _, err := i.client.Register(ctx, i.account, xacme.AcceptTOS); err != nil && !errors.Is(err, xacme.ErrAccountAlreadyExists) {
		return fmt.Errorf("acme: registering the account: %w", err)
	}
	i.registered = true
	return nil
}

// issue orders, proves and fetches a certificate for name, and keeps it.
func (i *Issuer) issue(name string) (*tls.Certificate, error) {
	ctx, cancel := context.WithTimeout(context.Background(), i.timeout)
	defer cancel()

	if err := i.register(ctx); err != nil {
		return nil, err
	}
	order, err := i.client.AuthorizeOrder(ctx, xacme.DomainIDs(name))
	if err != nil {
		return nil, fmt.Errorf("acme: ordering %s: %w", name, err)
	}
	for _, u := range order.AuthzURLs {
		if err := i.prove(ctx, u); err != nil {
			return nil, fmt.Errorf("acme: proving %s: %w", name, err)
		}
	}
	// The order as first returned keeps its finalize URL; a re-read of it
	// need not carry one.
	finalize := order.FinalizeURL
	if _, err := i.client.WaitOrder(ctx, order.URI); err != nil {
		return nil, fmt.Errorf("acme: %s: %w", name, err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: []string{name}}, key)
	if err != nil {
		return nil, err
	}
	chain, err := i.finalize(ctx, order.URI, finalize, csr)
	if err != nil {
		return nil, fmt.Errorf("acme: finalizing %s: %w", name, err)
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return nil, fmt.Errorf("acme: %s: %w", name, err)
	}
	if err := leaf.VerifyHostname(name); err != nil {
		return nil, fmt.Errorf("acme: the CA issued a certificate that does not name %s: %w", name, err)
	}

	cert := &tls.Certificate{Certificate: chain, PrivateKey: key, Leaf: leaf}
	if err := i.keep(name, chain, key); err != nil {
		return nil, err
	}
	i.remember(name, cert)
	return cert, nil
}

// finalize submits the CSR and fetches the chain.
//
// A CA WAITING TO ISSUE answers the finalize with an order still processing,
// and x/crypto polls it by the URL in that answer's Location header — which
// RFC 8555 does not require on a finalize, and pebble does not send. The order
// URL is known from the order itself, so a finalize that fails that way is
// finished by polling it there.
func (i *Issuer) finalize(ctx context.Context, orderURL, finalizeURL string, csr []byte) ([][]byte, error) {
	chain, _, err := i.client.CreateOrderCert(ctx, finalizeURL, csr, true)
	if err == nil {
		return chain, nil
	}
	o, werr := i.client.WaitOrder(ctx, orderURL)
	if werr != nil || o.Status != xacme.StatusValid || o.CertURL == "" {
		return nil, err
	}
	return i.client.FetchCert(ctx, o.CertURL, true)
}

func (i *Issuer) prove(ctx context.Context, url string) error {
	z, err := i.client.GetAuthorization(ctx, url)
	if err != nil {
		return err
	}
	if z.Status == xacme.StatusValid {
		return nil
	}
	for _, c := range z.Challenges {
		if c.Type != "http-01" {
			continue
		}
		if _, err := i.client.Accept(ctx, c); err != nil {
			return err
		}
		_, err := i.client.WaitAuthorization(ctx, z.URI)
		return err
	}
	return errors.New("the CA offers no http-01 challenge")
}

func (i *Issuer) keep(name string, chain [][]byte, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	var out []byte
	for _, c := range chain {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c})...)
	}
	out = append(out, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})...)
	return write(i.path(name), out)
}

// write replaces path whole, so a crash leaves the old file or the new one.
func write(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("acme: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("acme: %w", err)
	}
	return nil
}
