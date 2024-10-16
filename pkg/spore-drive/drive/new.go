package drive

import (
	"fmt"

	"github.com/taubyte/tau/pkg/mycelium"
	"github.com/taubyte/tau/pkg/spore-drive/config"
	myceliumUtils "github.com/taubyte/tau/pkg/spore-drive/mycelium"
)

func New(cnf config.Parser, options ...Option) (Spore, error) {
	n, err := myceliumUtils.Map(cnf)
	if err != nil {
		return nil, fmt.Errorf("failed to create a sporedrive with %w", err)
	}

	s := &sporedrive{
		parser:      cnf,
		network:     n,
		hostWrapper: newRemote,
	}

	for _, opt := range options {
		if err := opt(s); err != nil {
			return nil, err
		}
	}

	return s, nil
}

func (s *sporedrive) Network() *mycelium.Network {
	return s.network
}
