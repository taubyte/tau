package api

import (
	"fmt"

	"github.com/taubyte/tau/dream"
	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *multiverseService) getUniverse(ctx httpIface.Context) (*dream.Universe, error) {
	name, err := ctx.GetStringVariable("universe")
	if err != nil {
		return nil, fmt.Errorf("failed getting name with: %w", err)
	}

	return dream.GetUniverse(name)
}
