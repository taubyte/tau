package node

import (
	"github.com/taubyte/tau/node/model"
)

// model.go wires this node's inference backing.
//
// The adapter is node/model. What is decided here is the part only the root
// knows: WHEN each of its two collaborators exists.
//
// BOTH ARE PASSED AS FUNCTIONS AND NOT AS VALUES, which is not a style choice.
// The wasm tier is composed inside openWorker and placement is opened AFTER it,
// so a backing handed n.workers at construction would be handed nil on every
// node there is. A function is read at the call, by which time the node has
// finished opening.

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
