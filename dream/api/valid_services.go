package api

import (
	"github.com/taubyte/tau/legacy/specs/common"
	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *Service) validServices() {
	srv.server.GET(&httpIface.RouteDefinition{
		Path: "/spec/services",
		Handler: func(ctx httpIface.Context) (interface{}, error) {
			return common.Services, nil
		},
	})
}
