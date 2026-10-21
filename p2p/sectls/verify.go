// Package sectls is libp2p's TLS security transport with admission: every peer
// carries a certificate from the cloud's CA inside its libp2p TLS certificate,
// and the handshake fails, in both directions, unless that certificate chains
// to the configured root, is within its validity, names this cloud and the
// node purpose, and certifies the very key libp2p authenticated for the
// connection. It negotiates as stock /tls/1.0.0.
package sectls

import (
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"slices"
	"time"

	ic "github.com/libp2p/go-libp2p/core/crypto"
)

var oidAdmission = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 58888, 2, 1}

var (
	ErrNoCertificate = errors.New("sectls: peer presented no node certificate")
	ErrChainInvalid  = errors.New("sectls: node certificate does not chain to the CA root")
	ErrExpired       = errors.New("sectls: node certificate expired or not yet valid")
	ErrPeerMismatch  = errors.New("sectls: node certificate does not certify the connecting peer key")
	ErrWrongCloud    = errors.New("sectls: node certificate is for another cloud")
	ErrWrongPurpose  = errors.New("sectls: certificate is not a node certificate")
)

// Credentials are a node's admission: the root it trusts, its own certificate,
// and the cloud both must name.
type Credentials struct {
	Root  *x509.Certificate
	Cert  []byte
	Cloud string
	Now   func() time.Time
}

// NewCredentials parses PEM and checks the node's own certificate against its
// own key, so a node that would be refused by every peer refuses to start.
func NewCredentials(rootPEM, certPEM []byte, cloud string, key ic.PrivKey) (*Credentials, error) {
	root, err := ParseCertificate(rootPEM)
	if err != nil {
		return nil, fmt.Errorf("ca: %w", err)
	}
	cert, err := ParseCertificate(certPEM)
	if err != nil {
		return nil, fmt.Errorf("certificate: %w", err)
	}
	c := &Credentials{Root: root, Cert: cert.Raw, Cloud: cloud}
	if err := c.Verify(cert.Raw, key.GetPublic()); err != nil {
		return nil, fmt.Errorf("this node's own certificate: %w", err)
	}
	return c, nil
}

// Verify admits a peer's certificate for the key its connection authenticated.
func (c *Credentials) Verify(der []byte, key ic.PubKey) error {
	if len(der) == 0 {
		return ErrNoCertificate
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return fmt.Errorf("sectls: parsing node certificate: %w", err)
	}
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return ErrExpired
	}
	roots := x509.NewCertPool()
	roots.AddCert(c.Root)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: now,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return fmt.Errorf("%w: %v", ErrChainInvalid, err)
	}
	std, err := ic.PubKeyToStdKey(key)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPeerMismatch, err)
	}
	if !publicEqual(leaf.PublicKey, std) {
		return ErrPeerMismatch
	}
	if !slices.Equal(leaf.Subject.Organization, []string{c.Cloud}) {
		return ErrWrongCloud
	}
	if !slices.Contains(leaf.Subject.OrganizationalUnit, Purpose) {
		return ErrWrongPurpose
	}
	return nil
}

func extract(leaf *x509.Certificate) []byte {
	for _, ext := range leaf.Extensions {
		if ext.Id.Equal(oidAdmission) {
			return ext.Value
		}
	}
	return nil
}
