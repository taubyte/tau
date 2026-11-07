package auto

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	acmeStore "github.com/taubyte/tau/services/auth/acme/store"
	"golang.org/x/crypto/acme"
)

var (
	// WildcardRenewBefore is how long before expiry the wildcard is ordered again.
	WildcardRenewBefore = 30 * 24 * time.Hour
	// WildcardCheckEvery is how often a node reloads or renews the wildcard; WildcardRetry after a failure.
	WildcardCheckEvery = 12 * time.Hour
	WildcardRetry      = 10 * time.Minute
	// WildcardSettle lets a store write reach every auth node before it is relied on.
	WildcardSettle  = 5 * time.Second
	wildcardLockTTL = 10 * time.Minute
)

// lookuper reads a store entry without retry or local caching (acmeStore.Store.Lookup).
type lookuper interface {
	Lookup(name string) ([]byte, error)
}

func wildcardLockKey(base string) string { return "wildcard-order." + base + "+token" }

// getCertificate serves generated hosts from the shared wildcard and every
// other host (custom domains, ACME tls-alpn challenges, no wildcard yet) from perHost.
func (s *Service) getCertificate(perHost func(*tls.ClientHelloInfo) (*tls.Certificate, error)) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
		host := strings.ToLower(strings.TrimSuffix(h.ServerName, "."))
		c := s.wildcard.Load()
		if c != nil && s.skipDomainProof != nil && s.skipDomainProof(host) &&
			strings.HasSuffix(host, "."+s.wildcardBase) && !strings.Contains(strings.TrimSuffix(host, "."+s.wildcardBase), ".") &&
			!(len(h.SupportedProtos) == 1 && h.SupportedProtos[0] == acme.ALPNProto) {
			return c, nil
		}
		return perHost(h)
	}
}

