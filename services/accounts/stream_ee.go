//go:build ee

package accounts

import eeapi "github.com/taubyte/tau/ee/services/accounts/api"

// setupStreamRoutesEE hands the service to the ee api package, which owns the
// extra verb names and their handlers. Called from setupStreamRoutes after the
// base verbs are wired.
func (srv *AccountsService) setupStreamRoutesEE() {
	eeapi.AttachStream(srv)
}

// DefineVerb registers a stream verb — the generic hook the ee api package
// uses to attach its handlers.
func (srv *AccountsService) DefineVerb(name string, h eeapi.Handler) {
	srv.stream.Define(name, h)
}
