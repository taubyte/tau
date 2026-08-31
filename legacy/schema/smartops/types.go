package smartops

import (
	"github.com/taubyte/tau/legacy/schema/basic"
	"github.com/taubyte/tau/legacy/schema/common"
	structureSpec "github.com/taubyte/tau/legacy/specs/structure"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

type SmartOps interface {
	Get() Getter
	common.Resource[*structureSpec.SmartOp]
}

type smartOps struct {
	*basic.Resource
	seer        *seer.Seer
	name        string
	application string
}

type Getter interface {
	basic.ResourceGetter[*structureSpec.SmartOp]
	Source() string
	Timeout() string
	Memory() string
	Call() string
}
