package p2p

import (
	"bitbucket.org/taubyte/go-node-tvm/cache"
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/odo/protocols/node/components/p2p/common"
)

func New(srv nodeIface.Service, options ...Option) (*Service, error) {
	s := &Service{
		Service: srv,
		cache:   cache.New(),
	}

	var err error
	if s.stream, err = s.StartStream(common.ServiceName, common.Protocol, s.Handle); err != nil {
		return nil, err
	}

	for _, opt := range options {
		if err := opt(s); err != nil {
			return nil, err
		}
	}

	return s, nil
}
