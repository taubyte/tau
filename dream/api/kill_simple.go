package api

import (
	"fmt"

	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *Service) killSimpleHttp() {
	srv.server.DELETE(&httpIface.RouteDefinition{
		Path: "/simple/{universe}/{name}",
		Vars: httpIface.Variables{
			Required: []string{"universe", "name"},
		},
		Handler: srv.killSimple,
	})
}

func (srv *Service) killSimple(ctx httpIface.Context) (interface{}, error) {
	universe, err := srv.getUniverse(ctx)
	if err != nil {
		return nil, fmt.Errorf("killing simple failed with: %s", err.Error())
	}

	_name, err := ctx.GetStringVariable("name")
	if err != nil {
		return nil, fmt.Errorf("failed getting simple to kill error %w", err)
	}

	err = universe.Kill(_name)
	if err != nil {
		return nil, fmt.Errorf("failed killing %s with error: %w", _name, err)
	}

	return nil, nil
}
