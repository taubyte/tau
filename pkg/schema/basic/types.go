package basic

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

// Getter represents the methods which are common to all resources
type Getter interface {
	Id() string
	Name() string
	Description() string
}

type ResourceGetter[T structureSpec.Structure] interface {
	Getter
	Tags() []string
	SmartOps() []string
	Application() string
	Struct() (T, error)
}

// RootMethod returns a query for the root location of a resource
type RootMethod func() *seer.Query

// ConfigIface is used in the ops for accessing the root of a resource
type ConfigIface interface {
	Config() *seer.Query
}

type Op func(ConfigIface) []*seer.Query

// ErrorWrapper is used to wrap "sync failed with %s" to "on application `name`; sync failed with"
type ErrorWrapper func(format string, i ...any) error

type ResourceIface interface {
	Name() string

	SetName(name string)
	AppName() string
	Directory() string
	Root() *seer.Query
	Config() *seer.Query
	WrapError(format string, i ...any) error
	Delete(attributes ...string) error
}
