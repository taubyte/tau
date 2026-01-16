package pass1

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type websites struct{}

func Websites() transform.Transformer[object.Refrence] {
	return &websites{}
}

func (a *websites) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	os, err := o.Child("websites").Object()
	if err != nil {
		if err == object.ErrNotExist {
			return o, nil
		}
		return nil, fmt.Errorf("fetching websites failed with %w", err)
	}

	for _, fn := range os.Children() {
		sel := os.Child(fn)

		sel.Move("github-fullname", "repository-name")
		sel.Move("github-id", "repository-id")
		sel.Move("git-provider", "provider")

		id, err := sel.Get("id")
		if err != nil {
			return nil, fmt.Errorf("fetching id failed with %w", err)
		}

		idStr, ok := id.(string)
		if !ok {
			return nil, fmt.Errorf("id is not a string")
		}

		sel.Set("name", fn)

		sel.Delete("id")

		sel.Rename(idStr)

		err = utils.IndexById(ct, "websites", fn, idStr)
		if err != nil {
			return nil, fmt.Errorf("indexing websites %s failed with %w", fn, err)
		}
	}

	return o, nil
}
