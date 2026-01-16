package pass3

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
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

		// Reverse attribute moves
		sel.Move("repository-name", "github-fullname")
		sel.Move("repository-id", "github-id")
		sel.Move("provider", "git-provider")

		// Swap ID/name back
		name, err := sel.GetString("name")
		if err != nil {
			return nil, fmt.Errorf("fetching name for website %s failed with %w", fn, err)
		}

		sel.Set("id", fn)
		sel.Delete("name")
		sel.Rename(name)
	}

	return o, nil
}
