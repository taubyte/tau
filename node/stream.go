package node

import (
	"github.com/taubyte/tau/node/stream"
)

// The stream and bus adapters are node/stream's; the root decides which gossip and
// cloud, and that a bus has one backing shared by the wasm tier and the gateway.

// streamBacking is a backing for one kind over this node's gossip.
//
// A NIL GOSSIP IS PASSED THROUGH rather than refused here. The tier is handed a
// publisher on every node, so a workload that reaches a stream on a node with no
// pubsub is refused by name when it sends — not at start, for a resource it may
// never touch.
func (n *Node) streamBacking(kind string) (*stream.Streams, error) {
	s, err := stream.New(n.opts.pubsub, kind, n.cfg.Cloud)
	if err != nil {
		return nil, err
	}
	n.addCloser(s)
	return s, nil
}

// busStreams is this node's bus backing, built on first use.
//
// It is a method rather than a line in openWorker because both the wasm tier and
// the gateway want it and a node may run either without the other.
func (n *Node) busStreams() (*stream.Streams, error) {
	if n.bus == nil {
		backing, err := n.streamBacking(stream.Bus)
		if err != nil {
			return nil, err
		}
		n.bus = backing
	}
	return n.bus, nil
}

// BusTopic is what a bus's handle goes on the wire as. Whatever comes to attach
// a browser has to listen on exactly what a publish reaches — the same argument
// StreamTopic carries.
func (n *Node) BusTopic(handle string) string { return stream.Topic(stream.Bus, n.cfg.Cloud, handle) }

// StreamTopic is what a stream's handle goes on the wire as.
func (n *Node) StreamTopic(handle string) string {
	return stream.Topic(stream.Stream, n.cfg.Cloud, handle)
}
