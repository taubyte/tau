package api

import (
	"fmt"

	"github.com/taubyte/tau/dream"
	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *Service) injectUniverseHttp() {
	srv.server.POST(&httpIface.RouteDefinition{
		Path: "/universe/{universe}",
		Vars: httpIface.Variables{
			Required: []string{"universe", "config"},
		},
		Handler: srv.apiHandlerUniverse,
	})
}

func (srv *Service) apiHandlerUniverse(ctx httpIface.Context) (interface{}, error) {
	name, err := ctx.GetStringVariable("universe")
	if err != nil {
		return nil, fmt.Errorf("failed getting name with: %w", err)
	}

	_, err = srv.Universe(name)
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

	u, err := srv.New(dream.UniverseConfig{
		Name: name,
	})
	if err != nil {
		return err, nil
	}

	return nil, u.StartWithConfig(config.Config)
}
