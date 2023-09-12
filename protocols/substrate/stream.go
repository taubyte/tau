package substrate

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/p2p/streams"
	"github.com/taubyte/p2p/streams/command"
	cr "github.com/taubyte/p2p/streams/command/response"
	"github.com/taubyte/tau/protocols/substrate/components/http/common"
	"github.com/taubyte/tau/vm/helpers"
)

func (s *Service) setupStreamRoutes() error {
	if err := s.stream.Router().AddStatic("has", s.hasHandler); err != nil {
		return fmt.Errorf("setting up `has` route failed with: %w", err)
	}

	if err := s.stream.Router().AddStatic("handle", s.handleHandler); err != nil {
		return fmt.Errorf("setting up `handle` route failed with: %w", err)
	}

	return nil
}

func (s *Service) hasHandler(ctx context.Context, con streams.Connection, body command.Body) (cr.Response, error) {
	hostIface, ok := body["host"]
	if !ok {
		return nil, errors.New("no host set")
	}
	host, ok := hostIface.(string)
	if !ok {
		return nil, errors.New("host is not a string")
	}

	pathIface, ok := body["path"]
	if !ok {
		return nil, errors.New("no path set")
	}
	path, ok := pathIface.(string)
	if !ok {
		return nil, errors.New("path is not a string")
	}

	methodIface, ok := body["method"]
	if !ok {
		return nil, errors.New("no method set")
	}
	method, ok := methodIface.(string)
	if !ok {
		return nil, errors.New("method is not a string")
	}

	response := make(map[string]interface{}, 1)
	response["cached"] = false
	matcher := common.New(helpers.ExtractHost(host), path, method)
	servs, err := s.nodeHttp.Cache().Get(matcher, components.GetOptions{Validation: true})
	if err == nil && len(servs) == 1 {
		response["cached"] = true
	}

	return response, nil
}

func (s *Service) handleHandler(ctx context.Context, con streams.Connection, body command.Body) (cr.Response, error) {
	peer := con.LocalPeer()
	response := make(map[string]interface{}, 1)
	response["peer"] = peer.String()
	return response, nil
}
