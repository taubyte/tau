package substrate

import (
	"context"
	"fmt"
	"io"

	"github.com/taubyte/go-interfaces/services/substrate/components"
	con "github.com/taubyte/p2p/streams"
	"github.com/taubyte/p2p/streams/command"
	"github.com/taubyte/p2p/streams/command/response"
	streams "github.com/taubyte/p2p/streams/service"
	httptun "github.com/taubyte/p2p/streams/tunnels/http"
	"github.com/taubyte/tau/clients/p2p/seer/usage"
	"github.com/taubyte/tau/clients/p2p/substrate"
	protocolCommon "github.com/taubyte/tau/protocols/common"
	http "github.com/taubyte/tau/protocols/substrate/components/http/common"
	"github.com/taubyte/tau/protocols/substrate/components/http/function"
	"github.com/taubyte/tau/protocols/substrate/components/http/website"
	"github.com/taubyte/tau/vm/helpers"
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

	s.nodeHttp.Handler(w, r)
}

func (s *Service) proxyHttp(ctx context.Context, con con.Connection, body command.Body) (response.Response, error) {
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

	mem, err := usage.GetMemoryUsage()
	if err != nil {
		return nil, fmt.Errorf("getting memory usage failed with: %w", err)
	}

	response := map[string]interface{}{substrate.ResponseCached: false}

	matcher := http.New(helpers.ExtractHost(host), path, method)
	// ignoring error, only care if there are serviceables or not
	servs, _ := s.nodeHttp.Cache().Get(matcher, components.GetOptions{Validation: true})
	// cached float

	// not cached
	if len(servs) < 1 {
		// ---> look up with tns and get config
		response["cold-start"] = -1
		response["average-run"] = -1
	} else {
		switch serviceable := servs[0].(type) {
		case *function.Function:
			shadows := serviceable.Shadows()
			// Serviceable.ColdStart()
			maxMemory := shadows.Calls().MemoryMax() // only need this from wazero
			if serviceable.Shadows().Count() > 1 {
				response["cold-start"] = 0
			} else {
				response["cold-start"] = shadows.ColdStart().DurationAverage().Nanoseconds()
				if csMemory := shadows.ColdStart().MemoryMax(); csMemory > maxMemory {
					maxMemory = csMemory
				}
			}

			response["mem"] = float64(mem.Free) / float64(maxMemory)
			response["cpu-usage"] = 0.5 // os cpu usage
			response["average-run"] = shadows.Calls().DurationAverage().Nanoseconds()
		case *website.Website:
			// TODO
		}

	}

	return response, nil
}
