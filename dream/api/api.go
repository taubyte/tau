package api

import (
	"context"
	"time"

	"github.com/taubyte/tau/dream"
	httpIface "github.com/taubyte/tau/pkg/http"
	http "github.com/taubyte/tau/pkg/http/basic"
	"github.com/taubyte/tau/pkg/http/options"

	goHttp "net/http"
)

type Service struct {
	server httpIface.Service
	*dream.Multiverse
}

// Deprecated: Use New instead
func BigBang(m *dream.Multiverse) error {
	srv, err := New(m, nil)
	if err != nil {
		return err
	}

	srv.server.Start()

	if _, err = srv.Ready(10 * time.Second); err != nil {
		return err
	}

	return nil
}

func New(m *dream.Multiverse, httpService httpIface.Service) (*Service, error) {
	if httpService == nil {
		var err error
		httpService, err = http.New(
			m.Context(),
			options.Listen(dream.DreamApiListen()),
			options.AllowedOrigins(true, []string{".*"}),
		)
		if err != nil {
			return nil, err
		}
	}

	srv := &Service{
		Multiverse: m,
		server:     httpService,
	}

	srv.setUpHttpRoutes()

	return srv, nil
}

func (srv *Service) Server() httpIface.Service {
	return srv.server
}

func (srv *Service) Ready(timeout time.Duration) (bool, error) {
	waitCtx, waitCtxC := context.WithTimeout(srv.Context(), timeout)
	defer waitCtxC()

	for {
		select {
		case <-waitCtx.Done():
			return false, waitCtx.Err()
		case <-time.After(100 * time.Millisecond):
			if srv.server.Error() != nil {
				return false, srv.server.Error()
			}
			_, err := goHttp.Get("http://" + dream.DreamApiListen())
			if err == nil {
				return true, nil
			}
		}
	}
}
