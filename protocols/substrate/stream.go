package substrate

import (
	"context"
	"fmt"
	"io"

	compIface "github.com/taubyte/go-interfaces/services/substrate/components"
	httpComp "github.com/taubyte/go-interfaces/services/substrate/components/http"
	con "github.com/taubyte/p2p/streams"
	"github.com/taubyte/p2p/streams/command"
	"github.com/taubyte/p2p/streams/command/response"
	streams "github.com/taubyte/p2p/streams/service"
	httptun "github.com/taubyte/p2p/streams/tunnels/http"
	"github.com/taubyte/tau/clients/p2p/substrate"
	protocolCommon "github.com/taubyte/tau/protocols/common"
	http "github.com/taubyte/tau/protocols/substrate/components/http/common"
	"github.com/taubyte/tau/protocols/substrate/components/http/function"
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

	// mem, err := usage.GetMemoryUsage()
	// if err != nil {
	// 	return nil, fmt.Errorf("getting memory usage failed with: %w", err)
	// }

	response := make(map[string]interface{})
	// var (
	// 	cached    float64 // 0-1
	// 	coldStart int64   // nanoseconds
	// 	runTime   int64   // nanosecond
	// 	maxMemory int64   //retrieved from cached serviceable or config
	// )

	httpComponent := s.components.http
	serviceables, _ := httpComponent.Cache().Get(
		&http.MatchDefinition{Request: request},
		compIface.GetOptions{Validation: true},
	)

	var pick httpComp.Serviceable

	if len(serviceables) == 0 {
		pick, err = s.components.http.Lookup(&http.MatchDefinition{Request: request})
		if err != nil {
			return nil, fmt.Errorf("lookup failed with: %w", err)
		}
	} else {
		pick = serviceables[0]
	}

	// case http.NoMatch: // serviceable not cached
	// 	coldStart = -1
	// 	runTime = -1
	// 	match, err := s.components.http.Lookup(&http.MatchDefinition{Request: request})
	// 	if err != nil {
	// 		return nil, fmt.Errorf("lookup failed with: %w", err)
	// 	}

	// 	switch serviceable := match.(type) {
	// 	case *function.Function:
	// 		maxMemory = int64(serviceable.Config().Memory)
	// 	case httpComp.Website:
	// 	default:
	// 		return nil, fmt.Errorf("unknown serviceable type")
	// 	}

	// 	assetCid, _ := cid.Decode(match.AssetId())
	// 	if exists, _ := s.node.DAG().HasBlock(s.ctx, assetCid); exists {
	// 		cached += 0.3
	// 	}

	// 	// TODO: look up dht

	// case http.ValidMatch: // serviceable is cached
	// 	cached = 1
	// 	switch serviceable := serviceables[0].(type) {
	// 	case *function.Function:
	// 		struct{cs,ct,mem} := serviceable.Metrics()
	// 		// // ShadowCount()
	// 		// if serviceable.Shadows().Count() < 1 {
	// 		// 	coldStart = serviceable.ColdStart().Nanoseconds()
	// 		// }
	// 		// cached = 1
	// 		// maxMemory = serviceable.MemoryMax()
	// 		// runTime = serviceable.CallTime().Nanoseconds()
	// 	case *website.Website:
	// 		// TODO
	// 	}

	// default: // internal error
	// 	return nil, fmt.Errorf("invalid # of matches: %d", len(serviceables))
	// }

	// response[substrate.ResponseCached] = cached
	// response[substrate.ResponseAverageRun] = runTime
	// response[substrate.ResponseColdStart] = coldStart
	// response[substrate.ResponseMemory] = float64(mem.Free) / float64(maxMemory)
	// response[substrate.ResponseCpuCount] = s.cpuCount
	// response[substrate.ResponseAverageCpu] = s.cpuAverage

	switch serviceable := pick.(type) {
	case *function.Function:
		response["metrics"] = serviceables.Metrics()
	case httpComp.Website:
	default:
		return nil, fmt.Errorf("unknown serviceable type")
	}

	response[substrate.ResponseMemory] = float64(mem.Free) / float64(maxMemory)

	return response, nil
}
