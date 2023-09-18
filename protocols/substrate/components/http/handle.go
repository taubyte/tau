package http

import (
	"context"
	"fmt"
	"io"
	"time"

	goHttp "net/http"

	"github.com/taubyte/go-interfaces/services/substrate/components"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/http"
	http "github.com/taubyte/http"
	"github.com/taubyte/p2p/streams"
	"github.com/taubyte/p2p/streams/command"
	"github.com/taubyte/p2p/streams/command/response"
	tunnel "github.com/taubyte/p2p/streams/tunnels/http"
	"github.com/taubyte/tau/protocols/substrate/components/http/common"
	"github.com/taubyte/tau/vm/counter"
	"github.com/taubyte/tau/vm/helpers"
	"github.com/taubyte/tau/vm/lookup"
	"github.com/taubyte/utils/maps"
)

func (s *Service) handle(w goHttp.ResponseWriter, r *goHttp.Request) error {
	startTime := time.Now()
	matcher := common.New(helpers.ExtractHost(r.Host), r.URL.Path, r.Method)

	servs, err := lookup.Lookup(s, matcher)
	if err != nil {
		return fmt.Errorf("http serviceable lookup failed with: %s", err)
	}

	if len(servs) != 1 {
		return fmt.Errorf("lookup returned %d serviceables, expected 1", len(servs))
	}

	pick, ok := servs[0].(iface.Serviceable)
	if !ok {
		return fmt.Errorf("matched serviceable is not a http serviceable")
	}

	if err := pick.Ready(); err != nil {
		return counter.ErrorWrapper(pick, startTime, time.Time{}, fmt.Errorf("HTTP serviceable is not ready with: %s", err))
	}

	coldStartDoneTime, err := pick.Handle(w, r, matcher)
	return counter.ErrorWrapper(pick, startTime, coldStartDoneTime, err)
}

func (s *Service) attach() error {
	// attach P2P stream handler
	if err := s.stream.DefineStream("upgrade", s.checkCache, s.tunnel); err != nil {
		return fmt.Errorf("defining p2p command `upgrade` failed with: %w", err)
	}

	// attach HTTP route handler
	s.Http().LowLevel(&http.LowLevelDefinition{
		PathPrefix: "/",
		Handler: func(w goHttp.ResponseWriter, r *goHttp.Request) {
			if err := s.handle(w, r); err != nil {
				s.writeError(w, err)
			}
		},
	})

	return nil
}

func (s *Service) checkCache(ctx context.Context, con streams.Connection, body command.Body) (response.Response, error) {
	host, err := maps.String(body, "host")
	if err != nil {
		return nil, err
	}

	path, err := maps.String(body, "path")
	if err != nil {
		return nil, err
	}

	method, err := maps.String(body, "method")
	if err != nil {
		return nil, err
	}

	response := make(map[string]interface{}, 1)
	response["cached"] = false
	matcher := common.New(helpers.ExtractHost(host), path, method)
	servs, err := s.cache.Get(matcher, components.GetOptions{Validation: true})
	if err == nil && len(servs) == 1 {
		response["cached"] = true
	}

	return response, nil
}

func (s *Service) tunnel(ctx context.Context, rw io.ReadWriter) {
	w, r, err := tunnel.Backend(rw)
	if err != nil {
		fmt.Fprintf(rw, "Status: %d\nerror: %s", 500, err.Error())
		return
	}

	if err := s.handle(w, r); err != nil {
		s.writeError(w, err)
		return
	}
}
