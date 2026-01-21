package pass4

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type initIndexes struct{}

// InitIndexes ensures that the "indexes" child always exists in the root object,
// even when there are no resources to index.
func InitIndexes() transform.Transformer[object.Refrence] {
	return &initIndexes{}
}

func (i *initIndexes) Process(ct transform.Context[object.Refrence], config object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	if len(ct.Path()) < 1 {
		return nil, fmt.Errorf("path is too short")
	}

	root, ok := ct.Path()[0].(object.Object[object.Refrence])
	if !ok {
		return nil, fmt.Errorf("root is not an object")
	}

	_, err := root.CreatePath("indexes")
	if err != nil {
		return nil, fmt.Errorf("creating indexes path failed with %w", err)
	}

	return config, nil
}
