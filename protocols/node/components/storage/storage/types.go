package storage

import (
	"context"

	"github.com/ipfs/go-cid"
	"github.com/taubyte/go-interfaces/kvdb"
	p2p "github.com/taubyte/go-interfaces/p2p/peer"
	storageIface "github.com/taubyte/go-interfaces/services/substrate/storage"
	structureSpec "github.com/taubyte/go-specs/structure"
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
	node    p2p.Node
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
