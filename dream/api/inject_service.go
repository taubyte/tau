package api

import (
	"fmt"

	httpIface "github.com/taubyte/http"
	commonIface "github.com/taubyte/tau/core/common"
)

func (srv *multiverseService) injectServiceHttp() {
	// Path to create services in a universe
	srv.rest.POST(&httpIface.RouteDefinition{
		Path: "/service/{universe}/{name}",
		Vars: httpIface.Variables{
			Required: []string{"universe", "name", "config"},
		},
		Handler: srv.apiHandlerService,
	})
}

func (srv *multiverseService) apiHandlerService(ctx httpIface.Context) (interface{}, error) {
	// Grab the universe
	universe, err := srv.getUniverse(ctx)
	if err != nil {
		return nil, fmt.Errorf("killing service failed with: %s", err.Error())
	}

	name, err := ctx.GetStringVariable("name")
	if err != nil {
		return nil, fmt.Errorf("failed getting name error %w", err)
	}

	config := struct {
		Config *commonIface.ServiceConfig
	}{}

	err = ctx.ParseBody(&config)
	if err != nil {
		return nil, err
	}

	err = universe.Service(name, config.Config)
	if err != nil {
		return nil, fmt.Errorf("failed creating service `%s` failed with: %v", name, err)
	}

	return nil, nil
}
