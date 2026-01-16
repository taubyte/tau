package pass1

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
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

		// TODO: need to read the files
		sel.Move("certificate-data", "cert-file")
		sel.Move("certificate-key", "key-file")
		sel.Move("certificate-type", "cert-type")

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

		err = utils.IndexById(ct, "domains", ch, idStr)
		if err != nil {
			return nil, fmt.Errorf("indexing domain %s failed with %w", ch, err)
		}

		if err != nil {
			return nil, fmt.Errorf("moving certificate-data failed with %w", err)
		}
	}

	return o, nil
}
