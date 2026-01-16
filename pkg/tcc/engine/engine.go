package engine

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

func New(schema Schema, options ...yaseer.Option) (Engine, error) {
	sr, err := yaseer.New(options...)
	if err != nil {
		return nil, fmt.Errorf("parser failed to created seer with %w", err)
	}

	return &instance{
		schema: schema.(*schemaDef),
		seer:   sr,
	}, nil
}

func (s *instance) Parse() (object.Object[SeerRef], error) {
	return load[SeerRef](s.schema.root, s.seer.Query())
}

func (s *instance) Process() (object.Object[object.Refrence], error) {
	return load[object.Refrence](s.schema.root, s.seer.Query())
}

func (s *instance) Schema() Schema {
	return s.schema
}
