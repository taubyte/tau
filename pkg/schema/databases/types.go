package databases

import (
	"github.com/taubyte/tau/pkg/schema/basic"
	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

type Database interface {
	Get() Getter
	common.Resource[*structureSpec.Database]
}

type database struct {
	*basic.Resource
	seer        *seer.Seer
	name        string
	application string
}

type Getter interface {
	basic.ResourceGetter[*structureSpec.Database]
	Match() string
	Regex() bool
	Local() bool
	Secret() bool
	Min() int
	Max() int
	Size() string
	Encryption() (key string, keyType string)
}
