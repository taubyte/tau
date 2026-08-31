package engine

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

func New(schema Schema, options ...yaseer.Option) (Engine, error) {
	return NewFor(schema, nil, options...)
}

// NewFor is New for a project that DECLARES environments. `known` is the closed set,
// which the loader needs whichever environment is being read: one document carries
// every environment's spelling at once, so without the whole set production's read
// rejects `timeout_preview` as an unknown key.
//
// WHICH environment is being read is not here — it is an argument to ProcessFor,
// because one branch feeds several deployments and they share this engine and its
// parsed documents. Binding it to the instance would mean re-reading the tree once per
// environment, which is the cost that shows up first when this is compiled to wasm and
// asked to answer in under 20ms.
func NewFor(schema Schema, known []string, options ...yaseer.Option) (Engine, error) {
	sr, err := yaseer.New(options...)
	if err != nil {
		return nil, fmt.Errorf("parser failed to created seer with %w", err)
	}

	return &instance{
		schema: schema.(*schemaDef),
		seer:   sr,
		envs:   known,
	}, nil
}

func (s *instance) Parse() (object.Object[SeerRef], error) {
	return load[SeerRef](s.schema.root, s.seer.Query(), s.walk(""))
}

func (s *instance) Process() (object.Object[object.Refrence], error) {
	return s.ProcessFor("")
}

// ProcessFor reads the project AS ONE ENVIRONMENT sees it: every marked attribute takes
// its `<key>_<env>` spelling where the document writes one. The documents are parsed
// once and read many times, which is the point of it being an argument.
func (s *instance) ProcessFor(env string) (object.Object[object.Refrence], error) {
	return load[object.Refrence](s.schema.root, s.seer.Query(), s.walk(env))
}

func (s *instance) walk(env string) walk {
	return walk{strict: s.schema.root.Strict, env: env, envs: s.envs}
}

func (s *instance) Schema() Schema {
	return s.schema
}
