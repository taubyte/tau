package pass1

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
	// root is project, we ignore that
	oapps, err := o.Child("applications").Object()
	if err != nil {
		if err == object.ErrNotExist {
			return o, nil
		}
		return nil, fmt.Errorf("fetching applications failed with %w", err)
	}

	for _, app := range oapps.Children() {
		sel := oapps.Child(app)
		id, _ := sel.Get("id") // schema will make sure "id" is present
		idStr, ok := id.(string)
		if !ok {
			return nil, fmt.Errorf("id is not a string")
		}
		sel.Set("name", app)
		sel.Delete("id")
		err = sel.Rename(idStr)
		if err != nil {
			return nil, fmt.Errorf("renaming application failed with %w", err)
		}
	}

	return o, nil
}
