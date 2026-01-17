package api

import (
	"fmt"

	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *Service) killUniverseHttp() {
	srv.server.DELETE(&httpIface.RouteDefinition{
		Path: "/universe/{universe}",
		Vars: httpIface.Variables{
			Required: []string{"universe"},
		},
		Handler: srv.killUniverse,
	})
}

func (srv *Service) killUniverse(ctx httpIface.Context) (interface{}, error) {
	name, err := ctx.GetStringVariable("universe")
	if err != nil {
		return nil, fmt.Errorf("failed getting name error %w", err)
	}

	u, err := srv.Universe(name)
	if err != nil {
		return nil, fmt.Errorf("universe `%s` does not exist", name)
	}

	u.Stop()

	return nil, nil
}
