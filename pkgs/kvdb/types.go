package kvdb

import (
	"context"

	crdt "github.com/ipfs/go-ds-crdt"
)

type kvDatabase struct {
	closeCtx    context.Context
	closeCtxC   context.CancelFunc
	factory     *factory
	broadcaster *(crdt.PubSubBroadcaster)
	datastore   *(crdt.Datastore)
	closed      bool
	path        string
}
