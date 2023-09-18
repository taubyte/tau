package http

import (
	"fmt"

	streams "github.com/taubyte/p2p/streams/service"
	"github.com/taubyte/tau/protocols/common"
	"github.com/taubyte/tau/vm/cache"

	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
)

func New(srv nodeIface.Service, options ...Option) (*Service, error) {
	s := &Service{
		Service: srv,
		cache:   cache.New(),
	}

	var err error
	defer func() {
		if err != nil {
			s.Close()
		}
	}()

	if s.stream, err = streams.New(srv.Node(), common.SubstrateHttp, common.SubstrateHttpProtocol); err != nil {
		return nil, fmt.Errorf("starting p2p stream failed with: %w", err)
	}

	for _, opt := range options {
		if err = opt(s); err != nil {
			return nil, fmt.Errorf("options failed with: %w", err)
		}
	}

	if err = s.attach(); err != nil {
		err = fmt.Errorf("attach failed with: %w", err)
	}

	return s, err
}
