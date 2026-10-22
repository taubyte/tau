package drive

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/taubyte/tau/pkg/mycelium"
	"github.com/taubyte/tau/tools/spore-drive/config"
	myceliumUtils "github.com/taubyte/tau/tools/spore-drive/mycelium"
)

// DefaultRoot is where tau lives on a host unless WithRoot says otherwise.
const DefaultRoot = "/tb"

func New(cnf config.Parser, options ...Option) (Spore, error) {
	n, err := myceliumUtils.Map(cnf)
	if err != nil {
		return nil, fmt.Errorf("failed to create a sporedrive with %w", err)
	}

	s := &sporedrive{
		parser:      cnf,
		network:     n,
		hostWrapper: newRemote,
		root:        DefaultRoot,
		now:         time.Now,
	}

	for _, opt := range options {
		if err := opt(s); err != nil {
			return nil, err
		}
	}

	if s.tauBinary != nil {
		hasher := md5.New()
		hasher.Write(s.tauBinary)
		s.tauBinaryHash = hex.EncodeToString(hasher.Sum(nil))
	}

	return s, nil
}

func (s *sporedrive) Network() *mycelium.Network {
	return s.network
}
