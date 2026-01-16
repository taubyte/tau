package pass1

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

		sel.Move("replicas-max", "max")
		sel.Move("replicas-min", "min")

		accessType, err := sel.Get("network-access")
		if err == nil {
			if accessType == "host" {
				sel.Set("local", true)
			} else {
				sel.Set("local", false)
			}

			if accessType == "all" || accessType == "host" { // TODO: compat. delete later
				sel.Delete("network-access")
			}
		}

		if err := utils.ParseSize(sel, "size"); err != nil {
			return nil, err
		}

		idStr, err := utils.RenameById(sel, ch)
		if err != nil {
			return nil, err
		}

		err = utils.IndexById(ct, "databases", ch, idStr)
		if err != nil {
			return nil, fmt.Errorf("indexing database %s failed with %w", ch, err)
		}
	}

	return o, nil
}
