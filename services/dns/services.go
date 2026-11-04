package dns

import (
	"errors"
	"slices"
	"strings"

	"github.com/miekg/dns"
)

// The cloud's own services by name: `<service>.<cloud>`, e.g.
// `build.tau.example.io`. A service is one label under the cloud fqdn,
// beside the project labels, which are fixed-width derived labels and cannot collide.
//
// The addresses are the nodes reporting the service in discovery. A known service
// nobody reports is SERVFAIL: unserved is not absent. A label that is no service
// carries on to the generated names, since NXDOMAIN there would cut off a project's
// names; only a server with no generated names says NXDOMAIN.

// Services is the cloud's zone and the services that may be named under it.
type Services struct {
	// Zone is the cloud's fqdn: the node's cloudfqdn, the same value the generated
	// names use.
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
