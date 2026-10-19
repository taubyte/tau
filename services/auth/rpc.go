package auth

import (
	"net/http"

	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// mount is the one handler rpc.Listen serves.
func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(authv2connect.NewIdentityServiceHandler(&identityHandler{s}))
	return mux
}
