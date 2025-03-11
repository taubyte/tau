package api

import (
	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *multiverseService) statusHttp() {
	srv.rest.GET(&httpIface.RouteDefinition{
		Path: "/status",
		Handler: func(ctx httpIface.Context) (interface{}, error) {
			return srv.Status(), nil
		},
	})
}
