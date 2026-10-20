package dns

import (
	"errors"
	"slices"
	"strings"

	"github.com/miekg/dns"
)

// services.go answers the cloud's own services by name: `<service>.<cloud>`,
// e.g. `build-frontend.tau.example.io`, the convention v1 kept as
// `<service>.tau.<network fqdn>`.
//
// # The zone is the cloud's fqdn, not a second one under it
//
// v1's network fqdn was the bare domain and the services hung under `tau.` of
// it. v2's cloud fqdn is already that name — "tau.example.io" is the example
// every config in the tree uses — so a service is one label under it, beside
// the project labels of the generated names. They cannot meet: a project label
// is a fixed-width derived label, and a service name is a word.
//
// # Who answers, and what an unknown name is
//
// The addresses are those of the nodes reporting the service in discovery, the
// same read the generated names make for the gateways. A service the cloud
// knows and nobody is reporting right now is SERVFAIL, for the package's first
// refusal: unserved is not absent.
//
// A LABEL THAT IS NO SERVICE carries on to the generated names when the cloud
// has them, because one label under the cloud is also where a project label
// sits and NXDOMAIN there would cut off every name its project publishes (see
// generated.go). Only a server with no generated names says NXDOMAIN for it.

// Services is the cloud's zone and the services that may be named under it.
type Services struct {
	// Zone is the cloud's fqdn — the gateway's CloudFqdn, the same value the
	// generated names use.
	Zone string

	// Names are the services this cloud knows how to run. A name outside them
	// is not a service, whatever discovery happens to carry.
	Names []string
}

// ErrBadServices is a services zone missing its zone or its names.
var ErrBadServices = errors.New("dns: a services zone needs a zone and the services named under it")

// WithServices answers `<service>.<zone>` with the nodes running it.
func WithServices(s Services) Option {
	return func(o *options) error {
		s.Zone = strings.ToLower(strings.Trim(s.Zone, "."))
		if s.Zone == "" || len(s.Names) == 0 {
			return ErrBadServices
		}
		s.Names = slices.Clone(s.Names)
		o.services = &s
		return nil
	}
}

// answerService answers a service's name, or reports false when the name is
// not one this function speaks for.
func (s *Service) answerService(reply *dns.Msg, name string, q dns.Question) bool {
	svc := s.opts.services
	if svc == nil {
		return false
	}
	label, ok := strings.CutSuffix(name, "."+svc.Zone)
	if !ok || label == "" || strings.Contains(label, ".") {
		return false
	}
	if !slices.Contains(svc.Names, label) {
		if s.opts.generated != nil && s.opts.generated.Zone == svc.Zone {
			return false // possibly a project label: see the file doc
		}
		reply.Rcode = dns.RcodeNameError
		return true
	}

	nodes := s.opts.router.Route(label, s.cfg.MaxAge)
	if len(nodes) == 0 {
		reply.Rcode = dns.RcodeServerFailure
		return true
	}
	switch q.Qtype {
	case dns.TypeA:
		reply.Answer = s.records(nodes, q.Name, false)
	case dns.TypeAAAA:
		reply.Answer = s.records(nodes, q.Name, true)
	}
	return true
}
