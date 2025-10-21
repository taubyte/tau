package api

import (
	"fmt"

	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *Service) killNodeIdHttp() {
	srv.server.DELETE(&httpIface.RouteDefinition{
		Path: "/node/{universe}/{name}/{id}",
		Vars: httpIface.Variables{
			Required: []string{"universe", "name", "id"},
		},
		Handler: srv.killNodeById,
	})
}

func (srv *Service) killNodeById(ctx httpIface.Context) (interface{}, error) {
	universe, err := srv.getUniverse(ctx)
	if err != nil {
		return nil, fmt.Errorf("killing service failed with: %s", err.Error())
	}

	_name, err := ctx.GetStringVariable("name")
	if err != nil {
		return nil, fmt.Errorf("failed getting name error %w", err)
	}

	_id, err := ctx.GetStringVariable("id")
	if err != nil {
		return nil, fmt.Errorf("failed getting id error %w", err)
	}

	err = universe.KillNodeByNameID(_name, _id)
	if err != nil {
		return nil, fmt.Errorf("failed killing %s with error: %w", _id, err)
	}

	return nil, nil
}
