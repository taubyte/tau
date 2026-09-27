package node

import (
	"fmt"

	"github.com/taubyte/tau/node/admit"
)

// admission.go opens the one place every admission on this node is recorded.
//
// The adapter that decides is node/admit. What is left here is the part only the
// root can do: reach the component tier and the worker this node composed,
// reach the database factory an operator gave it, name the database, and register
// what it opened with the closers — none of which a decision has an opinion
// about.

// admissionDatabase is the one database's name, for cacheDatabase's reason.
const admissionDatabase = "instances"

// admissions is this node's one admitter, shared by the wasm tier and the
// gateway: a workload opening `chat/room-42` and a browser attaching to
// `/room-42` ask one question, and two answers to it would be two security
// policies for one config block.
//
// CONSTRUCTION ONLY, like the other backings.
//
// THE TIER AND THE WORKER ARE HANDED OVER AS FUNCTIONS because neither exists
// when this is built: the component tier takes this very value as a
// collaborator, so it is composed after, and the worker is composed around the
// tier. Both are read at the decision instead — and the worker's check is made
// here, where it is still a *worker.Service, because a nil one handed over as an
// interface is not a nil interface.
func (n *Node) admissions() (*admit.Admissions, error) {
	if n.admit == nil {
		a, err := admit.New(
			func() admit.Tier { return n.wasmTier() },
			func() admit.Functions {
				if n.worker == nil {
					return nil
				}
				return n.worker
			},
			admissionStores{node: n})
		if err != nil {
			return nil, err
		}
		n.admit = a
		n.closers = append(n.closers, a)
	}
	return n.admit, nil
}

// admissionStores opens that database for node/admit, on the first admission
// that needs it.
type admissionStores struct{ node *Node }

var _ admit.Stores = admissionStores{}

func (s admissionStores) Store() (admit.Rows, error) {
	if s.node.opts.databases == nil {
		return nil, ErrNoCacheStore
	}
	db, err := s.node.opts.databases.New(logger, admissionDatabase, rebroadcastSeconds)
	if err != nil {
		return nil, fmt.Errorf("node: opening the instances database: %w", err)
	}
	// THROUGH addCloser AND NOT AN APPEND, because this runs on the request
	// path: two concurrent first-admissions would otherwise append to one slice
	// at once.
	s.node.addCloser(closerFunc(db.Close))
	return cacheStore{db}, nil
}
