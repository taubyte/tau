package auth

import (
	"net/http"

	"github.com/taubyte/tau/pkg/v2/proto/gen/auth/v1/authv1connect"
)

// mount assembles the five generated connect handlers behind one mux, which
// rpc.Listen serves over a single libp2p stream listener. connect's
// NewXServiceHandler returns (path string, handler http.Handler); Go binds a
// two-result call straight into ServeMux.Handle's two parameters, which is
// why each line below needs nothing more than the constructor call itself
// (service-conventions.md §2c, verified there to compile).
//
// Unlike service-conventions.md §2c's own worked example, the five handler
// values passed in are NOT s itself — see repositories.go's repositoryHandler
// doc for why one *Service cannot implement more than one of these
// interfaces.
func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(authv1connect.NewProjectServiceHandler(&projectHandler{s}))
	mux.Handle(authv1connect.NewRepositoryServiceHandler(&repositoryHandler{s}))
	mux.Handle(authv1connect.NewHookServiceHandler(&hookHandler{s}))
	mux.Handle(authv1connect.NewDomainServiceHandler(&domainHandler{s}))
	mux.Handle(authv1connect.NewCertificateServiceHandler(&certificateHandler{s}))
	return mux
}
