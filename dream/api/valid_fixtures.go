package api

import (
	"github.com/taubyte/tau/dream"
	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *multiverseService) validFixtures() {
	srv.rest.GET(&httpIface.RouteDefinition{
		Path: "/spec/fixtures",
		Handler: func(ctx httpIface.Context) (interface{}, error) {
			return dream.ValidFixtures(), nil
		},
	})
}
