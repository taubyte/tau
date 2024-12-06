package dream

import (
	"context"

	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
)

func init() {
	// Universes
	universes = make(map[string]*Universe)
	multiverseCtx, multiverseCtxC = context.WithCancel(context.Background())
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
}
