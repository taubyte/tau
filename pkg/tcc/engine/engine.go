package engine

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

func New(schema Schema, options ...yaseer.Option) (Engine, error) {
	return NewFor(schema, "", nil, options...)
}

// NewFor is New for one ENVIRONMENT: every attribute the vocabulary marks reads its
// `<key>_<env>` spelling where the document writes one, and its declared key where it
// does not. An empty env is New, so a vocabulary that never names one — v1 — resolves
// exactly as it always did, which is why this needed no opt-in.
func NewFor(schema Schema, env string, known []string, options ...yaseer.Option) (Engine, error) {
	sr, err := yaseer.New(options...)
	if err != nil {
		return nil, fmt.Errorf("parser failed to created seer with %w", err)
	}

	return &instance{
		schema: schema.(*schemaDef),
		seer:   sr,
		env:    env,
		envs:   known,
	}, nil
}

func (s *instance) Parse() (object.Object[SeerRef], error) {
	return load[SeerRef](s.schema.root, s.seer.Query(), walk{strict: s.schema.root.Strict, env: s.env, envs: s.envs})
}

func (s *instance) Process() (object.Object[object.Refrence], error) {
	return load[object.Refrence](s.schema.root, s.seer.Query(), walk{strict: s.schema.root.Strict, env: s.env, envs: s.envs})
}

func (s *instance) Schema() Schema {
	return s.schema
}
