package substrate

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/p2p/streams"
	"github.com/taubyte/p2p/streams/command"
	cr "github.com/taubyte/p2p/streams/command/response"
	"github.com/taubyte/p2p/streams/service"
	"github.com/taubyte/tau/protocols/substrate/components/http/common"
	"github.com/taubyte/tau/vm/helpers"
	"github.com/taubyte/utils/maps"
)

func (s *Service) setupStreamRoutes() error {
	if err := s.stream.Define("has", s.hasHandler); err != nil {
		return fmt.Errorf("setting up `has` route failed with: %w", err)
	}
	if err := s.stream.DefineStream("tunnel", service.NoOpCommandHandler, s.tunnel); err != nil {
		return fmt.Errorf("setting up `handle` route failed with: %w", err)
	}

	return nil
}

func (s *Service) hasHandler(ctx context.Context, con streams.Connection, body command.Body) (cr.Response, error) {
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
	servs, err := s.nodeHttp.Cache().Get(matcher, components.GetOptions{Validation: true})
	if err == nil && len(servs) == 1 {
		response["cached"] = true
	}

	return response, nil
}

type responseWriter struct {
	io.ReadWriter
	headers http.Header
}

func NewRW(rw io.ReadWriter) responseWriter {
	return responseWriter{
		ReadWriter: rw,
		headers:    make(http.Header),
	}
}

func (r responseWriter) WriteHeader(statusCode int) {
	fmt.Fprintf(r, "Status: %d\n", statusCode)
}

func (r responseWriter) Header() http.Header {
	return r.headers
}

func (s *Service) tunnel(ctx context.Context, rw io.ReadWriter) {
	r, err := http.ReadRequest(bufio.NewReader(rw))
	// s.nodeHttp.Handle()
	if err != nil {
		fmt.Println("fuck")
	}

	w := NewRW(rw)

	if err := s.nodeHttp.Handle(w, r); err != nil {
		fmt.Println("Fuck2")
	}

	fmt.Println("REQUEST:", r)
	// reqData, err := io.ReadAll(rw)
	// if err != nil {
	// 	fmt.Println("ERR:", err)
	// 	fmt.Println("shit1")
	// 	return
	// }

	// r := new(http.Request)
	// if err = json.Unmarshal(reqData, r); err != nil {
	// 	fmt.Println("shit2")
	// 	return
	// }

	// w := NewRW(rw)
	// if err = s.nodeHttp.Handle(w, r); err != nil {
	// 	fmt.Println("shit3")
	// }
}

// func (s *Service) handleHandler(ctx context.Context, con streams.Connection, body command.Body) (cr.Response, error) {
// 	peer := con.LocalPeer()
// 	response := make(map[string]interface{}, 1)
// 	response["peer"] = peer.String()
// 	return response, nil
// }
