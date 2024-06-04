package functions

import (
	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/schema/basic"
	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

type Function interface {
	Get() Getter
	common.Resource[*structureSpec.Function]
}

type function struct {
	*basic.Resource
	seer        *seer.Seer
	name        string
	application string
}

type Getter interface {
	basic.ResourceGetter[*structureSpec.Function]
	Type() string
	Method() string
	Paths() []string
	Local() bool
	Command() string
	Channel() string
	Source() string
	Domains() []string
	Timeout() string
	Memory() string
	Call() string
	Protocol() string
}
