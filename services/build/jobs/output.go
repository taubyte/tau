package jobs

import "sync"

// output.go is what a run left behind, as build files it. A run's log and artifacts
// are its runner's to keep (services/runner): the runner puts each artifact in
// the storage service as it is built and its log once build seals it, and only the
// cids come back, to reach a record from the settling section.

// assetRef is which artifact a cid is, kept as a pair so the settling section
// composes the key from two fields rather than parsing a string.
type assetRef struct {
	environment string
	resource    string
}

// produced is what one run left behind: its log's cid and one cid per resource,
// with its own attempt, since the record re-read after the build may carry
// another.
type produced struct {
	attempt uint32
	logCID  string
	assets  map[assetRef]string
	// children is the environment jobs a plan enqueued.
	children []string
}

// notice is a ring a waiter can wait on without a timer: wait hands out the channel
// the next ring closes.
type notice struct {
	mu sync.Mutex
	ch chan struct{}
}

func (n *notice) wait() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.ch == nil {
		n.ch = make(chan struct{})
	}
	return n.ch
}

func (n *notice) ring() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.ch != nil {
		close(n.ch)
		n.ch = nil
	}
}
