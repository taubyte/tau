package streams

import (
	"context"
	"io"

	"github.com/libp2p/go-libp2p/core/network"
	protocol "github.com/libp2p/go-libp2p/core/protocol"
	"github.com/taubyte/tau/p2p/peer"

	discoveryUtil "github.com/libp2p/go-libp2p/p2p/discovery/util"
)

type Connection interface {
	io.Closer
	network.ConnSecurity
	network.ConnMultiaddrs
}

type Stream network.Stream
type StreamHandler func(Stream)

type StreamManger struct {
	ctx        context.Context
	ctx_cancel context.CancelFunc
	peer       peer.Node
	name       string
	path       string
	handler    StreamHandler
}

func New(peer peer.Node, name string, path string) *StreamManger {
	ctx, ctx_cancel := context.WithCancel(peer.Context())
	s := StreamManger{
		ctx:        ctx,
		ctx_cancel: ctx_cancel,
		peer:       peer,
		name:       name,
		path:       path,
	}

	discoveryUtil.Advertise(s.ctx, peer.Discovery(), path)

	return &s
}

func (s *StreamManger) Start(handler StreamHandler) {
	s.handler = handler
	s.peer.Peer().SetStreamHandler(protocol.ID(s.path), func(ns network.Stream) {
		s.handler(Stream(ns))
	})
}

func (s *StreamManger) Stop() {
	s.ctx_cancel()
}

func (s *StreamManger) Context() context.Context {
	return s.ctx
}
