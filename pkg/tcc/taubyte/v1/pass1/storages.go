package pass1

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

		accessType, err := sel.Get("network-access")
		if err == nil {
			if accessType == "all" {
				sel.Set("public", true)
			} else {
				sel.Set("public", false)
			}
			sel.Delete("network-access")
		}

		if err := utils.ParseSize(sel, "size"); err != nil {
			return nil, err
		}

		if err := utils.ParseTimeout(sel, "ttl"); err != nil {
			return nil, err
		}

		idStr, err := utils.RenameById(sel, ch)
		if err != nil {
			return nil, err
		}

		err = utils.IndexById(ct, "storages", ch, idStr)
		if err != nil {
			return nil, fmt.Errorf("indexing storages %s failed with %w", ch, err)
		}
	}

	return o, nil

}
