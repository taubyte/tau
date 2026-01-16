package pass3

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
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

		// Swap ID/name back
		name, err := sel.GetString("name")
		if err != nil {
			return nil, fmt.Errorf("fetching name for service %s failed with %w", ch, err)
		}

		sel.Set("id", ch)
		sel.Delete("name")
		sel.Rename(name)
	}

	return o, nil
}
