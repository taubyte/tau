package kvdb

import (
	"context"

	"github.com/ipfs/go-cid"
)

type kvDatabase struct {
	closeCtx    context.Context
	closeCtxC   context.CancelFunc
	factory     *factory
	broadcaster *(PubSubBroadcaster)
	datastore   *(Datastore)
	closed      bool
	path        string
}

type stats struct {
	heads      []cid.Cid
	maxHeight  uint64
	queuedJobs int
}
