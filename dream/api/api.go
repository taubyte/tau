package api

import (
	"context"
	"fmt"
	"strconv"
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
//
// BigBang is the test/mock entry point (the tau CLI calls New directly and
// keeps the fixed 1421 default). It lets the kernel pick the port and then
// publishes it as dream.DreamApiPort: process-local consumers (taucorder, the
// dream http client) build their default URLs from DreamApiPort, so setting it
// here keeps them all in agreement while making concurrent test binaries
// collision-free.
//
// THE PORT IS PUBLISHED AFTER THE BIND, NOT BEFORE IT. DreamApiPort is set to
// 0 only long enough for New to ask for a kernel-assigned port; the real one
// is read back off the listener, so no consumer can be handed a number that
// was merely reserved and might already belong to someone else.
func BigBang(m *dream.Multiverse) error {
	dream.DreamApiPort = 0

	srv, err := New(m, nil)
	if err != nil {
		return err
	}

	srv.server.Start()

	listen, err := srv.server.GetListenAddress()
	if err != nil {
		return err
	}

	// Still 0 means Start never got a listener — it reports that synchronously
	// before it spawns anything, so Error() is the reason and reading it here
	// is reading a settled value.
	port, err := strconv.Atoi(listen.Port())
	if err != nil || port == 0 {
		return fmt.Errorf("dream api did not bind: %w", srv.server.Error())
	}
	dream.DreamApiPort = port

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
