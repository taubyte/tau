package http

import (
	"fmt"
	"time"

	goHttp "net/http"

	iface "github.com/taubyte/tau/core/services/substrate/components/http"
	http "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/services/substrate/components/http/common"
	"github.com/taubyte/tau/services/substrate/runtime/counter"
	"github.com/taubyte/tau/services/substrate/runtime/helpers"
	"github.com/taubyte/tau/services/substrate/runtime/lookup"
)

func (s *Service) Lookup(matcher *common.MatchDefinition) (iface.Serviceable, error) {
	// TODO: Lookup should not be in vm/
	servs, err := lookup.Lookup(s, matcher)
	if err != nil {
		return nil, fmt.Errorf("http serviceable lookup failed with: %w", err)
	}

	if len(servs) != 1 {
		// probably we got old entries in cache. let's purge them and try again
		for _, srv := range servs {
			s.Cache().Remove(srv)
		}

		servs, err = lookup.Lookup(s, matcher)
		if err != nil {
			return nil, fmt.Errorf("http serviceable lookup failed with: %w", err)
		} else if len(servs) != 1 {
			return nil, fmt.Errorf("lookup returned %d serviceables, expected 1", len(servs))
		}
	}

	pick, ok := servs[0].(iface.Serviceable)
	if !ok {
		return nil, fmt.Errorf("matched serviceable is not a http serviceable")
	}

	return pick, nil
}

func (s *Service) handle(w goHttp.ResponseWriter, r *goHttp.Request) error {
	startTime := time.Now()
	matcher := common.New(helpers.ExtractHost(r.Host), r.URL.Path, r.Method)

	pick, err := s.Lookup(matcher)
	if err != nil {
		return fmt.Errorf("looking up serviceable failed with: %w", err)
	}

	if !pick.IsProvisioned() {
		pick, err = pick.Provision()
		if err != nil {
			return fmt.Errorf("provisioning serviceable failed with: %w", err)
		}
	}

	if err := pick.Ready(); err != nil {
		return counter.ErrorWrapper(pick, startTime, time.Time{}, fmt.Errorf("HTTP serviceable is not ready with: %s", err))
	}

	coldStartDoneTime, err := pick.Handle(w, r, matcher)
	return counter.ErrorWrapper(pick, startTime, coldStartDoneTime, err)
}

func (s *Service) Handler(w goHttp.ResponseWriter, r *goHttp.Request) {
	if err := s.handle(w, r); err != nil {
		w.WriteHeader(500)
		w.Write([]byte(err.Error()))
	}
}

func (s *Service) attach() error {
	s.Http().LowLevel(&http.LowLevelDefinition{
		PathPrefix: "/",
		Handler:    s.Handler,
	})

	return nil
}
