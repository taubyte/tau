package p2p

import (
	"context"
	"errors"
	"sync"

	nodeIface "github.com/taubyte/tau/core/services/substrate"
	p2pIface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	"github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/services/substrate/runtime/cache"
)

var _ p2pIface.Service = &Service{}

type Service struct {
	nodeIface.Service
	stream p2pIface.CommandService
	cache  *cache.Cache

	// client is the Service's single shared outgoing-command client, reused by
	// every Stream instead of one-per-Send (each client spawns a background
	// discover goroutine, so per-Send clients leaked goroutines). clientMu
	// guards lazy creation against a concurrent Close: Close() teardown races
	// in-flight Stream() calls because the substrate tears down components
	// before the WASM vm that drives them (see substrate.Service.Close).
	clientMu     sync.Mutex
	client       *client.Client
	clientClosed bool
}

// p2pClient returns the Service's shared outgoing-command client, creating it
// lazily on first use so it comes up on demand rather than racing the node's
// discovery/DHT convergence at boot (eager creation at New() poisons the
// discovery backoff cache before the DHT converges). It errors once the Service
// is closed so a late Stream() doesn't spawn a discover goroutine that would
// outlive teardown.
func (s *Service) p2pClient() (*client.Client, error) {
	s.clientMu.Lock()
	defer s.clientMu.Unlock()

	if s.clientClosed {
		return nil, errors.New("p2p service is closed")
	}

	if s.client == nil {
		c, err := client.New(s.Node(), common.SubstrateP2PProtocol)
		if err != nil {
			return nil, err
		}
		s.client = c
	}

	return s.client, nil
}

func (s *Service) Close() error {
	s.cache.Close()
	s.stream.Close()

	s.clientMu.Lock()
	c := s.client
	s.clientClosed = true
	s.clientMu.Unlock()

	if c != nil {
		c.Close()
	}
	return nil
}

func (s *Service) Context() context.Context {
	return s.Node().Context()
}
