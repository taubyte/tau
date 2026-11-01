package schema

import "github.com/taubyte/tau/pkg/tcc/jsonschema"

// jsonSchemaOptions labels the generic emitter for this vocabulary. ExtPrefix is
// stated because an empty one falls back to a bare `x-`; `x-tau-` is also what v1
// emits.
var jsonSchemaOptions = jsonschema.JSONSchemaOptions{
	ExtPrefix:   "x-tau-",
	ID:          "https://taubyte.com/schemas/config/v2",
	Title:       "tau v2 project configuration",
	Description: "The v2 DSL and its constraints. A resource is one flat document at <kind>/<name>.yaml; a scope is a directory — namespace/<name>/meta.yaml beside that scope's own <kind>/ dirs, nesting as deep as it goes. This schema models the project AS WRITTEN: compiling lowers, so the object that runs holds resources no document here describes. x-tau-ref / x-tau-validation mark checks the compiler enforces that a plain JSON Schema validator cannot.",
}

// JSONSchema returns the Draft 2020-12 JSON Schema describing this DSL and its
// constraints, generated from the live schema definition — the same source the
// compiler uses, so the schema can never drift from what actually compiles. It is
// the single source used by the wasm client's schema() export and any Go caller.
func JSONSchema() ([]byte, error) {
	return jsonschema.GenerateJSONSchema(GenerationRoot(), jsonSchemaOptions)
}
