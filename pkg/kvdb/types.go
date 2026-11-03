package kvdb

import (
	"context"

	"github.com/ipfs/go-cid"

	"github.com/taubyte/tau/p2p/peer"
)

type kvDatabase struct {
	closeCtx  context.Context
	closeCtxC context.CancelFunc
	peer.Node
	broadcaster *(PubSubBroadcaster)
	datastore   *(Datastore)
	closed      bool
	path        string
	onClose     func()
}

type stats struct {
	heads []cid.Cid
}
