package api

import (
	"fmt"

	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *multiverseService) killSimpleHttp() {
	// Path to delete simples in a universe
	srv.rest.DELETE(&httpIface.RouteDefinition{
		Path: "/simple/{universe}/{name}",
		Vars: httpIface.Variables{
			Required: []string{"universe", "name"},
		},
		Handler: srv.killSimple,
	})
}

func (srv *multiverseService) killSimple(ctx httpIface.Context) (interface{}, error) {
	// Grab the universe
	universe, err := srv.getUniverse(ctx)
	if err != nil {
		return nil, fmt.Errorf("killing simple failed with: %s", err.Error())
	}

	// Grab simple to kill
	_name, err := ctx.GetStringVariable("name")
	if err != nil {
		return nil, fmt.Errorf("failed getting simple to kill error %w", err)
	}

	// Kill simple
	err = universe.Kill(_name)
	if err != nil {
		return nil, fmt.Errorf("failed killing %s with error: %w", _name, err)
	}

	return nil, nil
}
