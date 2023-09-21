package substrate

import (
	"github.com/taubyte/go-interfaces/vm"
	protocolCommon "github.com/taubyte/tau/protocols/common"
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

	srv.nodeHttp.Close()
	srv.nodePubSub.Close()
	srv.nodeIpfs.Close()
	srv.nodeDatabase.Close()
	srv.nodeStorage.Close()
	srv.nodeP2P.Close()
	srv.nodeCounters.Close()
	srv.nodeSmartOps.Close()

	srv.vm.Close()

	return nil
}

func (srv *Service) Dev() bool {
	return srv.dev
}
