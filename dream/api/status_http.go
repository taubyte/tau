package api

import (
	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *multiverseService) statusHttp() {
	srv.server.GET(&httpIface.RouteDefinition{
		Path: "/status",
		Handler: func(ctx httpIface.Context) (interface{}, error) {
			return srv.Status(), nil
		},
	})
}
