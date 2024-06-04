package substrate

import (
	"github.com/taubyte/tau/core/vm"
	protocolCommon "github.com/taubyte/tau/services/common"
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

	srv.tns.Close()
	components := srv.components

	components.http.Close()
	components.pubsub.Close()
	components.ipfs.Close()
	components.database.Close()
	components.storage.Close()
	components.p2p.Close()
	components.counters.Close()
	components.smartops.Close()

	srv.vm.Close()

	return nil
}

func (srv *Service) Dev() bool {
	return srv.dev
}
