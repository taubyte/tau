package storages

import (
	"github.com/taubyte/tau/legacy/schema/basic"
	"github.com/taubyte/tau/legacy/schema/common"
	structureSpec "github.com/taubyte/tau/legacy/specs/structure"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

type Storage interface {
	Get() Getter
	common.Resource[*structureSpec.Storage]
}

type storage struct {
	*basic.Resource
	seer        *seer.Seer
	name        string
	application string
}

type Getter interface {
	basic.ResourceGetter[*structureSpec.Storage]
	Match() string
	Regex() bool
	Type() string

	// if bucket type Object
	Public() bool
	Versioning() bool

	// if bucket type Streaming
	TTL() string

	// independent
	Size() string
	SmartOps() []string
}
