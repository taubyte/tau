package database

import (
	"context"

	crdt "github.com/ipfs/go-ds-crdt"
)

type KVDatabase struct {
	closeCtx  context.Context
	closeCtxC context.CancelFunc

	broadcaster *(crdt.PubSubBroadcaster)
	datastore   *(crdt.Datastore)

	path string
}
