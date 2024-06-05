package kvdb

import (
	"context"

	"github.com/ipfs/go-cid"
	crdt "github.com/ipfs/go-ds-crdt"
)

type kvDatabase struct {
	closeCtx    context.Context
	closeCtxC   context.CancelFunc
	factory     *factory
	broadcaster *(PubSubBroadcaster)
	datastore   *(crdt.Datastore)
	closed      bool
	path        string
}

type stats struct {
	heads      []cid.Cid
	maxHeight  uint64
	queuedJobs int
}

type statsCbor struct {
	Heads      [][]byte `cbor:"1,keyasint"`
	MaxHeight  uint64   `cbor:"2,keyasint"`
	QueuedJobs int      `cbor:"3,keyasint"`
}
