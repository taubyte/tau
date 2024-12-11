package service

import (
	"context"
	"net/http"

	pbconnect "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1/taucorderv1connect"
)

func (s *Service) Attach(mux *http.ServeMux) {
	for path, handler := range s.handlers {
		mux.Handle(path, handler)
	}
}

func (s *Service) addHandler(path string, handler http.Handler) {
	s.handlers[path] = handler
}

func Serve(ctx context.Context, resolver ConfigResolver) (*Service, error) {
	s := &Service{
		ctx:      ctx,
		handlers: make(map[string]http.Handler),
		nodes:    make(map[string]*instance),
		resolver: resolver,
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	s.addHandler(pbconnect.NewNodeServiceHandler(&nodeService{Service: s}))
	s.addHandler(pbconnect.NewSwarmServiceHandler(&swarmService{Service: s}))
	s.addHandler(pbconnect.NewAuthServiceHandler(&authService{Service: s}))
	s.addHandler(pbconnect.NewProjectsInAuthServiceHandler(&projectsService{Service: s}))
	s.addHandler(pbconnect.NewRepositoriesInAuthServiceHandler(&reposService{Service: s}))
	s.addHandler(pbconnect.NewGitHooksInAuthServiceHandler(&hooksService{Service: s}))
	s.addHandler(pbconnect.NewX509InAuthServiceHandler(&x509Service{Service: s}))
	s.addHandler(pbconnect.NewSeerServiceHandler(&seerService{Service: s}))
	s.addHandler(pbconnect.NewHoarderServiceHandler(&hoarderService{Service: s}))
	s.addHandler(pbconnect.NewTNSServiceHandler(&tnsService{Service: s}))
	s.addHandler(pbconnect.NewPatrickServiceHandler(&patrickService{Service: s}))
	s.addHandler(pbconnect.NewMonkeyServiceHandler(&monkeyService{Service: s}))
	s.addHandler(pbconnect.NewHealthServiceHandler(&healthService{Service: s}))

	return s, nil
}
