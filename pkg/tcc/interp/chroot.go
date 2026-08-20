package interp

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/interp/utils"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

// chroot wraps the compiled tree under ProjectKey so a compile can write siblings beside
// it: the index subtree, and the record of what it lowered.
type chroot struct{}

func (a *chroot) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	obj := object.New[object.Refrence]()
	if err := obj.Child(ProjectKey).Add(o); err != nil {
		return nil, fmt.Errorf("adding child object failed with %w", err)
	}

	return obj, nil
}

// chrootEnvelope wraps the whole project (project scope) so a sibling can sit beside it.
// The caller gates it on whether anything needs one.
func chrootEnvelope() transform.Transformer[object.Refrence] {
	return utils.Global("", &chroot{})
}
