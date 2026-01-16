package pass1

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type messaging struct{}

func Messaging() transform.Transformer[object.Refrence] {
	return &messaging{}
}

func (a *messaging) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	os, err := o.Child("messaging").Object()
	if err != nil {
		if err == object.ErrNotExist {
			return o, nil
		}
		return nil, fmt.Errorf("fetching messaging failed with %w", err)
	}

	for _, ch := range os.Children() {
		sel := os.Child(ch)

		sel.Move("websocket", "webSocket") //TODO: camel case not needed - need fixing in specs

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

		err = utils.IndexById(ct, "messaging", ch, idStr)
		if err != nil {
			return nil, fmt.Errorf("indexing messaging %s failed with %w", ch, err)
		}

		if err != nil {
			return nil, fmt.Errorf("moving websocket failed with %w", err)
		}
	}

	return o, nil
}
