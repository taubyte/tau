package p2p

import (
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/odo/protocols/substrate/components/p2p/common"
	"github.com/taubyte/odo/vm/cache"
)

func New(srv nodeIface.Service) (*Service, error) {
	s := &Service{
		Service: srv,
		cache:   cache.New(),
	}

	var err error
	if s.stream, err = s.StartStream(common.ServiceName, common.Protocol, s.Handle); err != nil {
		return nil, err
	}
	return s, nil
}
