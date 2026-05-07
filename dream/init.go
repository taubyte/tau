package dream

import (
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
)

func init() {
	// Universes
	fixtures = make(map[string]FixtureHandler)

	// Services and P2P Client Registry
	Registry = &handlerRegistry{
		registry: make(map[string]*handlers),
	}

	Ports = make(map[string]int)
	lastPort := portStart
	for _, service := range commonSpecs.Services {
		Registry.registry[service] = &handlers{}

		port := lastPort
		Ports["http/"+service] = port
		Ports["p2p/"+service] = port + 2
		Ports["ipfs/"+service] = port + 4
		Ports["dns/"+service] = port + 8
		lastPort += portBuffer
	}

	// Dream tests skip the accounts integration so they don't have to
	// stand up the accounts service for auth/monkey paths.
	accountsIface.VerifyOnAuth = false
}
