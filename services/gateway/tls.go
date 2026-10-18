package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// tls.go is which certificate answers a handshake for a gateway's name.
//
// # The gateway chooses; the composer binds
//
// This service still binds nothing — Handler is what a composer serves — but
// a composer serving it over TLS has to answer every handshake with the
// certificate the GATEWAY ANSWERING THAT NAME declares, and only the gateway
// can read that off the compiled project. So it is asked, per handshake, by
// the server name the client sent: Certificate is a tls.Config's
// GetCertificate. node/boot binds it.
//
// # Where the certificate comes from
//
// `certificate.source` is defaulted to `generated` on every zone-hosted
// gateway, so it is the common case and must never be a refusal:
//
//	generated   the operator's wildcard for the cloud's own zone, when it
//	            covers the name — pkg/certificate's Wildcard, the same code a
//	            proxy terminates with
//	provided    the secret the gateway names, read through the same resolver
//	            the worker and the proxy take
//
// A generated name the wildcard does not cover — a zone the author holds,
// whose certificate this cloud cannot issue — fails ITS handshake and nothing
// else: the name is still served over plain HTTP, exactly as it was before
// this node terminated anything, because the author asked for nothing a
// default did not say.
//
// # A provided certificate on a node that terminates nothing
//
// is refused by name over plain HTTP. The author wrote a certificate; serving
// the name in the clear from a node that has no way to use it would be the
// security control silently not applied. A node that DOES terminate serves the
// same name over both, as any site does, and the handshake carries the
// certificate.

// Secrets resolves the secret a gateway's provided certificate names. It is
// the same seam services/worker and services/proxy take.
type Secrets interface {
	Secret(ctx context.Context, project, environment, secretID string) (string, error)
}

var (
	// ErrNoServerName is a handshake that named nothing: without SNI there is
	// no gateway to ask, and guessing one would answer with somebody else's.
	ErrNoServerName = errors.New("gateway: the handshake names no server, so no gateway's certificate can answer it")

	// ErrNoCertificate is a gateway whose certificate this node cannot produce.
	ErrNoCertificate = errors.New("gateway: this node holds no certificate for that name")
)

// Certificate answers one handshake: the certificate the gateway serving its
// server name declares. It is a tls.Config's GetCertificate.
func (s *Service) Certificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.ToLower(strings.TrimSuffix(hello.ServerName, "."))
	if name == "" {
		return nil, ErrNoServerName
	}

	ctx := hello.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()

	projectName, environment, err := s.ownerOf(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrNoCertificate, name, err)
	}
	proj, _, err := s.projects.At(ctx, register.Deployment{Project: projectName, Environment: environment})
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrNoCertificate, name, err)
	}

	candidates := s.candidatesAt(proj, name, projectName)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: nothing serves %s in this environment", ErrNoCertificate, name)
	}

	cert, err := s.certificateOf(ctx, declared(candidates), projectName, environment, name)
	if err != nil {
		s.logf("gateway: %s/%s: no certificate answers %s: %v", projectName, environment, name, err)
		return nil, err
	}
	return &cert, nil
}

// declared is the certificate block that answers a name.
//
// ONE NAME, POSSIBLY SEVERAL GATEWAYS — one per path — and one handshake.
// A gateway that PROVIDES a certificate is the author saying which one this
// name presents, so the first that does answers; otherwise every one of them
// is on the default and the wildcard answers for all.
func declared(candidates []candidate) specs.Certificate {
	for _, c := range candidates {
		if c.gateway.Certificate.Source == specs.CertificateSourceProvided {
			return c.gateway.Certificate
		}
	}
	return candidates[0].gateway.Certificate
}

// certificateOf is the certificate one block names, for one name.
func (s *Service) certificateOf(ctx context.Context, declared specs.Certificate, projectName, environment, name string) (tls.Certificate, error) {
	switch declared.Source {
	case specs.CertificateSourceProvided:
		if declared.SecretName == "" {
			return tls.Certificate{}, fmt.Errorf("%w: it provides a certificate and names no secret", ErrNoCertificate)
		}
		if s.secrets == nil {
			return tls.Certificate{}, fmt.Errorf("%w: it provides a certificate and this node resolves no secret", ErrNoCertificate)
		}
		value, err := s.secrets.Secret(ctx, projectName, environment, declared.SecretName)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("%w: reading the secret%s: %w", ErrNoCertificate, scopeOf(declared.SecretNamespace), err)
		}
		cert, err := certificate.KeyPair([]byte(value))
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("%w: %w", ErrNoCertificate, err)
		}
		return cert, nil

	case specs.CertificateSourceGenerated, "":
		cert, err := certificate.Wildcard(s.cfg.Wildcard, s.cfg.CloudFqdn, name)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("%w: %w", ErrNoCertificate, err)
		}
		return cert, nil
	}
	return tls.Certificate{}, fmt.Errorf("%w: its certificate source is %q", ErrNoCertificate, declared.Source)
}

func scopeOf(namespace string) string {
	if namespace == "" {
		return ""
	}
	return " in " + namespace
}

// cleartext is why a request over plain HTTP is refused, or "" when it is not:
// a gateway providing its own certificate, reached on a node that terminates
// nothing.
func (s *Service) cleartext(m match, r *http.Request) string {
	if r.TLS != nil || s.terminates {
		return ""
	}
	if m.gateway.Certificate.Source != specs.CertificateSourceProvided {
		return ""
	}
	return "this name provides its own certificate, and this node serves no TLS to present it with"
}
