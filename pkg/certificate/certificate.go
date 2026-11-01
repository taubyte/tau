// Package certificate is how a v2 service that terminates TLS gets the certificate
// it answers with.
//
// A resource's `certificate` block says `provided` (a secret holding chain and key)
// or `generated` (a name under the cloud's own zone, whose wildcard the operator hands
// a node in its config, since no project can hold a key for it). Issuance is
// pkg/certificate/acme's. A name the wildcard does not cover is an error, never
// plaintext.
//
// Shared because a proxy and a gateway both terminate.
package certificate

import (
	"crypto/tls"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrNoWildcard is a generated name on a node holding no certificate for
	// the cloud's own zone.
	ErrNoWildcard = errors.New("this cloud holds no certificate for its own zone")

	// ErrNotCovered is a name outside the cloud's own zone, which its
	// wildcard does not cover.
	ErrNotCovered = errors.New("the cloud's own certificate does not cover it")

	// ErrNoChain and ErrNoKey are a secret holding half a certificate, said by name.
	ErrNoChain = errors.New("that certificate secret holds no certificate")
	ErrNoKey   = errors.New("that certificate secret holds no private key")
)

// Pair is a chain and its key, in PEM: what an operator writes for the
// cloud's own zone.
type Pair struct {
	Certificate string `yaml:"certificate"`
	Key         string `yaml:"key"`
}

// Held says the operator gave this node a certificate at all.
func (p Pair) Held() bool { return p.Certificate != "" && p.Key != "" }

// Wildcard is the cloud's own certificate for host, when it covers it: when host is
// under the zone cloudFqdn.
func Wildcard(p Pair, cloudFqdn, host string) (tls.Certificate, error) {
	if !p.Held() {
		return tls.Certificate{}, ErrNoWildcard
	}
	if !Under(cloudFqdn, host) {
		return tls.Certificate{}, fmt.Errorf("%w: %s is not under %q", ErrNotCovered, host, cloudFqdn)
	}
	return tls.X509KeyPair([]byte(p.Certificate), []byte(p.Key))
}

// Under reports whether host is a name inside the zone cloudFqdn, never the
// zone itself.
func Under(cloudFqdn, host string) bool {
	return cloudFqdn != "" && strings.HasSuffix(strings.ToLower(host), "."+strings.ToLower(cloudFqdn))
}

// KeyPair splits one PEM blob into the chain and the key. One secret holds both, as a
// Kubernetes TLS secret does. Splitting by block type, not order, accepts a key-first
// blob.
func KeyPair(blob []byte) (tls.Certificate, error) {
	var chain, key []byte

	rest := blob
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			chain = append(chain, pem.EncodeToMemory(block)...)
			continue
		}
		if strings.HasSuffix(block.Type, "PRIVATE KEY") && key == nil {
			key = pem.EncodeToMemory(block)
		}
	}

	switch {
	case len(chain) == 0:
		return tls.Certificate{}, ErrNoChain
	case len(key) == 0:
		return tls.Certificate{}, ErrNoKey
	}
	return tls.X509KeyPair(chain, key)
}
