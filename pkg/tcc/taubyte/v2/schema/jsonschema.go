package schema

import "github.com/taubyte/tau/pkg/tcc/jsonschema"

// jsonSchemaOptions labels the (generic) emitter for THIS DSL: the vendor
// extension prefix and document identity. The v2 specifics live here with the v2
// DSL, not in the generic emitter.
//
// ExtPrefix is stated rather than left empty. Empty does not mean unprefixed —
// GenerateJSONSchema falls back to a bare "x-", which is nobody's namespace in
// particular: `x-scalar` is a key any vendor may define, and a consumer reading
// one has no way to know whose it is. `x-tau-scalar` says. It is also the prefix
// v1 already emits and every Go consumer of a tau schema already decodes, so a
// reader that handles one vocabulary handles both.
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
