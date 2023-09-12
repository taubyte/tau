package libdream

import (
	"context"

	commonSpecs "github.com/taubyte/go-specs/common"
)

func init() {
	// Universes
	universes = make(map[string]*Universe)
	multiverseCtx, multiverseCtxC = context.WithCancel(context.Background())
	fixtures = make(map[string]FixtureHandler)

	// Protocols and P2P Client Registry
	Registry = &handlerRegistry{
		registry: make(map[string]*handlers),
	}

	Ports = make(map[string]int)
	lastPort := portStart
	for _, protocol := range commonSpecs.Protocols {
		Registry.registry[protocol] = &handlers{}

		port := lastPort + portBuffer
		Ports["p2p/"+protocol] = port
		lastPort = port
	}

	Ports[DNSPathVar] = lastPort + portBuffer

	for idx, name := range commonSpecs.HTTPProtocols {
		Ports["http/"+name] = httpPortStart + idx*portBuffer
	}
}
