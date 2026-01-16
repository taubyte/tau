package pass3

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type databases struct{}

func Databases() transform.Transformer[object.Refrence] {
	return &databases{}
}

func (a *databases) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	os, err := o.Child("databases").Object()
	if err != nil {
		if err == object.ErrNotExist {
			return o, nil
		}
		return nil, fmt.Errorf("fetching databases failed with %w", err)
	}

	for _, ch := range os.Children() {
		sel := os.Child(ch)

		// Reverse attribute moves
		sel.Move("max", "replicas-max")
		sel.Move("min", "replicas-min")

		// Reverse local->network-access
		local, err := sel.GetBool("local")
		if err == nil {
			sel.Delete("local")
			if local {
				sel.Set("network-access", "host")
			} else {
				sel.Set("network-access", "all")
			}
		}

		// Reverse size parsing
		if err := utils.FormatSize(sel, "size"); err != nil {
			return nil, err
		}

		// Swap ID/name back
		_, err = utils.RenameByName(sel)
		if err != nil {
			return nil, fmt.Errorf("renaming database %s failed with %w", ch, err)
		}
	}

	return o, nil
}
