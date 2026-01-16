package transform

import "github.com/taubyte/tau/pkg/tcc/object"

func Pipe[T object.DataTypes](c Context[T], o object.Object[T], transformers ...Transformer[T]) (object.Object[T], error) {
	var err error
	for _, t := range transformers {
		o, err = t.Process(c, o)
		if err != nil {
			return nil, err
		}
	}
	return o, err
}
