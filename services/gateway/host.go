package gateway

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/secrets"
	"github.com/taubyte/tau/services/storage"
	"github.com/taubyte/tau/services/worker/placement"
)

// Certifying is the node's side of the cloud's certificates: the issuer a generated
// name the wildcard does not cover is ordered through, or nil in a cloud that issues
// none.
type Certifying interface {
	Issuer() (Issuer, error)
}

// Terminating says whether whoever serves this gateway's handler serves it over TLS
// too, which decides what a name providing its own certificate gets over plain HTTP.
type Terminating interface {
	Terminates() bool
}

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
// where a build's artifact is, the issuer for generated names and whether its
// handler is served over TLS.
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
	if admits, ok := service.Get[Admits](h); ok {
		opts = append(opts, withAdmission(admits))
	}
	if artifacts, ok := service.Get[Artifacts](h); ok {
		opts = append(opts, withFiles(artifacts, assets))
	}
	if certifying, ok := service.Get[Certifying](h); ok {
		issuer, err := certifying.Issuer()
		if err != nil {
			return fmt.Errorf("gateway: %w", err)
		}
		if issuer != nil {
			opts = append(opts, withIssuer(issuer))
		}
	}
	if terminating, ok := service.Get[Terminating](h); ok && terminating.Terminates() {
		opts = append(opts, withTLS())
	}
	return s.assemble(cfg.Config, opts...)
}
