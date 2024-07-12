package api

import (
	httpIface "github.com/taubyte/http"
	"github.com/taubyte/tau/dream"
)

func (srv *multiverseService) validFixtures() {
	srv.rest.GET(&httpIface.RouteDefinition{
		Path: "/spec/fixtures",
		Handler: func(ctx httpIface.Context) (interface{}, error) {
			return dream.ValidFixtures(), nil
		},
	})
}
