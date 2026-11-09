package auth

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/pkg/forge/drivers"
	"github.com/taubyte/tau/services/secrets"
)

// hostConfig is what auth reads off its node: its own keys and the forges the cloud
// serves.
type hostConfig struct {
	Auth   Config          `yaml:"auth,omitempty"`
	Forges []drivers.Forge `yaml:"forges,omitempty"`
}

// Start runs auth on its node: its users in the database the node shares under its
// name, its keys and OAuth secrets in the cloud's vault, the forges the cloud serves,
// and whoever the node says decides admission, when it says.
func (s *Service) Start(ctx context.Context, h service.Host) error {
	var cfg hostConfig
	if err := h.Config(&cfg); err != nil {
		return err
	}
	if cfg.Auth.Issuer == "" {
		cfg.Auth.Issuer = "tau:" + h.Cloud()
	}
	dbs, ok := service.Get[service.Databases](h)
	if !ok {
		return fmt.Errorf("auth: its node shares no databases")
	}
	db, err := dbs.Database(Descriptor.Name)
	if err != nil {
		return err
	}
	registry, err := drivers.Registry(cfg.Forges)
	if err != nil {
		return err
	}
	vault := secrets.VaultOn(h)
	s.vaultClient = vault
	opts := []Option{withUsers(db, vault.Cloud(VaultService), cfg.Auth), withForges(registry)}
	if identity, ok := service.Get[Identity](h); ok {
		opts = append(opts, withIdentity(identity))
	}
	if err := s.assemble(h.Peer().Host(), opts...); err != nil {
		return err
	}
	if err := s.serve(ctx); err != nil {
		return err
	}
	h.Serve(Protocol, s)
	return nil
}
