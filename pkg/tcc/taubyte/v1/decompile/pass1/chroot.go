package pass1

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type chroot struct{}

func Chroot() transform.Transformer[object.Refrence] {
	return &chroot{}
}

func (a *chroot) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	// pass3 wraps the root in "object", so we unwrap it if it exists
	// Applications don't have this wrapper, so check first
	if o.Child("object").Exists() {
		obj, err := o.Child("object").Object()
		if err != nil {
			return nil, fmt.Errorf("unwrapping object failed with %w", err)
		}
		return obj, nil
	}
	// No "object" wrapper, return as-is (e.g., for applications)
	return o, nil
}
