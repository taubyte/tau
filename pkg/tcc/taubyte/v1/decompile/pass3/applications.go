package pass3

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type applications struct{}

func Applications() transform.Transformer[object.Refrence] {
	return &applications{}
}

func (a *applications) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	oapps, err := o.Child("applications").Object()
	if err != nil {
		if err == object.ErrNotExist {
			return o, nil
		}
		return nil, fmt.Errorf("fetching applications failed with %w", err)
	}

	for _, id := range oapps.Children() {
		sel := oapps.Child(id)
		name, err := sel.GetString("name")
		if err != nil {
			return nil, fmt.Errorf("fetching name for application %s failed with %w", id, err)
		}

		// Set id from current key
		sel.Set("id", id)
		sel.Delete("name")
		sel.Rename(name)
	}

	return o, nil
}
