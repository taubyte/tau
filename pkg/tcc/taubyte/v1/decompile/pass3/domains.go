package pass3

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type domains struct{}

func Domains() transform.Transformer[object.Refrence] {
	return &domains{}
}

func (a *domains) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	os, err := o.Child("domains").Object()
	if err != nil {
		if err == object.ErrNotExist {
			return o, nil
		}
		return nil, fmt.Errorf("fetching domains failed with %w", err)
	}

	for _, ch := range os.Children() {
		sel := os.Child(ch)

		// Reverse attribute moves
		sel.Move("cert-file", "certificate-data")
		sel.Move("key-file", "certificate-key")
		sel.Move("cert-type", "certificate-type")

		// Swap ID/name back
		name, err := sel.GetString("name")
		if err != nil {
			return nil, fmt.Errorf("fetching name for domain %s failed with %w", ch, err)
		}

		sel.Set("id", ch)
		sel.Delete("name")
		sel.Rename(name)
	}

	return o, nil
}
