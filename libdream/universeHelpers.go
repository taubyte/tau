package libdream

import (
	commonIface "github.com/taubyte/go-interfaces/common"
	commonSpecs "github.com/taubyte/go-specs/common"
)

func (u *Universe) defaultClients() map[string]*commonIface.ClientConfig {
	clients := make(map[string]*commonIface.ClientConfig)
	for _, name := range commonSpecs.P2PStreamProtocols {
		clients[name] = &commonIface.ClientConfig{}
	}

	return clients
}
