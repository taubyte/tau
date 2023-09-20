package http

import (
	"fmt"

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
