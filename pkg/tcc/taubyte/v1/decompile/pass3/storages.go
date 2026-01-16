package pass3

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type storages struct{}

func Storages() transform.Transformer[object.Refrence] {
	return &storages{}
}

func (a *storages) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	os, err := o.Child("storages").Object()
	if err != nil {
		if err == object.ErrNotExist {
			return o, nil
		}
		return nil, fmt.Errorf("fetching storages failed with %w", err)
	}

	for _, ch := range os.Children() {
		sel := os.Child(ch)

		// Reverse public->network-access
		public, err := sel.GetBool("public")
		if err == nil {
			sel.Delete("public")
			if public {
				sel.Set("network-access", "all")
			} else {
				sel.Set("network-access", "subnet")
			}
		}

		// Reverse size/ttl parsing
		if err := utils.FormatSize(sel, "size"); err != nil {
			return nil, err
		}

		if err := utils.FormatTimeout(sel, "ttl"); err != nil {
			return nil, err
		}

		// Swap ID/name back
		_, err = utils.RenameByName(sel)
		if err != nil {
			return nil, fmt.Errorf("renaming storage %s failed with %w", ch, err)
		}
	}

	return o, nil
}