// keepWildcard keeps the wildcard loaded and renewed for as long as the service runs.
func (s *Service) keepWildcard(ctx context.Context) {
	for {
		wait := WildcardCheckEvery
		if err := s.ensureWildcard(ctx); err != nil {
			logger.Warnf("wildcard *.%s: %s; generated hosts use per-host certificates%s", s.wildcardBase, err, challengeHint(s.wildcardBase, err))
			wait = WildcardRetry
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// ensureWildcard loads the wildcard from the shared store, or orders it when
// missing or near expiry. Only the node holding the store lock orders.
func (s *Service) ensureWildcard(ctx context.Context) error {
	name := "*." + s.wildcardBase
	if fresh(s.wildcard.Load()) {
		return nil
	}
	if data, err := s.certStore.Get(ctx, name); err == nil {
		if c, err := parseCert(data); err == nil && fresh(c) {
			s.wildcard.Store(c)
			return nil
		}
	}

	if err := s.lockWildcard(ctx); err != nil {
		return err
	}
	data, err := s.orderWildcard(ctx, s.wildcardBase)
	if err != nil {
		return fmt.Errorf("ordering %s: %w", name, err)
	}
	c, err := parseCert(data)
	if err != nil {
		return fmt.Errorf("ordered %s is unusable: %w", name, err)
	}
	if err := s.certStore.Put(ctx, name, data); err != nil {
		return fmt.Errorf("storing %s: %w", name, err)
	}
	s.wildcard.Store(c)
	return nil
}

// lockWildcard claims the right to order for wildcardLockTTL.
// shortcut: the auth store is a CRDT, not compare-and-swap; two nodes racing inside
// WildcardSettle can both order (2 certificates instead of 1). Upgrade to a raft lock if seen.
func (s *Service) lockWildcard(ctx context.Context) error {
	l, ok := s.certStore.(lookuper)
	if !ok {
		return errors.New("store cannot hold a lock")
	}
	key, me := wildcardLockKey(s.wildcardBase), s.lockOwner()
	if v, err := l.Lookup(key); err == nil {
		if owner, ok := acmeStore.DecodeChallenge(v); ok && owner != me {
			return fmt.Errorf("node %s is ordering it", owner)
		}
	}
	if err := s.certStore.Put(ctx, key, acmeStore.EncodeChallenge(me, time.Now().Add(wildcardLockTTL))); err != nil {
		return err
	}
	time.Sleep(WildcardSettle)
	v, err := l.Lookup(key)
	if err != nil {
		return err
	}
	if owner, _ := acmeStore.DecodeChallenge(v); owner != me {
		return fmt.Errorf("node %s is ordering it", owner)
	}
	return nil
}

func (s *Service) lockOwner() string {
	if s.clientNode != nil {
		return s.clientNode.ID().String()
	}
	return "local"
}

func fresh(c *tls.Certificate) bool {
	return c != nil && c.Leaf != nil && time.Until(c.Leaf.NotAfter) > WildcardRenewBefore
}

// parseCert reads autocert's PEM layout: the private key, then the chain.
func parseCert(data []byte) (*tls.Certificate, error) {
	c, err := tls.X509KeyPair(data, data)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// orderWildcardACME orders *.<base> with DNS-01. The TXT value is published in the
// auth store, where every Seer reads it to answer _acme-challenge.<base>.
func (s *Service) orderWildcardACME(ctx context.Context, base string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	client := s.acmeClient()
	if client.Key == nil {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		client.Key = k
	}
	if _, err := client.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return nil, fmt.Errorf("register: %w", err)
	}

	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs("*."+base))
	if err != nil {
		return nil, fmt.Errorf("new order: %w", err)
	}
	challengeKey := acmeStore.ChallengeKey(base)
	defer s.certStore.Put(context.Background(), challengeKey, acmeStore.EncodeChallenge("-", time.Time{}))
	for _, u := range order.AuthzURLs {
		z, err := client.GetAuthorization(ctx, u)
		if err != nil {
			return nil, err
		}
		if z.Status == acme.StatusValid {
			continue
		}
		var chal *acme.Challenge
		for _, c := range z.Challenges {
			if c.Type == "dns-01" {
				chal = c
			}
		}
		if chal == nil {
			return nil, errors.New("no dns-01 challenge offered")
		}
		txt, err := client.DNS01ChallengeRecord(chal.Token)
		if err != nil {
			return nil, err
		}
		if err := s.certStore.Put(ctx, challengeKey, acmeStore.EncodeChallenge(txt, time.Now().Add(10*time.Minute))); err != nil {
			return nil, err
		}
		waitForChallenge(ctx, "_acme-challenge."+base, txt)
		if _, err := client.Accept(ctx, chal); err != nil {
			return nil, fmt.Errorf("accept: %w", err)
		}
		if _, err := client.WaitAuthorization(ctx, z.URI); err != nil {
			return nil, fmt.Errorf("authorization: %w", err)
		}
	}
	if order, err = client.WaitOrder(ctx, order.URI); err != nil {
		return nil, fmt.Errorf("order: %w", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: []string{"*." + base}}, key)
	if err != nil {
		return nil, err
	}
	chain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return nil, fmt.Errorf("finalize: %w", err)
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	out := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	for _, der := range chain {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	return out, nil
}

// challengeHint explains the usual cause of a failed DNS check: the challenge
// name is answered by the parent zone (often a wildcard record at the
// registrar) instead of this cloud's Seers.
func challengeHint(base string, err error) string {
	if msg := err.Error(); strings.Contains(msg, "Incorrect TXT record") || strings.Contains(msg, "NXDOMAIN") || strings.Contains(msg, "No TXT record") {
		return fmt.Sprintf(". The ACME server did not reach Seer for _acme-challenge.%s: delegate that name to the Seers with an NS record at the DNS provider", base)
	}
	return ""
}

// The ACME server checks the challenge from several places, each of which may
// reach a different Seer, and the value reaches every Seer's copy of the store
// a moment apart: accepting after a fixed wait failed with "secondary
// validation: No TXT record found". Before accepting, every address of every
// nameserver for the name must answer the value.
var (
	lookupNS      = net.DefaultResolver.LookupNS
	lookupHost    = net.DefaultResolver.LookupHost
	lookupTXTAt   = txtAt
	challengeWait = 2 * time.Minute
)

func txtAt(ctx context.Context, server, name string) ([]string, error) {
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, net.JoinHostPort(server, "53"))
	}}
	return r.LookupTXT(ctx, name)
}

// waitForChallenge returns once every nameserver address answers value for
// name, or after challengeWait. Without a delegation it falls back to
// WildcardSettle, the fixed wait used before.
func waitForChallenge(ctx context.Context, name, value string) {
	nss, _ := lookupNS(ctx, name)
	if len(nss) == 0 {
		// resolvers may not hand out the delegation itself: ask the cloud Seers,
		// seer.<cloud>, the name the delegation points to
		if _, cloud, ok := strings.Cut(strings.TrimPrefix(name, "_acme-challenge."), "."); ok {
			nss = []*net.NS{{Host: "seer." + cloud}}
		}
	}
	var servers []string
	for _, ns := range nss {
		if addrs, err := lookupHost(ctx, ns.Host); err == nil {
			servers = append(servers, addrs...)
		}
	}
	if len(servers) == 0 {
		time.Sleep(WildcardSettle)
		return
	}
	deadline := time.Now().Add(challengeWait)
	for {
		missing := 0
		for _, srv := range servers {
			if vals, err := lookupTXTAt(ctx, srv, name); err != nil || !slices.Contains(vals, value) {
				missing++
			}
		}
		if missing == 0 {
			return
		}
		if time.Now().After(deadline) {
			logger.Warnf("wildcard: %d of %d nameservers still do not answer %s after %s; accepting anyway", missing, len(servers), name, challengeWait)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}
