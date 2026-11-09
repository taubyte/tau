package hosted

import (
	"github.com/taubyte/tau/services/worker/model"
)

// The inference backing is services/worker/model's; the worker passes its two collaborators as
// functions, since the wasm tier and placement are composed after it.

// inference is this node's model backing.
func (n *Service) inference() (*model.Inferences, error) {
	return model.New(func() model.Secrets { return n.vault }, n.inferenceWorkers)
}

// inferenceWorkers is this node's placement router, or nil.
//
// THE NIL CHECK IS WHY THIS IS A METHOD. n.workers is a concrete pointer, and a
// nil one put in an interface is not nil — so returning it unchecked would make
// every guard past this point pass and the first local model's dial panic.
func (n *Service) inferenceWorkers() model.Workers {
	if n.workers == nil {
		return nil
	}
	return n.workers
}
