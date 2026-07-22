package schema

import "github.com/taubyte/tau/pkg/tcc/jsonschema"

// jsonSchemaOptions labels the (generic) emitter for THIS DSL: the vendor
// extension prefix and document identity. The Taubyte specifics live here with
// the Taubyte DSL, not in the generic emitter.
var jsonSchemaOptions = jsonschema.JSONSchemaOptions{
	ExtPrefix:   "x-tau-",
	ID:          "https://taubyte.com/schemas/config/v1",
	Title:       "Taubyte project configuration",
	Description: "The Taubyte DSL and its constraints. Resources are authored one file per instance under <group>/<name>.yaml (and applications/<app>/<group>/<name>.yaml); this schema models the assembled project. x-tau-ref / x-tau-validation mark checks the compiler enforces that a plain JSON Schema validator cannot.",
}

// JSONSchema returns the Draft 2020-12 JSON Schema describing this DSL and its
// constraints, generated from the live schema definition — the same source the
// compiler uses, so the schema can never drift from what actually compiles. It is
// the single source used by tcc-gen (the committed config.schema.json), the wasm
// client's schema() export, and any Go caller.
func JSONSchema() ([]byte, error) {
	return jsonschema.GenerateJSONSchema(GenerationRoot(), jsonSchemaOptions)
}
