package databases

import (
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/basic"
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/tcc/internal/parity/specs/structure"
	seer "github.com/taubyte/tau/pkg/tcc/internal/parity/yaseer"
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
	Size() string
	Encryption() (key string, keyType string)
}
