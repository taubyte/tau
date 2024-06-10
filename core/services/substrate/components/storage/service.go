package storage

import (
	"context"
	"io"

	"github.com/ipfs/go-cid"
	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/services/substrate/components"
	peer "github.com/taubyte/tau/p2p/peer"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

type Context struct {
	context.Context
	ProjectId     string
	ApplicationId string
	Matcher       string
	Config        *structureSpec.Storage
}

type Meta interface {
	Get() (io.ReadSeekCloser, error)
	Cid() cid.Cid
	Version() int
}

type Service interface {
	components.ServiceComponent
	Storages() map[string]Storage
	Get(context Context) (Storage, error)
	Storage(context Context) (Storage, error)
	Add(r io.Reader) (cid.Cid, error)
	GetFile(context.Context, cid.Cid) (peer.ReadSeekCloser, error)
}

type Storage interface {
	AddFile(ctx context.Context, r io.ReadSeeker, name string, replace bool) (int, error)
	DeleteFile(ctx context.Context, name string, version int) error
	Meta(ctx context.Context, name string, version int) (Meta, error)
	ListVersions(ctx context.Context, name string) ([]string, error)
	GetLatestVersion(ctx context.Context, name string) (int, error)
	List(ctx context.Context, prefix string) ([]string, error)
	Close()
	Used(ctx context.Context) (int, error)
	Capacity() int
	Id() string
	Kvdb() kvdb.KVDB
	ContextConfig() Context
	UpdateCapacity(size uint64)
	Config() *structureSpec.Storage
}
