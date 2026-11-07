package auto

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	acmeStore "github.com/taubyte/tau/services/auth/acme/store"
	"golang.org/x/crypto/acme/autocert"
	"gotest.tools/v3/assert"
)

// memStore stands in for the auth ACME store shared by every node.
type memStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newMemStore() *memStore { return &memStore{m: map[string][]byte{}} }

func (s *memStore) Get(_ context.Context, k string) ([]byte, error) { return s.Lookup(k) }
func (s *memStore) Lookup(k string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[k]; ok {
		return v, nil
	}
	return nil, autocert.ErrCacheMiss
}
func (s *memStore) Put(_ context.Context, k string, v []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
	return nil
}
func (s *memStore) Delete(_ context.Context, k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, k)
	return nil
}

// selfSigned returns a cert+key in autocert's PEM layout for name, valid for life.
func selfSigned(t *testing.T, name string, life time.Duration) []byte {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	assert.NilError(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(life)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	assert.NilError(t, err)
	kb, err := x509.MarshalECPrivateKey(key)
	assert.NilError(t, err)
	return append(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
}

var genRe = regexp.MustCompile(`^[^.]+\.g\.example\.com$`)

func wildNode(t *testing.T, st *memStore, orders *atomic.Int32, fail bool) *Service {
	WildcardSettle = 0
	return &Service{
		wildcardBase:    "g.example.com",
		skipDomainProof: genRe.MatchString,
		certStore:       st,
		orderWildcard: func(ctx context.Context, base string) ([]byte, error) {
			orders.Add(1)
			if fail {
				return nil, errors.New("acme down")
			}
			return selfSigned(t, "*."+base, 90*24*time.Hour), nil
		},
	}
}

type perHost struct{ hosts []string }

func (p *perHost) get(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
	p.hosts = append(p.hosts, h.ServerName)
	return &tls.Certificate{}, nil
}

func hello(h string) *tls.ClientHelloInfo { return &tls.ClientHelloInfo{ServerName: h} }

func TestWildcardOneOrderForGeneratedHosts(t *testing.T) {
	st, orders := newMemStore(), &atomic.Int32{}
	a, b := wildNode(t, st, orders, false), wildNode(t, st, orders, false)
	ctx := context.Background()
	assert.NilError(t, a.ensureWildcard(ctx))
	assert.NilError(t, a.ensureWildcard(ctx))
	assert.NilError(t, b.ensureWildcard(ctx)) // second node takes it from the store
	assert.Equal(t, orders.Load(), int32(1))

	ph := &perHost{}
	get := a.getCertificate(ph.get)
	c1, err := get(hello("one.g.example.com"))
	assert.NilError(t, err)
	c2, err := get(hello("TWO.g.example.com"))
	assert.NilError(t, err)
	assert.Assert(t, c1 == c2)
	assert.DeepEqual(t, c1.Leaf.DNSNames, []string{"*.g.example.com"})
	assert.Equal(t, len(ph.hosts), 0)

	c3, err := b.getCertificate(ph.get)(hello("three.g.example.com"))
	assert.NilError(t, err)
	assert.DeepEqual(t, c3.Certificate, c1.Certificate)
}

func TestWildcardCustomDomainStaysPerHost(t *testing.T) {
	st, orders := newMemStore(), &atomic.Int32{}
	a := wildNode(t, st, orders, false)
	assert.NilError(t, a.ensureWildcard(context.Background()))
	ph := &perHost{}
	_, err := a.getCertificate(ph.get)(hello("shop.custom.org"))
	assert.NilError(t, err)
	// a deeper name is not covered by *.g.example.com either
	_, err = a.getCertificate(ph.get)(hello("x.y.g.example.com"))
	assert.NilError(t, err)
	assert.DeepEqual(t, ph.hosts, []string{"shop.custom.org", "x.y.g.example.com"})
}

func TestWildcardOrderFailureFallsBackPerHost(t *testing.T) {
	st, orders := newMemStore(), &atomic.Int32{}
	a := wildNode(t, st, orders, true)
	assert.Assert(t, a.ensureWildcard(context.Background()) != nil)
	ph := &perHost{}
	_, err := a.getCertificate(ph.get)(hello("one.g.example.com"))
	assert.NilError(t, err)
	assert.DeepEqual(t, ph.hosts, []string{"one.g.example.com"})
}

func TestWildcardRenewsNearExpiry(t *testing.T) {
	st, orders := newMemStore(), &atomic.Int32{}
	st.m["*.g.example.com"] = selfSigned(t, "*.g.example.com", 10*24*time.Hour)
	a := wildNode(t, st, orders, false)
	assert.NilError(t, a.ensureWildcard(context.Background()))
	assert.Equal(t, orders.Load(), int32(1))
}

func TestWildcardLockHeldByOtherNode(t *testing.T) {
	st, orders := newMemStore(), &atomic.Int32{}
	st.m[wildcardLockKey("g.example.com")] = acmeStore.EncodeChallenge("other", time.Now().Add(time.Minute))
	a := wildNode(t, st, orders, false)
	assert.Assert(t, a.ensureWildcard(context.Background()) != nil)
	assert.Equal(t, orders.Load(), int32(0))
}

func TestChallengeValue(t *testing.T) {
	v := acmeStore.EncodeChallenge("tok", time.Now().Add(time.Minute))
	txt, ok := acmeStore.DecodeChallenge(v)
	assert.Assert(t, ok)
	assert.Equal(t, txt, "tok")
	_, ok = acmeStore.DecodeChallenge(acmeStore.EncodeChallenge("tok", time.Now().Add(-time.Second)))
	assert.Assert(t, !ok)
}

// On t3 the challenge name was answered by the registrar's wildcard record,
// and the log gave no way to tell. The warning now says what to change.
func TestChallengeHintNamesTheDelegation(t *testing.T) {
	err := errors.New(`authorization error for gen.example.com: 403 urn:ietf:params:acme:error:unauthorized: Incorrect TXT record "/ip4/1.2.3.4/tcp/4242" found at _acme-challenge.gen.example.com`)
	if h := challengeHint("gen.example.com", err); !strings.Contains(h, "NS record") || !strings.Contains(h, "_acme-challenge.gen.example.com") {
		t.Fatalf("hint %q", h)
	}
	if h := challengeHint("gen.example.com", errors.New("rate limited")); h != "" {
		t.Fatalf("hint for another error: %q", h)
	}
}
