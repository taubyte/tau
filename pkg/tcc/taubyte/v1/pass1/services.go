package pass1

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type services struct{}

func Services() transform.Transformer[object.Refrence] {
	return &services{}
}

func (a *services) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	os, err := o.Child("services").Object()
	if err != nil {
		if err == object.ErrNotExist {
			return o, nil
		}
		return nil, fmt.Errorf("fetching services failed with %w", err)
	}

	for _, ch := range os.Children() {
		sel := os.Child(ch)

		id, err := sel.Get("id")
		if err != nil {
			return nil, fmt.Errorf("fetching id failed with %w", err)
		}

		idStr, ok := id.(string)
		if !ok {
			return nil, fmt.Errorf("id is not a string")
		}

		sel.Set("name", ch)

		sel.Delete("id")

		sel.Rename(idStr)

		err = utils.IndexById(ct, "services", ch, idStr)
		if err != nil {
			return nil, fmt.Errorf("indexing services %s failed with %w", ch, err)
		}
	}

	return o, nil
}
