package service

func (srv *Service) setupHTTPRoutes() {
	srv.setupLocationHTTPRoutes()
	srv.setupDnsHTTPRoutes()
	srv.setupTNSGatewayHTTPRoutes()
}
