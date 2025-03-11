package seer

import (
	http "github.com/taubyte/tau/pkg/http"
)

func (srv *Service) setupDnsHTTPRoutes() {
	var host string
	if !srv.devMode && len(srv.hostUrl) > 0 {
		host = "seer.tau." + srv.hostUrl
	}

	srv.http.GET(&http.RouteDefinition{
		Host:    host,
		Path:    "/network/config",
		Scope:   []string{"network/config"},
		Handler: srv.getGeneratedDomain,
	})
}

func (srv *Service) getGeneratedDomain(ctx http.Context) (interface{}, error) {
	return srv.config.GeneratedDomain, nil
}
