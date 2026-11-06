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

// Handler is served by a composer; Certificate is its tls.Config GetCertificate,
// asked per handshake by server name, because only the gateway can read which
// certificate the gateway answering that name declares. instance/boot binds it.
//
// `certificate.source` defaults to `generated` on every zone-hosted gateway:
//
//	generated   the operator's wildcard for the cloud's zone when it covers the
//	            name (pkg/certificate's Wildcard); otherwise, under the zone,
//	            one issued by ACME when the node has an Issuer. A name in the
//	            project's own zone is issued only once that zone is proven
//	            the project's (services/cm)
//	provided    the secret the gateway names, read through the same resolver
//	            the worker and the proxy take
//
// A generated name the wildcard does not cover fails its handshake only; it is
// still served over plain HTTP. A provided certificate on a node that terminates
// nothing is refused over plain HTTP: serving it in the clear would skip a control
// the author asked for.

// acmeChallengePrefix is where a CA fetches an HTTP-01 answer.
const acmeChallengePrefix = "/.well-known/acme-challenge/"

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

	projectName, environment, err := s.hosts.ResolveHost(ctx, name)
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

	cert, err := s.certificateOf(ctx, proj, declared(candidates), projectName, environment, name)
	if err != nil {
		s.logf("gateway: %s/%s: no certificate answers %s: %v", projectName, environment, name, err)
		return nil, err
	}
	return &cert, nil
}

// declared is the certificate block that answers a name. One name may have several
// gateways, one per path, and one handshake: the first that provides a certificate
// answers, otherwise the wildcard answers for all.
func declared(candidates []candidate) specs.Certificate {
	for _, c := range candidates {
		if c.gateway.Certificate.Source == specs.CertificateSourceProvided {
			return c.gateway.Certificate
		}
	}
	return candidates[0].gateway.Certificate
}

// certificateOf is the certificate one block names, for one name.
func (s *Service) certificateOf(ctx context.Context, proj *specs.Project, declared specs.Certificate, projectName, environment, name string) (tls.Certificate, error) {
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
		// A name in the project's own zone is ordered only once the zone is proven the
		// project's; the issuer checks.
		if p, ok := s.issuer.(ProjectIssuer); ok && !certificate.Under(s.cfg.CloudFqdn, name) {
			if zone := zoneOf(proj, name); zone != "" {
				cert, err := p.ForProject(ctx, projectName, zone, name)
				if err != nil {
					return tls.Certificate{}, fmt.Errorf("%w: %w", ErrNoCertificate, err)
				}
				return cert, nil
			}
		}
		cert, err := certificate.Wildcard(s.cfg.Wildcard, s.cfg.CloudFqdn, name)
		// The operator's wildcard first, when it covers the name. Otherwise only a name
		// under the cloud's zone is issued.
		if errors.Is(err, certificate.ErrNoWildcard) && s.issuer != nil && certificate.Under(s.cfg.CloudFqdn, name) {
			cert, err = s.issuer.Certificate(ctx, name)
		}
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("%w: %w", ErrNoCertificate, err)
		}
		return cert, nil
	}
	return tls.Certificate{}, fmt.Errorf("%w: its certificate source is %q", ErrNoCertificate, declared.Source)
}

// zoneOf is the project's zone name is in, the deepest when zones nest.
func zoneOf(p *specs.Project, name string) string {
	var best string
	for _, z := range p.Zones() {
		fqdn := strings.ToLower(strings.TrimSuffix(z.Fqdn, "."))
		if (name == fqdn || strings.HasSuffix(name, "."+fqdn)) && len(fqdn) > len(best) {
			best = fqdn
		}
	}
	return best
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
