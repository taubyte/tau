package gateway

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/secrets"
	"github.com/taubyte/tau/services/storage"
	"github.com/taubyte/tau/services/worker"
	"github.com/taubyte/tau/services/worker/placement"
)

// hostConfig is what the gateway reads off its node: its own section and the
// cloud's zone.
type hostConfig struct {
	Config    `yaml:",inline"`
	CloudFqdn string `yaml:"cloudfqdn,omitempty"`
}

// Start builds the gateway from its node: names resolved and projects read through
// the register's client, requests placed on workers from the stats view, provided
// certificates and package files read through the secrets and storage clients, and
// what the node offers beside it: the bus a browser attaches to and who may attach,
// and where a build's artifact is; generated names are ordered through its own cm
// client.
func (s *Service) Start(ctx context.Context, h service.Host) error {
	var cfg hostConfig
	if err := h.Config(&cfg); err != nil {
		return err
	}
	cfg.Config.CloudFqdn = cfg.CloudFqdn
	workers, err := placement.On(h)
	if err != nil {
		return fmt.Errorf("gateway: a gateway places requests from stats: %w", err)
	}
	views := register.ViewsOn(h)
	vault := secrets.VaultOn(h)
	assets := storage.StoreOn(h)
	s.owned = append(s.owned, workers, views, vault, assets)

	opts := []option{
		withHosts(views), withSource(views), withWorkers(workers),
		withSecrets(vault), withClock(h.Now),
	}
	if buses, ok := service.Get[Buses](h); ok {
		opts = append(opts, withBuses(buses))
	}
	// WHAT THE WORKER BESIDE IT ANSWERS: who may attach to one instance of a bus, and
	// where a build's artifact is. A gateway on a node with no worker admits nothing
	// and serves no package's files.
	if admits, ok := service.Local[Admits](h, worker.Protocol); ok {
		opts = append(opts, withAdmission(admits))
	}
	if artifacts, ok := service.Local[Artifacts](h, worker.Protocol); ok {
		opts = append(opts, withFiles(artifacts, assets))
	}
	certs, err := cm.ClientOn(ctx, h)
	if err != nil {
		return fmt.Errorf("gateway: %w", err)
	}
	if certs != nil {
		s.owned = append(s.owned, certs)
		opts = append(opts, withIssuer(certs))
	}
	if cfg.TLS {
		opts = append(opts, withTLS())
	}
	return s.assemble(cfg.Config, opts...)
}
