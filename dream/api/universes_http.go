package api

import (
	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *multiverseService) universesHttp() {
	srv.rest.GET(&httpIface.RouteDefinition{
		Path: "/universes",
		Handler: func(ctx httpIface.Context) (interface{}, error) {
			return srv.Universes(), nil
		},
	})
}
