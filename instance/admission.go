package instance

import (
	"fmt"

	"github.com/taubyte/tau/services/worker/admit"
)

// The decision is services/worker/admit's; the root reaches the component tier, the worker and
// the database factory, names the database, and registers what it opened to close.

// admissionDatabase is the one database's name, for cacheDatabase's reason.
const admissionDatabase = "instances"

// admissions is this node's one admitter, shared by the wasm tier and the gateway so
// one config block has one security policy. The tier and the worker are passed as
// functions because neither exists yet when this is built; the worker's nil check is
// made here, where it is still a *worker.Service.
func (n *Instance) admissions() (*admit.Admissions, error) {
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

// admissionStores opens that database for services/worker/admit, on the first admission
// that needs it.
type admissionStores struct{ node *Instance }

var _ admit.Stores = admissionStores{}

func (s admissionStores) Store() (admit.Rows, error) {
	db, err := s.node.dbs.at(admissionDatabase)
	if err != nil {
		return nil, fmt.Errorf("node: opening the instances database: %w", err)
	}
	// THROUGH addCloser AND NOT AN APPEND, because this runs on the request
	// path: two concurrent first-admissions would otherwise append to one slice
	// at once.
	s.node.addCloser(closerFunc(db.Close))
	return cacheStore{db}, nil
}
