package interp

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/interp/utils"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

// chroot wraps the compiled tree under an "object" key so the IndexDriver can
// write an "indexes" sibling beside it.
type chroot struct{}

func (a *chroot) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	obj := object.New[object.Refrence]()
	if err := obj.Child("object").Add(o); err != nil {
		return nil, fmt.Errorf("adding child object failed with %w", err)
	}

	return obj, nil
}

// chrootEnvelope wraps the whole project under "object" (project scope) so the
// index subtree can sit beside it. The caller gates it on UsesIndexing.
func chrootEnvelope() transform.Transformer[object.Refrence] {
	return utils.Global("", &chroot{})
}
