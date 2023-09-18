package p2p

import (
	"github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/tau/protocols/common"
	"github.com/taubyte/tau/vm/cache"
)

func New(srv substrate.Service) (*Service, error) {
	s := &Service{
		Service: srv,
		cache:   cache.New(),
	}

	var err error
	if s.stream, err = s.StartStream(common.SubstrateP2P, common.SubstrateP2PProtocol, s.Handle); err != nil {
		return nil, err
	}
	return s, nil
}
