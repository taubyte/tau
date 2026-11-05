package jobs

import (
	"net/http"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/proto/codec"
	"github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2/plmv2connect"
)

// mount puts plm.proto's connect handlers behind one mux for rpc.Listen.
// WithReadMaxBytes is required: p2p/rpc leaves the message bound to each service.
func (s *server) mount() http.Handler {
	mux := http.NewServeMux()
	max := connect.WithReadMaxBytes(s.cfg.MaxMessageBytes)
	mux.Handle(plmv2connect.NewQueueServiceHandler(&queueHandler{s}, max, codec.Option()))
	mux.Handle(plmv2connect.NewJobServiceHandler(&jobHandler{s}, max, codec.Option()))
	return mux
}
