package storage

import (
	"context"

	"github.com/ipfs/go-cid"
	"github.com/taubyte/go-interfaces/kvdb"
	storageIface "github.com/taubyte/go-interfaces/services/substrate/components/storage"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/p2p/peer"
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
