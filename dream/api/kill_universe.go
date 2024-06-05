package api

import (
	"fmt"

	httpIface "github.com/taubyte/http"
	"github.com/taubyte/tau/dream"
)

func (srv *multiverseService) killUniverseHttp() {
	// Path to delete simples in a universe
	srv.rest.DELETE(&httpIface.RouteDefinition{
		Path: "/universe/{universe}",
		Vars: httpIface.Variables{
			Required: []string{"universe"},
		},
		Handler: srv.killUniverse,
	})
}

func (srv *multiverseService) killUniverse(ctx httpIface.Context) (interface{}, error) {
	// Grab the universe
	name, err := ctx.GetStringVariable("universe")
	if err != nil {
		return nil, fmt.Errorf("failed getting name error %w", err)
	}

	u, err := dream.GetUniverse(name)
	if err != nil {
		return nil, fmt.Errorf("universe `%s` does not exist", name)
	}

	u.Stop()

	return nil, nil
}
