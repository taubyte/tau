package pass3

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type libraries struct{}

func Libraries() transform.Transformer[object.Refrence] {
	return &libraries{}
}

func (a *libraries) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	os, err := o.Child("libraries").Object()
	if err != nil {
		if err == object.ErrNotExist {
			return o, nil
		}
		return nil, fmt.Errorf("fetching libraries failed with %w", err)
	}

	for _, ch := range os.Children() {
		sel := os.Child(ch)

		// Reverse attribute moves
		sel.Move("repository-name", "github-fullname")
		sel.Move("repository-id", "github-id")
		sel.Move("provider", "git-provider")

		// Swap ID/name back
		name, err := sel.GetString("name")
		if err != nil {
			return nil, fmt.Errorf("fetching name for library %s failed with %w", ch, err)
		}

		sel.Set("id", ch)
		sel.Delete("name")
		sel.Rename(name)
	}

	return o, nil
}
