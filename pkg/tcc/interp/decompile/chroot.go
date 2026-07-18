package decompile

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/interp/utils"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

// chrootUnwrap is the inverse of the compile-side chroot: it unwraps the "object"
// envelope. Roots without the wrapper (no forward chroot) pass through unchanged.
type chrootUnwrap struct{}

func (a *chrootUnwrap) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	if o.Child("object").Exists() {
		obj, err := o.Child("object").Object()
		if err != nil {
			return nil, fmt.Errorf("unwrapping object failed with %w", err)
		}
		return obj, nil
	}
	return o, nil
}

// unwrapEnvelope reverses chrootEnvelope. The caller gates it on UsesIndexing.
func unwrapEnvelope() transform.Transformer[object.Refrence] {
	return utils.Global("", &chrootUnwrap{})
}
