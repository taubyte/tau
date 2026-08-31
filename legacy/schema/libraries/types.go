package libraries

import (
	"github.com/taubyte/tau/legacy/schema/basic"
	"github.com/taubyte/tau/legacy/schema/common"
	structureSpec "github.com/taubyte/tau/legacy/specs/structure"
	seer "github.com/taubyte/tau/pkg/yaseer"
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
