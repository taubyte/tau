package sectls

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"time"

	ic "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Purpose is the organizational unit every node certificate carries, so a
// certificate the same CA issued for something else does not admit a peer.
const Purpose = "tau-node"

const clockSkew = 5 * time.Minute

// CA is a root key and its self-signed certificate. It is what spore-drive and
// dream hold; a node holds only the root certificate.
type CA struct {
	key  crypto.Signer
	Root *x509.Certificate
}

// NewCA mints an Ed25519 root valid for validity.
func NewCA(name string, validity time.Duration) (*CA, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             now.Add(-clockSkew),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, fmt.Errorf("sectls: creating root: %w", err)
	}
	root, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{key: key, Root: root}, nil
}

// LoadCA reads a root certificate and its PKCS#8 key, both PEM.
func LoadCA(certPEM, keyPEM []byte) (*CA, error) {
	root, err := ParseCertificate(certPEM)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("sectls: CA key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("sectls: CA key: %w", err)
	}
	key, ok := parsed.(crypto.Signer)
	if !ok {
		return nil, errors.New("sectls: CA key cannot sign")
	}
	if !publicEqual(root.PublicKey, key.Public()) {
		return nil, errors.New("sectls: CA key does not match its certificate")
	}
	return &CA{key: key, Root: root}, nil
}

// CertificatePEM is the root certificate, PEM.
func (c *CA) CertificatePEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Root.Raw})
}

// KeyPEM is the root key, PKCS#8 PEM.
func (c *CA) KeyPEM() ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(c.key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// Issue certifies a node's libp2p key for cloud, valid for validity. The
// result is PEM.
func (c *CA) Issue(key ic.PubKey, cloud string, validity time.Duration) ([]byte, error) {
	std, err := ic.PubKeyToStdKey(key)
	if err != nil {
		return nil, fmt.Errorf("sectls: node key: %w", err)
	}
	id, err := peer.IDFromPublicKey(key)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject: pkix.Name{
			CommonName:         id.String(),
			Organization:       []string{cloud},
			OrganizationalUnit: []string{Purpose},
		},
		NotBefore:   now.Add(-clockSkew),
		NotAfter:    now.Add(validity),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		URIs:        []*url.URL{{Scheme: "libp2p", Opaque: id.String()}},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.Root, std, c.key)
	if err != nil {
		return nil, fmt.Errorf("sectls: issuing: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// ParseCertificate reads one PEM certificate.
func ParseCertificate(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("sectls: not a PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

func publicEqual(a, b crypto.PublicKey) bool {
	e, ok := a.(interface{ Equal(crypto.PublicKey) bool })
	return ok && e.Equal(b)
}

// serial is a positive 128-bit serial. rand.Read never fails.
func serial() *big.Int {
	b := make([]byte, 16)
	rand.Read(b)
	return new(big.Int).Add(new(big.Int).SetBytes(b), big.NewInt(1))
}
