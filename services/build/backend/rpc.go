package backend

import (
	"net/http"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v1/buildv1connect"
)

// mount assembles build.proto's two generated connect handlers behind one mux,
// which rpc.Listen serves over a single libp2p stream listener. connect's
// NewXServiceHandler returns (path string, handler http.Handler); Go binds a
// two-result call straight into ServeMux.Handle's two parameters.
//
// connect.WithReadMaxBytes is REQUIRED, not decoration. p2p/rpc says in its own
// doc that it deliberately does not bound a message — "deciding how much a
// handler will hold in memory once it does read is the service's call, and
// every v2 service is expected to make it" — and this is build's. The messages
// here are three short strings and an id; 64 KiB is already orders of
// magnitude more than any of them needs, and the point is that there is a
// ceiling at all.
//
// ONE Go type could implement both interfaces — Enqueue does not collide with
// Get/List/Cancel/Retry the way auth's five blocks collide with each other —
// but there are two one-line wrappers anyway, so that queue.go and jobs.go stay
// separable and the mount reads the same as auth's.
func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()
	max := connect.WithReadMaxBytes(s.cfg.MaxMessageBytes)
	mux.Handle(buildv1connect.NewQueueServiceHandler(&queueHandler{s}, max))
	mux.Handle(buildv1connect.NewJobServiceHandler(&jobHandler{s}, max))
	return mux
}
