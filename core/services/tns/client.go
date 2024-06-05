package tns

import (
	"github.com/taubyte/tau/core/kvdb"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

type Query struct {
	Prefix []string
	RegEx  bool
}

type Object interface {
	Path() Path
	Bind(interface{}) error
	Interface() interface{}
	// Expected to use with links index
	Current(branch string) ([]Path, error)
}

type Path interface {
	String() string
	Slice() []string
}

type Client interface {
	Fetch(path Path) (Object, error)
	Lookup(query Query) (interface{}, error)
	Push(path []string, data interface{}) error
	List(depth int) ([]string, error)
	Close()

	Simple() SimpleIface
	Database() StructureIface[*structureSpec.Database]
	Domain() StructureIface[*structureSpec.Domain]
	Function() StructureIface[*structureSpec.Function]
	Library() StructureIface[*structureSpec.Library]
	Messaging() StructureIface[*structureSpec.Messaging]
	Service() StructureIface[*structureSpec.Service]
	SmartOp() StructureIface[*structureSpec.SmartOp]
	Storage() StructureIface[*structureSpec.Storage]
	Website() StructureIface[*structureSpec.Website]

	Stats() Stats
}

type Stats interface {
	Database() (kvdb.Stats, error)
}

type SimpleIface interface {
	Commit(projectId, branch string) (string, error)
	Project(projectID, branch string) (interface{}, error)
	GetRepositoryProjectId(gitProvider, repoId string) (projectId string, err error)
}

type StructureGetter[T structureSpec.Structure] interface {
	Commit(projectId, branch string) (string, error)
	List() (map[string]T, error)
	GetById(resourceId string) (T, error)
	GetByIdCommit(projectId, branch string) (resource T, err error)
	GetByName(resourceName string) (resource T, err error)
}

type StructureIface[T structureSpec.Structure] interface {
	Relative(projectId, appId, branch string) StructureGetter[T]
	All(projectId, appId, branch string) StructureGetter[T]
	Global(projectId, branch string) StructureGetter[T]
}
