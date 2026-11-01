package node

import (
	"github.com/taubyte/tau/node/model"
)

// The inference backing is node/model's; the root passes its two collaborators as
// functions, since the wasm tier and placement are composed after it.

// inference is this node's model backing.
func (n *Node) inference() (*model.Inferences, error) {
	return model.New(n.secretResolver, n.inferenceWorkers)
}

// inferenceWorkers is this node's placement router, or nil.
//
// THE NIL CHECK IS WHY THIS IS A METHOD. n.workers is a concrete pointer, and a
// nil one put in an interface is not nil — so returning it unchecked would make
// every guard past this point pass and the first local model's dial panic.
func (n *Node) inferenceWorkers() model.Workers {
	if n.workers == nil {
		return nil
	}
	return n.workers
}
