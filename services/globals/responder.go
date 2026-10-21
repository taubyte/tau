package globals

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	challengesv2 "github.com/taubyte/tau/pkg/proto/gen/tau/challenges/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/challenges/v2/challengesv2connect"
)

// responder.go is how the node placing an order learns that every node a CA
// may reach for the answer holds it: each dns node and gateway serves Holds
// over its own copy of the challenge store, and the issuer asks them all
// before it asks the CA to look.

// ResponderProtocol is the libp2p protocol a responder answers on.
var ResponderProtocol protocol.ID = rpc.Protocol(challengesv2connect.ResponderServiceName)

// Responder serves Holds over one node's copy of the challenge store.
type Responder struct {
	server *rpc.Server
}

// ServeResponder answers Holds on ResponderProtocol from c.
func ServeResponder(ctx context.Context, h host.Host, c *Challenges) (*Responder, error) {
	if h == nil {
		return nil, ErrNoHost
	}
	if c == nil {
		return nil, ErrNoChallenges
	}
	mux := http.NewServeMux()
	mux.Handle(challengesv2connect.NewResponderServiceHandler(responding{c}))
	server, err := rpc.Listen(ctx, h, ResponderProtocol, mux)
	if err != nil {
		return nil, err
	}
	return &Responder{server: server}, nil
}

// Close stops answering.
func (r *Responder) Close() error { return r.server.Close() }

type responding struct{ c *Challenges }

func (r responding) Holds(ctx context.Context, req *connect.Request[challengesv2.HoldsRequest]) (*connect.Response[challengesv2.Held], error) {
	held, err := r.c.Holds(ctx, req.Msg.GetKind(), req.Msg.GetName(), req.Msg.GetValue())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&challengesv2.Held{Held: held}), nil
}

// WithResponders is the nodes a CA may reach for an answer of each kind —
// every dns node for dns-01, every gateway for http-01 — asked at call time.
// With it the issuer has the CA look only once every one of them holds the
// answer.
func WithResponders(f func(kind string) []peer.ID) Option {
	return func(s *Service) { s.responders = f }
}

// Confirmed is acme.Confirmer's: every responder of kind holds value, this
// node's own read from its own copy.
func (s *Service) Confirmed(ctx context.Context, kind, name, value string) (bool, error) {
	peers := s.responders(kind)
	if len(peers) == 0 {
		return false, fmt.Errorf("globals: no node answers %s", kind)
	}
	for _, p := range peers {
		held, err := s.holds(ctx, p, kind, name, value)
		if err != nil {
			return false, fmt.Errorf("globals: asking %s: %w", p, err)
		}
		if !held {
			return false, nil
		}
	}
	return true, nil
}

func (s *Service) holds(ctx context.Context, p peer.ID, kind, name, value string) (bool, error) {
	if p == s.host.ID() {
		return s.challenges.Holds(ctx, kind, name, value)
	}
	conn, err := rpc.NewClient(s.host, p, ResponderProtocol)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	res, err := challengesv2connect.NewResponderServiceClient(conn.HTTP(), conn.URL()).Holds(ctx,
		connect.NewRequest(&challengesv2.HoldsRequest{Kind: kind, Name: name, Value: value}))
	if err != nil {
		return false, err
	}
	return res.Msg.GetHeld(), nil
}
