package website

import (
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/basic"
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/tcc/internal/parity/specs/structure"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

type Website interface {
	Get() Getter
	common.Resource[*structureSpec.Website]
}

type website struct {
	*basic.Resource
	seer        *seer.Seer
	name        string
	application string
}

type Getter interface {
	basic.ResourceGetter[*structureSpec.Website]
	Domains() []string
	Paths() []string
	Branch() string
	Git() (provider, id, fullname string)
}
