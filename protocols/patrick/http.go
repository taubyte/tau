package service

func (srv *PatrickService) setupHTTPRoutes() {
	// All github hooks will come through POST
	// see: https://github.com/go-playground/webhooks/blob/v5.17.0/github/github.go#L128
	srv.setupGithubRoutes()
	srv.setupJobRoutes()
}
