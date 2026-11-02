package backend

import (
	"net/http"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/proto/codec"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v2/buildv2connect"
)

// mount puts build.proto's two connect handlers behind one mux for rpc.Listen.
// WithReadMaxBytes is required: p2p/rpc leaves the message bound to each service.
func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()
	max := connect.WithReadMaxBytes(s.cfg.MaxMessageBytes)
	mux.Handle(buildv2connect.NewQueueServiceHandler(&queueHandler{s}, max, codec.Option()))
	mux.Handle(buildv2connect.NewJobServiceHandler(&jobHandler{s}, max, codec.Option()))
	return mux
}
