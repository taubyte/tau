package database

import (
	"context"

	iface "github.com/taubyte/go-interfaces/services/substrate/components/database"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/p2p/peer"
)

var _ iface.Database = &Database{}

type Database struct {
	node      peer.Node
	dbContext iface.Context
	config    *structureSpec.Database
	keystore  iface.KV

	srv iface.Service

	instanceCtx  context.Context
	instanceCtxC context.CancelFunc
}
