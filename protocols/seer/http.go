package seer

func (srv *Service) setupHTTPRoutes() {
	srv.setupLocationHTTPRoutes()
	srv.setupDnsHTTPRoutes()
	srv.setupTNSGatewayHTTPRoutes()
}
