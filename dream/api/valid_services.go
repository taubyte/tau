package api

import (
	httpIface "github.com/taubyte/http"
	"github.com/taubyte/tau/pkg/specs/common"
)

func (srv *multiverseService) validServices() {
	srv.rest.GET(&httpIface.RouteDefinition{
		Path: "/spec/services",
		Handler: func(ctx httpIface.Context) (interface{}, error) {
			return common.Services, nil
		},
	})
}
