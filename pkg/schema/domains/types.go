package domains

import (
	"github.com/taubyte/tau/pkg/schema/basic"
	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

type Domain interface {
	Get() Getter
	common.Resource[*structureSpec.Domain]
}

type domain struct {
	*basic.Resource
	seer        *seer.Seer
	name        string
	application string
}

type Getter interface {
	basic.ResourceGetter[*structureSpec.Domain]
	FQDN() string
	UseCertificate() bool
	Type() string
	Cert() string
	Key() string
}
