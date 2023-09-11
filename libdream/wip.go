package libdream

import (
	"context"
	"sync"

	"github.com/ipfs/go-log/v2"
	commonSpecs "github.com/taubyte/go-specs/common"
)

var (
	logger = log.Logger("dreamland")

	universes      map[string]*Universe
	universesLock  sync.RWMutex
	multiverseCtx  context.Context
	multiverseCtxC context.CancelFunc
)

func init() {
	// Universes
	universes = make(map[string]*Universe)
	multiverseCtx, multiverseCtxC = context.WithCancel(context.Background())

	// Protocols and P2P Client Registry
	Registry = &handlerRegistry{
		registry: make(map[string]*handlers),
	}

	for _, protocol := range commonSpecs.Protocols {
		Registry.registry[protocol] = &handlers{}
	}

	Ports = make(map[string]int)
	lastPort := portStart
	for _, name := range commonSpecs.Protocols {
		port := lastPort + portBuffer
		Ports["p2p/"+name] = port
		lastPort = port
	}

	Ports[DNSPathVar] = lastPort + portBuffer

	for idx, name := range commonSpecs.HTTPProtocols {
		Ports["http/"+name] = httpPortStart + idx*portBuffer
	}
}
