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

	config := struct {
		Config *dream.Config
		Fresh  bool
	}{}

	err = ctx.ParseBody(&config)
	if err != nil {
		return nil, err
	}

	if u, err := srv.Universe(name); err == nil {
		if !config.Fresh {
			if u.Stopped() {
				return nil, u.StartWithConfig(config.Config)
			}
			if u.Persistent() {
				// a persisted universe Dream already brought back: resume it
				return nil, nil
			}
			return nil, fmt.Errorf("universe `%s` already exists", name)
		}
		u.Stop()
		srv.Delete(name)
	}

	if config.Fresh {
		if err := dream.Reset(srv.Name(), name); err != nil {
			return nil, err
		}
	}

	u, err := srv.New(dream.UniverseConfig{
		Name:     name,
		KeepRoot: true,
	})
	if err != nil {
		return nil, err
	}

	return nil, u.StartWithConfig(config.Config)
}
