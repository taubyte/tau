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
	"github.com/taubyte/tau/pkg/certificate/acme"
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

// ServeResponder answers Holds on ResponderProtocol from c, reporting view as
// the nodes this one knows to answer each kind.
func ServeResponder(ctx context.Context, h host.Host, c *Challenges, view func(kind string) []peer.ID) (*Responder, error) {
	if h == nil {
		return nil, ErrNoHost
	}
	if c == nil {
		return nil, ErrNoChallenges
	}
	mux := http.NewServeMux()
	mux.Handle(challengesv2connect.NewResponderServiceHandler(responding{c: c, view: view}))
	server, err := rpc.Listen(ctx, h, ResponderProtocol, mux)
	if err != nil {
		return nil, err
	}
	return &Responder{server: server}, nil
}

// Close stops answering.
func (r *Responder) Close() error { return r.server.Close() }

type responding struct {
	c    *Challenges
	view func(kind string) []peer.ID
}

func (r responding) Holds(ctx context.Context, req *connect.Request[challengesv2.HoldsRequest]) (*connect.Response[challengesv2.Held], error) {
	held, err := r.c.Holds(ctx, req.Msg.GetKind(), req.Msg.GetName(), req.Msg.GetValue())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	res := &challengesv2.Held{Held: held}
	if r.view != nil {
		for _, p := range r.view(req.Msg.GetKind()) {
			res.Responders = append(res.Responders, p.String())
		}
	}
	return connect.NewResponse(res), nil
}

// WithResponders is the nodes this one knows to answer each kind — every dns
// node for dns-01, every gateway for http-01 — asked at call time. With it the
// issuer has the CA look only once every one of them holds the answer.
func WithResponders(f func(kind string) []peer.ID) Option {
	return func(s *Service) { s.responders = f }
}

// Confirmed is acme.Confirmer's. Every responder of kind must hold value —
// not only those this node knows, but every one any edge node it asks knows
// of, because a CA reaches the gateways the dns nodes hand out and the dns
// nodes the parent zone delegates to, and this node's view may be behind
// theirs. It asks every known dns node and gateway, widens by what each
// reports, and asks again until nothing new is named.
func (s *Service) Confirmed(ctx context.Context, kind, name, value string) (bool, error) {
	if kind != acme.KindDNS && kind != acme.KindHTTP {
		return false, fmt.Errorf("globals: no challenge kind %q", kind)
	}
	eligible := map[peer.ID]bool{}
	for _, p := range s.responders(kind) {
		eligible[p] = true
	}
	if len(eligible) == 0 {
		return false, fmt.Errorf("globals: no node answers %s", kind)
	}
	pending := map[peer.ID]bool{}
	for _, role := range []string{acme.KindDNS, acme.KindHTTP} {
		for _, p := range s.responders(role) {
			pending[p] = true
		}
	}
	asked := map[peer.ID]bool{}
	held := map[peer.ID]bool{}
	for len(pending) > 0 {
		for p := range pending {
			delete(pending, p)
			asked[p] = true
			h, view, err := s.holds(ctx, p, kind, name, value)
			if err != nil {
				if eligible[p] {
					return false, fmt.Errorf("globals: asking %s: %w", p, err)
				}
				continue
			}
			held[p] = h
			for _, q := range view {
				eligible[q] = true
				if !asked[q] {
					pending[q] = true
				}
			}
		}
	}
	for p := range eligible {
		if !held[p] {
			return false, nil
		}
	}
	return true, nil
}

func (s *Service) holds(ctx context.Context, p peer.ID, kind, name, value string) (bool, []peer.ID, error) {
	if p == s.host.ID() {
		h, err := s.challenges.Holds(ctx, kind, name, value)
		return h, s.responders(kind), err
	}
	conn, err := rpc.NewClient(s.host, p, ResponderProtocol)
	if err != nil {
		return false, nil, err
	}
	defer conn.Close()
	res, err := challengesv2connect.NewResponderServiceClient(conn.HTTP(), conn.URL()).Holds(ctx,
		connect.NewRequest(&challengesv2.HoldsRequest{Kind: kind, Name: name, Value: value}))
	if err != nil {
		return false, nil, err
	}
	var view []peer.ID
	for _, id := range res.Msg.GetResponders() {
		if q, err := peer.Decode(id); err == nil {
			view = append(view, q)
		}
	}
	return res.Msg.GetHeld(), view, nil
}
