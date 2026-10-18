package proxy

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/specs"
)

// tls.go answers the handshake when a proxy says it terminates one.
//
// # A proxy has to do this itself, where a gateway does not
//
// services/v2/gateway deliberately terminates nothing: it hands back an
// http.Handler and whatever binds the socket owns the certificate. A proxy has
// no such layer — it IS the socket — so `tls: terminate` has to mean something
// here or mean nothing anywhere.
//
// # Two ways to have a certificate, and neither invents one
//
// `provided` names a secret holding the chain and the key, which is the
// vocabulary's own answer and the one that works for a zone somebody delegated.
// `generated` means the name is under the cloud's own zone, which has a
// wildcard — and that wildcard is the operator's, handed to this node in its
// own config, for the same reason CloudFqdn is.
//
// NOTHING HERE ISSUES A CERTIFICATE. A name in a zone this cloud does not hold
// a certificate for is refused rather than served with a wrong one or with
// plaintext: a proxy that asked for TLS and got an open socket would be a
// security control silently not applied.
//
// # The secret holds both halves, as one artifact
//
// They are issued together, they expire together, and rotating them in two
// places leaves a window where they do not match — which is why the vocabulary
// takes ONE reference. The value is PEM: the CERTIFICATE blocks are the chain,
// in order, and the one key block is the key.

// Secrets resolves a secret a proxy refers to. It is the same seam
// services/worker takes, and a node hands both the same thing.
type Secrets interface {
	Secret(ctx context.Context, project, environment, secretID string) (string, error)
}

// terminates says this proxy answers the TLS handshake itself.
func terminates(r specs.Resource) bool {
	return specs.ReadProxy(r).Tls.Mode == specs.ProxyTlsModeTerminate
}

// certificateFor is the certificate this proxy answers with, or an error
// naming what is missing.
func (s *Service) certificateFor(ctx context.Context, r specs.Resource, b Bind, host string) (*tls.Config, error) {
	if !terminates(r) {
		return nil, nil
	}

	source := specs.ReadProxy(r).Certificate.Source
	if source == "" {
		source = specs.CertificateSourceGenerated
	}

	var (
		cert tls.Certificate
		err  error
	)
	switch source {
	case specs.CertificateSourceProvided:
		cert, err = s.providedCertificate(ctx, r, b)
	case specs.CertificateSourceGenerated:
		cert, err = s.wildcardFor(host)
	default:
		return nil, fmt.Errorf("%w: its certificate source is %q", ErrUnservable, source)
	}
	if err != nil {
		return nil, err
	}

	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
}

// providedCertificate reads the secret the resource names.
func (s *Service) providedCertificate(ctx context.Context, r specs.Resource, b Bind) (tls.Certificate, error) {
	id := specs.ReadProxy(r).Certificate.SecretName
	if id == "" {
		return tls.Certificate{}, fmt.Errorf("%w: it provides a certificate and names no secret", ErrUnservable)
	}
	if s.opts.secrets == nil {
		return tls.Certificate{}, fmt.Errorf("%w: it provides a certificate and this node resolves no secret", ErrUnservable)
	}

	value, err := s.opts.secrets.Secret(ctx, b.Project, b.Environment, id)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("proxy: reading the certificate for %s: %w", b.Name, err)
	}
	return keyPair([]byte(value))
}

// wildcardFor is the cloud's own certificate, when it covers this name.
//
// IT IS THE OPERATOR'S AND NOT THE PROJECT'S, exactly as CloudFqdn is: a
// generated host is `<label>.<project label>.<the cloud's zone>`, and no part
// of the config that named it knows the last piece — let alone holds a key for
// it.
func (s *Service) wildcardFor(host string) (tls.Certificate, error) {
	if s.cfg.Wildcard.Certificate == "" || s.cfg.Wildcard.Key == "" {
		return tls.Certificate{}, fmt.Errorf("%w: it terminates TLS on a generated name and this cloud holds no certificate for its own zone", ErrUnservable)
	}
	if s.cfg.CloudFqdn == "" || !strings.HasSuffix(strings.ToLower(host), "."+strings.ToLower(s.cfg.CloudFqdn)) {
		return tls.Certificate{}, fmt.Errorf("%w: %s is not under %q, so this cloud's own certificate does not cover it", ErrUnservable, host, s.cfg.CloudFqdn)
	}
	return tls.X509KeyPair([]byte(s.cfg.Wildcard.Certificate), []byte(s.cfg.Wildcard.Key))
}

// keyPair splits one PEM blob into the chain and the key.
//
// ONE SECRET HOLDS BOTH, which is what the vocabulary says and what a
// Kubernetes TLS secret does. Splitting on the block type rather than on order
// means a blob written key-first works, which is how half of them are written.
func keyPair(blob []byte) (tls.Certificate, error) {
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
		return tls.Certificate{}, fmt.Errorf("%w: that certificate secret holds no certificate", ErrUnservable)
	case len(key) == 0:
		return tls.Certificate{}, fmt.Errorf("%w: that certificate secret holds no private key", ErrUnservable)
	}
	return tls.X509KeyPair(chain, key)
}
