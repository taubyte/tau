package storages

import (
	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/schema/basic"
	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
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
