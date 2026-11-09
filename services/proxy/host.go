package proxy

import (
	"context"
	"errors"
	"fmt"

	logging "github.com/ipfs/go-log/v2"

	"github.com/taubyte/tau/core/service"
	pkgstats "github.com/taubyte/tau/pkg/stats"
	"github.com/taubyte/tau/services/secrets"
	statsService "github.com/taubyte/tau/services/stats"
	"github.com/taubyte/tau/services/worker"
	"github.com/taubyte/tau/services/worker/placement"
)

// Name is what a node's config runs the proxy under.
const Name = "proxy"

var log = logging.Logger("tau.proxy")

// ErrNoWorker is a proxy on a node holding no deployment to serve.
var ErrNoWorker = errors.New("proxy: a proxy serves the deployments a worker holds, and this node runs none")

// holder is the worker beside a proxy: the projects it holds and the proxies in them.
type holder interface {
	Projects
	Proxies(ctx context.Context) []worker.Proxy
}

// hostConfig is what the proxy reads off its node: its own section and the cloud's zone.
type hostConfig struct {
	Config    `yaml:",inline"`
	CloudFqdn string `yaml:"cloudfqdn,omitempty"`
}

// Start serves the proxies the worker beside it holds, sweeping for them until it
// closes: each reaches its workload through placement, and a terminated certificate is
// a secret read through the secrets client.
func (s *Service) Start(ctx context.Context, h service.Host) error {
	var cfg hostConfig
	if err := h.Config(&cfg); err != nil {
		return err
	}
	cfg.Config.CloudFqdn = cfg.CloudFqdn
	held, ok := service.Local[holder](h, worker.Protocol)
	if !ok {
		return ErrNoWorker
	}
	workers, err := placement.On(h)
	if err != nil {
		return fmt.Errorf("proxy: a proxy reaches a workload through placement: %w", err)
	}
	vault := secrets.VaultOn(h)
	s.owned = append(s.owned, workers, vault)
	err = s.assemble(cfg.Config,
		withProjects(held), withWorkers(workers), withDeployments(proxies{held}),
		withSecrets(vault), withWarn(log.Warnf))
	if err != nil {
		return err
	}
	sweep, stop := context.WithCancel(context.WithoutCancel(ctx))
	s.stop, s.done = stop, make(chan struct{})
	go func() {
		defer close(s.done)
		s.Run(sweep)
	}()
	return nil
}

// proxies is what the worker beside a proxy holds, as what it binds.
type proxies struct{ held holder }

func (p proxies) Proxies(ctx context.Context) []Bind {
	held := p.held.Proxies(ctx)
	out := make([]Bind, 0, len(held))
	for _, h := range held {
		out = append(out, Bind{Project: h.Project, Environment: h.Environment, Scope: h.Scope, Name: h.Name})
	}
	return out
}

// Report is every socket this node accepts connections on, for dns's SRV answers, and
// the name each answers to: a client resolves the name before it asks for the port.
func (s *Service) Report(snap *statsService.Snapshot) {
	for _, sock := range s.Serving() {
		snap.Serving = append(snap.Serving, sock.Host)
		snap.Sockets = append(snap.Sockets, pkgstats.Socket{
			Host: sock.Host, Protocol: sock.Protocol, Transport: sock.Transport, Port: sock.Port,
		})
	}
}
