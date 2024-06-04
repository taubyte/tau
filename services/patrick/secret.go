package service

import (
	authIface "github.com/taubyte/tau/core/services/auth"
)

func (srv *PatrickService) getHook(hookid string) (authIface.Hook, error) {
	return srv.authClient.Hooks().Get(hookid)
}
