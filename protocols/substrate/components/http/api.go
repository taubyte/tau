package http

import (
	"fmt"
	"time"

	goHttp "net/http"

	iface "github.com/taubyte/go-interfaces/services/substrate/components/http"
	http "github.com/taubyte/http"
	"github.com/taubyte/tau/protocols/substrate/components/http/common"
	"github.com/taubyte/tau/vm/counter"
	"github.com/taubyte/tau/vm/helpers"
	"github.com/taubyte/tau/vm/lookup"
)

func (s *Service) Lookup(matcher *common.MatchDefinition) (iface.Serviceable, error) {
	servs, err := lookup.Lookup(s, matcher)
	if err != nil {
		return nil, fmt.Errorf("http serviceable lookup failed with: %w", err)
	}

	if len(servs) != 1 {
		return nil, fmt.Errorf("lookup returned %d serviceables, expected 1", len(servs))
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
		w.Write([]byte(err.Error()))
		w.WriteHeader(500)
	}
}

func (s *Service) attach() error {
	s.Http().LowLevel(&http.LowLevelDefinition{
		PathPrefix: "/",
		Handler:    s.Handler,
	})

	return nil
}
