package substrate

import (
	"context"
	"fmt"
	"io"

	compIface "github.com/taubyte/go-interfaces/services/substrate/components"
	con "github.com/taubyte/p2p/streams"
	"github.com/taubyte/p2p/streams/command"
	"github.com/taubyte/p2p/streams/command/response"
	streams "github.com/taubyte/p2p/streams/service"
	httptun "github.com/taubyte/p2p/streams/tunnels/http"
	"github.com/taubyte/tau/clients/p2p/substrate"
	protocolCommon "github.com/taubyte/tau/protocols/common"
	http "github.com/taubyte/tau/protocols/substrate/components/http/common"
	"github.com/taubyte/tau/protocols/substrate/components/http/function"
	"github.com/taubyte/tau/protocols/substrate/components/http/website"
	"github.com/taubyte/utils/maps"
)

func (s *Service) startStream() (err error) {
	if s.stream, err = streams.New(s.node, protocolCommon.Substrate, protocolCommon.SubstrateProtocol); err != nil {
		return fmt.Errorf("new stream failed with: %w", err)
	}

	if err := s.stream.DefineStream(substrate.CommandHTTP, s.proxyHttp, s.tunnelHttp); err != nil {
		return fmt.Errorf("defining command `%s` failed with: %w", substrate.CommandHTTP, err)
	}

	return
}

func (s *Service) tunnelHttp(ctx context.Context, rw io.ReadWriter) {
	w, r, err := httptun.Backend(rw)
	if err != nil {
		fmt.Fprintf(rw, "Status: %d\nerror: %s", 500, err.Error())
		return
	}

	s.components.http.Handler(w, r)
}

func (s *Service) parseHttpRequest(body command.Body) (*http.Request, error) {
	host, err := maps.String(body, substrate.BodyHost)
	if err != nil {
		return nil, err
	}

	path, err := maps.String(body, substrate.BodyPath)
	if err != nil {
		return nil, err
	}

	method, err := maps.String(body, substrate.BodyMethod)
	if err != nil {
		return nil, err
	}

	return &http.Request{
		Host:   host,
		Path:   path,
		Method: method,
	}, nil
}

func (s *Service) proxyHttp(ctx context.Context, con con.Connection, body command.Body) (response.Response, error) {
	request, err := s.parseHttpRequest(body)
	if err != nil {
		return nil, fmt.Errorf("parsing matcher failed with: %w", err)
	}

	response := make(map[string]interface{})

	httpComponent := s.components.http

	var pick compIface.Serviceable
	if serviceables, _ := httpComponent.Cache().Get(
		&http.MatchDefinition{Request: request},
		compIface.GetOptions{Validation: true},
	); len(serviceables) < 1 {
		pick, err = s.components.http.Lookup(&http.MatchDefinition{Request: request})
		if err != nil {
			return nil, fmt.Errorf("lookup failed with: %w", err)
		}
	} else {
		// lookup should always return 0 or 1 serviceable
		pick = serviceables[0]
	}

	// response[substrate.ResponseCpuCount] = s.cpuCount
	// response[substrate.ResponseAverageCpu] = s.cpuAverage

	switch serviceable := pick.(type) {
	case *function.Function:
		response["metrics"], err = serviceable.Metrics()
	case *website.Website:
		response["metrics"], err = serviceable.Metrics()
	default:
		return nil, fmt.Errorf("unknown serviceable type")
	}

	if err != nil {
		return nil, fmt.Errorf("getting serviceable metrics failed with: %w", err)
	}

	return response, nil
}
