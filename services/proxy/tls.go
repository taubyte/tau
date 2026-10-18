package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/specs"
)

// tls.go answers the handshake when a proxy says it terminates one.
//
// # A proxy has to do this itself, where a gateway does not
//
// services/gateway binds nothing: it hands back an http.Handler, and the
// node that serves it binds the socket and asks the gateway which certificate
// answers each name (Service.Certificate there). A proxy has no such layer —
// it IS the socket — so `tls: terminate` has to mean something here or mean
// nothing anywhere.
//
// # Where the certificate comes from is pkg/certificate's
//
// `provided` is the secret the resource names and `generated` is the
// operator's wildcard for the cloud's own zone; the package says why neither
// invents one. A gateway's names are terminated by the node with the same
// code, so the two cannot disagree about what a wildcard covers.

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
func (s *Service) wildcardFor(host string) (tls.Certificate, error) {
	cert, err := certificate.Wildcard(s.cfg.Wildcard, s.cfg.CloudFqdn, host)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("%w: it terminates TLS on a generated name and %w", ErrUnservable, err)
	}
	return cert, nil
}

// keyPair is the secret's PEM as a certificate, refused by name when half of
// it is missing.
func keyPair(blob []byte) (tls.Certificate, error) {
	cert, err := certificate.KeyPair(blob)
	if errors.Is(err, certificate.ErrNoChain) || errors.Is(err, certificate.ErrNoKey) {
		return tls.Certificate{}, fmt.Errorf("%w: %w", ErrUnservable, err)
	}
	return cert, err
}
