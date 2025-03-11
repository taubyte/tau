package api

import (
	"fmt"

	"github.com/taubyte/tau/dream"
	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *multiverseService) injectUniverseHttp() {
	// Path to create simples in a universe
	srv.rest.POST(&httpIface.RouteDefinition{
		Path: "/universe/{universe}",
		Vars: httpIface.Variables{
			Required: []string{"universe", "config"},
		},
		Handler: srv.apiHandlerUniverse,
	})
}

func (srv *multiverseService) apiHandlerUniverse(ctx httpIface.Context) (interface{}, error) {
	name, err := ctx.GetStringVariable("universe")
	if err != nil {
		return nil, fmt.Errorf("failed getting name with: %w", err)
	}

	// Grab the universe
	_, err = dream.GetUniverse(name)
	if err == nil {
		return nil, fmt.Errorf("universe `%s` already exists", name)
	}

	config := struct {
		Config *dream.Config
	}{}

	err = ctx.ParseBody(&config)
	if err != nil {
		return nil, err
	}

	u := dream.New(dream.UniverseConfig{
		Name: name,
	})

	return nil, u.StartWithConfig(config.Config)
}
