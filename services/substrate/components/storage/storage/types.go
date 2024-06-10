package storage

import (
	"context"

	"github.com/ipfs/go-cid"
	"github.com/taubyte/tau/core/kvdb"
	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
	"github.com/taubyte/tau/p2p/peer"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

var _ storageIface.Storage = &Store{}

type Store struct {
	kvdb.KVDB
	srv     storageIface.Service
	id      string
	context storageIface.Context

	instanceCtx  context.Context
	instanceCtxC context.CancelFunc
}

type Meta struct {
	node    peer.Node
	cid     cid.Cid
	version int
}

func (s *Store) Kvdb() kvdb.KVDB {
	return s.KVDB
}

func (s *Store) Config() *structureSpec.Storage {
	return s.context.Config
}

func (s *Store) ContextConfig() storageIface.Context {
	return s.context
}
