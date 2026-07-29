package service

import (
	"net/http"

	p2p "github.com/taubyte/tau/p2p/peer"
)

// NodeResolver returns the peer node behind a node id. It is a plain func type
// so a handler defined outside this package can be handed one without importing
// this package, which would be an import cycle.
//
// Handlers mounted through RegisterHandler get a node rather than a client:
// building the client is theirs to do, so this package never names it.
type NodeResolver = func(nodeID string) (p2p.Node, error)

// extraHandlers are mounted alongside the built-in ones when a Service is
// created. Empty unless something registers into it from an init().
var extraHandlers []func(NodeResolver) (string, http.Handler)

// RegisterHandler adds a connect handler to every Service created afterwards.
// Call it from an init(); Serve reads the list once.
func RegisterHandler(h func(NodeResolver) (string, http.Handler)) {
	extraHandlers = append(extraHandlers, h)
}

// nodeByID is the NodeResolver handed to registered handlers.
func (s *Service) nodeByID(nodeID string) (p2p.Node, error) {
	ni, err := s.getNodeById(nodeID)
	if err != nil {
		return nil, err
	}
	return ni.Node, nil
}
