package service

import (
	authIface "github.com/taubyte/go-interfaces/services/auth"
)

func (srv *PatrickService) getHook(hookid string) (authIface.Hook, error) {
	return srv.authClient.Hooks().Get(hookid)
}
