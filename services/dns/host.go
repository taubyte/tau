package dns

import (
	"context"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/services/dns/zone"
	"github.com/taubyte/tau/services/gateway"
	"github.com/taubyte/tau/services/register"
	statsService "github.com/taubyte/tau/services/stats"
	"github.com/taubyte/tau/services/worker"
)

// Name is what a node's config runs dns under.
const Name = "dns"

// Catalog is the names of every service a node of this cloud can run, as the node
// knows them: what `<service>.<zone>` may be asked for.
type Catalog interface {
	ServiceNames() []string
}

// workerBeside is the worker a node runs, as its discovery names it: what it holds and
// its component tier, nil when it runs no components.
type workerBeside interface {
	zone.Projects
	Guests() zone.Guests
}

// Issuing is the node's side of the cloud's certificates: the DNS-01 values it
// holds while a certificate is being proved, or nil in a cloud that issues none.
type Issuing interface {
	Challenges() (Challenges, error)
}

var (
	_ Zones     = (*zone.Zones)(nil)
	_ Resolvers = (*zone.Zones)(nil)
)

// hostConfig is what dns reads off its node: its own section and the cloud's zone.
type hostConfig struct {
	Config    `yaml:",inline"`
	CloudFqdn string `yaml:"cloudfqdn,omitempty"`
}

// Start answers on the address its section names, from its node: the stats view it
// routes from, the register's host index, the worker and its component tier for what
// a project's zones say, the cloud's service names, and the DNS-01 values the node
// holds while it proves a certificate.
func (s *Service) Start(ctx context.Context, h service.Host) error {
	var cfg hostConfig
	if err := h.Config(&cfg); err != nil {
		return err
	}
	router, ok := service.Local[Router](h, statsService.Protocol)
	if !ok {
		return ErrNoRouter
	}
	opts := []option{withRouter(router)}

	views := register.ViewsOn(h)
	s.owned = append(s.owned, views)

	// AUTHORED RECORDS NEED A WORKER, because the records are in the compiled project
	// and the worker is what holds one. A dns node without one answers what it can
	// route and nothing an author wrote. Its component tier may be absent, and a zone
	// naming a resolver it cannot run then answers nothing rather than answering that
	// the name is gone.
	if beside, ok := service.Local[workerBeside](h, worker.Protocol); ok {
		z, err := zone.New(views, beside, beside.Guests())
		if err != nil {
			return err
		}
		opts = append(opts, withZones(z), withResolvers(z))
	}

	// THE CLOUD'S GENERATED NAMES and its services by name, when the node knows the
	// cloud's zone.
	if cfg.CloudFqdn != "" {
		opts = append(opts, withGenerated(Generated{Zone: cfg.CloudFqdn, Hosts: views, Service: gateway.Descriptor.Name}))
		if catalog, ok := service.Get[Catalog](h); ok {
			opts = append(opts, withServices(Services{Zone: cfg.CloudFqdn, Names: catalog.ServiceNames()}))
		}
	}

	if issuing, ok := service.Get[Issuing](h); ok {
		challenges, err := issuing.Challenges()
		if err != nil {
			return err
		}
		if challenges != nil {
			opts = append(opts, withChallenges(cfg.CloudFqdn, challenges))
		}
	}

	if err := s.assemble(cfg.Config, opts...); err != nil {
		return err
	}
	return s.Listen(s.cfg.Listen)
}
