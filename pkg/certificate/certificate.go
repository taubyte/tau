// Package certificate is how a v2 service that terminates TLS gets the
// certificate it answers with.
//
// # Two ways to have one, and neither invents one
//
// A resource's `certificate` block says `provided` — a secret holding the
// chain and the key, the vocabulary's own answer and the one that works for a
// zone somebody delegated — or `generated`, which means the name is under the
// cloud's own zone. That zone has a wildcard, and the wildcard is the
// OPERATOR'S, handed to a node in its own config for the same reason the
// cloud's fqdn is: a generated name ends in a zone no project declares, so no
// project can hold a key for it.
//
// NOTHING HERE ISSUES A CERTIFICATE; pkg/certificate/acme does, for a name
// under the cloud's zone, when the operator enables it. A name the wildcard
// does not cover is otherwise an
// error rather than a wrong certificate or plaintext: a resource that asked
// for TLS and got an open socket would be a security control silently not
// applied.
//
// It is shared because two services terminate — a proxy is its own socket,
// and a gateway's handler is served on the node's — and two copies of "does
// this wildcard cover that name" would be two answers to it.
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

	// ErrNoChain and ErrNoKey are a secret holding half a certificate. Said by
	// name, because "that key did not match" three frames later is not a
	// sentence anybody can act on.
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

// Wildcard is the cloud's own certificate for host, when it covers it.
//
// COVERED MEANS UNDER THE ZONE, and the zone is cloudFqdn: a generated host
// is `<label>.<project label>.<the cloud's zone>`, and the operator's
// certificate is issued for that zone.
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

// KeyPair splits one PEM blob into the chain and the key.
//
// ONE SECRET HOLDS BOTH, which is what the vocabulary says and what a
// Kubernetes TLS secret does: they are issued together, they expire together,
// and rotating them in two places leaves a window where they do not match.
// Splitting on the block type rather than on order means a blob written
// key-first works, which is how half of them are written.
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
