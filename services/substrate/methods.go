package substrate

import (
	"github.com/taubyte/tau/core/vm"
	protocolCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/services/substrate/migration"
)

// TODO: Rename to Satellites
func (srv *Service) Orbitals() []vm.Plugin {
	return srv.orbitals
}

func (srv *Service) Close() error {
	logger.Info("Closing", protocolCommon.Substrate)
	defer logger.Info(protocolCommon.Substrate, "closed")

	for _, orbitals := range srv.orbitals {
		if err := orbitals.Close(); err != nil {
			logger.Errorf("Failed to close orbital `%s`", orbitals.Name())
		}
	}

	// Stop and join background migration work before tearing down the store
	// and clients it uses.
	if srv.migrator != nil {
		srv.migrator.Close()
	}

	srv.tns.Close()
	srv.components.close()

	srv.vm.Close()

	return nil
}

func (srv *Service) Dev() bool {
	return srv.dev
}

// Migrator exposes the node-local data migration for tests and ops: a pass can
// be re-run at any time and is idempotent.
func (srv *Service) Migrator() *migration.Migrator {
	return srv.migrator
}
