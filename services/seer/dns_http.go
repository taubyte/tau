package seer

import (
	http "github.com/taubyte/tau/pkg/http"
	servicesCommon "github.com/taubyte/tau/services/common"
)

func (srv *Service) setupDnsHTTPRoutes() {
	hosts := srv.config.RouteHosts(servicesCommon.Seer)
	srv.http.GET(&http.RouteDefinition{
		Hosts:   hosts,
		Path:    "/network/config",
		Scope:   []string{"network/config"},
		Handler: srv.getGeneratedDomain,
	})
}

func (srv *Service) getGeneratedDomain(ctx http.Context) (interface{}, error) {
	return srv.config.GeneratedDomain(), nil
}
