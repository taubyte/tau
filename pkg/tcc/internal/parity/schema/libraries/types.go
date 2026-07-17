package libraries

import (
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/basic"
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/tcc/internal/parity/specs/structure"
	seer "github.com/taubyte/tau/pkg/tcc/internal/parity/yaseer"
)

type Library interface {
	Get() Getter
	common.Resource[*structureSpec.Library]
}

type library struct {
	*basic.Resource
	seer        *seer.Seer
	name        string
	application string
}

type Getter interface {
	basic.ResourceGetter[*structureSpec.Library]
	Path() string
	Branch() string
	Git() (provider, id, fullname string)
}
