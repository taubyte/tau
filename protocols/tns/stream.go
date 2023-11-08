package tns

func (srv *Service) setupStreamRoutes() {
	// TODO: requires secret + maybe a handshare using project PSK
	srv.stream.Define("push", srv.pushHandler)
	srv.stream.Define("fetch", srv.fetchHandler)
	srv.stream.Define("lookup", srv.lookupHandler)
	srv.stream.Define("list", srv.listHandler)
}
