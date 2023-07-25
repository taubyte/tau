package auth

func (srv *AuthService) setupHTTPRoutes() {
	srv.setupGitHubHTTPRoutes()
	srv.setupDomainsHTTPRoutes()
}
