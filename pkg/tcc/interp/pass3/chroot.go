package pass3

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
	obj := object.New[object.Refrence]()
	err := obj.Child("object").Add(o)
	if err != nil {
		return nil, fmt.Errorf("adding child object failed with %w", err)
	}

	return obj, nil
}
