package http

import (
	"fmt"

	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/services/substrate/runtime/cache"

	nodeIface "github.com/taubyte/tau/core/services/substrate"
)

func New(srv nodeIface.Service, config *config.Node, options ...Option) (*Service, error) {
	s := &Service{
		Service: srv,
		config:  config,
		cache:   cache.New(),
	}

	var err error
	defer func() {
		if err != nil {
			s.Close()
		}
	}()

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
