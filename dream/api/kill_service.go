package api

import (
	"fmt"

	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *Service) killServiceHttp() {
	srv.server.DELETE(&httpIface.RouteDefinition{
		Path: "/service/{universe}/{name}",
		Vars: httpIface.Variables{
			Required: []string{"universe", "name"},
		},
		Handler: srv.killService,
	})
}

func (srv *Service) killService(ctx httpIface.Context) (interface{}, error) {
	universe, err := srv.getUniverse(ctx)
	if err != nil {
		return nil, fmt.Errorf("killing service failed with: %s", err.Error())
	}

	_name, err := ctx.GetStringVariable("name")
	if err != nil {
		return nil, fmt.Errorf("failed getting services error %w", err)
	}

	err = universe.Kill(_name)
	if err != nil {
		return nil, fmt.Errorf("failed killing %s with error: %w", _name, err)
	}

	return nil, nil
}
