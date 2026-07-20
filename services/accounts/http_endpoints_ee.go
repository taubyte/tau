//go:build ee

package accounts

import (
	eeapi "github.com/taubyte/tau/ee/services/accounts/api"
	httpsvc "github.com/taubyte/tau/pkg/http"
)

// setupHTTPRoutesEE hands the service to the ee api package, which owns the
// management route path and handler.
func (srv *AccountsService) setupHTTPRoutesEE(hosts []string) {
	eeapi.AttachHTTP(srv, hosts)
}

// ManagementRoute registers a POST route behind the management (session) gate —
// the generic hook the ee api package uses to attach its handler.
func (srv *AccountsService) ManagementRoute(hosts []string, path string, h eeapi.Handler) {
	srv.http.POST(&httpsvc.RouteDefinition{
		Hosts:   hosts,
		Path:    path,
		Handler: srv.httpManagementHandler(h),
	})
}
