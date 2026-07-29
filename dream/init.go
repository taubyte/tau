package dream

import (
	"time"

	"github.com/taubyte/tau/p2p/peer"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
)

func init() {
	// Universes
	fixtures = make(map[string]FixtureHandler)

	// Universes boot many nodes at once on loopback; the production discovery
	// backoff (60s min) turns one empty-routing-table lookup during boot into
	// a minutes-long outage. Keep retries in the seconds range instead.
	peer.DiscoveryBackoffMin = time.Second
	peer.DiscoveryBackoffMax = 10 * time.Second

	// Services and P2P Client Registry
	Registry = &handlerRegistry{
		registry: make(map[string]*handlers),
	}

	for _, service := range commonSpecs.Services {
		Registry.registry[service] = &handlers{}
	}

}
