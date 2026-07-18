package interp_test

import (
	"context"
	"testing"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v1/schema"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/utils/mapstructure"
	"gotest.tools/v3/assert"
)

// TestCompiledObjectDecodesIntoStructs checks that tcc's compiled flat object is
// consumable by the structureSpec structs — the real downstream contract, since
// the object is stored ~as-is on TNS and later mapstructure-decoded into these
// structs (see pkg/tcc/internal/parity/config-compiler/decompile/*.go). Unlike TestCompile this does
// NOT compare against the old config-compiler, so it stays a valid guard once that
// compiler is retired: it catches tcc emitting a missing/mistyped field that no
// longer decodes into the struct model.
//
// Each entry decodes an instance map into its struct and returns the decoded
// Name plus the set of compiled keys that did NOT map to a struct field
// (mapstructure Metadata.Unused). An unused key is a divergence between the
// compiled-object contract and structureSpec — either an intentional drop
// (see intentionalDrops) or a silent data loss the test must fail on.
var resourceDecoders = map[string]func(any) (name string, unused []string, err error){
	"functions": func(o any) (string, []string, error) {
		var v structureSpec.Function
		var md mapstructure.Metadata
		e := mapstructure.DecodeMetadata(o, &v, &md)
		return v.Name, md.Unused, e
	},
	"databases": func(o any) (string, []string, error) {
		var v structureSpec.Database
		var md mapstructure.Metadata
		e := mapstructure.DecodeMetadata(o, &v, &md)
		return v.Name, md.Unused, e
	},
	"domains": func(o any) (string, []string, error) {
		var v structureSpec.Domain
		var md mapstructure.Metadata
		e := mapstructure.DecodeMetadata(o, &v, &md)
		return v.Name, md.Unused, e
	},
	"libraries": func(o any) (string, []string, error) {
		var v structureSpec.Library
		var md mapstructure.Metadata
		e := mapstructure.DecodeMetadata(o, &v, &md)
		return v.Name, md.Unused, e
	},
	"messaging": func(o any) (string, []string, error) {
		var v structureSpec.Messaging
		var md mapstructure.Metadata
		e := mapstructure.DecodeMetadata(o, &v, &md)
		return v.Name, md.Unused, e
	},
	"services": func(o any) (string, []string, error) {
		var v structureSpec.Service
		var md mapstructure.Metadata
		e := mapstructure.DecodeMetadata(o, &v, &md)
		return v.Name, md.Unused, e
	},
	"smartops": func(o any) (string, []string, error) {
		var v structureSpec.SmartOp
		var md mapstructure.Metadata
		e := mapstructure.DecodeMetadata(o, &v, &md)
		return v.Name, md.Unused, e
	},
	"storages": func(o any) (string, []string, error) {
		var v structureSpec.Storage
		var md mapstructure.Metadata
		e := mapstructure.DecodeMetadata(o, &v, &md)
		return v.Name, md.Unused, e
	},
	"websites": func(o any) (string, []string, error) {
		var v structureSpec.Website
		var md mapstructure.Metadata
		e := mapstructure.DecodeMetadata(o, &v, &md)
		return v.Name, md.Unused, e
	},
}

// intentionalDrops are compiled-object keys that legitimately have no
// structureSpec field, per resource. Everything else that fails to map is a
// divergence. Keep this list SMALL and documented — each entry is a known gap.
//
// Empty today: the fixture project doesn't exercise the currently-droppable
// keys (database "keyType" needs an encrypted db; function "methods" needs the
// unimplemented http-methods). If the fixture grows to cover them, add the
// field to pkg/specs/structure — or, if the drop is deliberate, an entry here.
var intentionalDrops = map[string]map[string]bool{}

func TestCompiledObjectDecodesIntoStructs(t *testing.T) {
	compiler, err := schema.New(schema.WithLocal("fixtures/config"), schema.WithBranch("master"))
	assert.NilError(t, err)

	obj, _, err := compiler.Compile(context.Background())
	assert.NilError(t, err)

	object := obj.Flat()["object"].(map[string]any)

	checked := checkResources(t, object)
	// Resources also live nested under each application.
	if apps, ok := object["applications"].(map[string]any); ok {
		for _, app := range apps {
			if m, ok := app.(map[string]any); ok {
				checked += checkResources(t, m)
			}
		}
	}

	assert.Assert(t, checked > 0, "no resources found in compiled object")
	t.Logf("decoded %d compiled resources into structureSpec structs", checked)
}

// checkResources decodes every resource instance in container into its struct and
// asserts it decodes without error and carries a Name. Returns the count checked.
func checkResources(t *testing.T, container map[string]any) int {
	t.Helper()
	count := 0
	for group, decode := range resourceDecoders {
		instances, ok := container[group].(map[string]any)
		if !ok {
			continue
		}
		for id, inst := range instances {
			m, ok := inst.(map[string]any)
			assert.Assert(t, ok, "%s/%s is not an object", group, id)

			name, unused, err := decode(m)
			assert.NilError(t, err, "%s/%s does not decode into its struct", group, id)
			assert.Assert(t, name != "", "%s/%s decoded with an empty Name", group, id)

			// A compiled key with no struct field is a divergence unless it is a
			// documented intentional drop. This is the guard that fails when
			// tcc emits a field structureSpec doesn't model.
			for _, key := range unused {
				assert.Assert(t, intentionalDrops[group][key],
					"%s/%s: compiled key %q has no structureSpec field (divergence). "+
						"Add the field to pkg/specs/structure or, if intentional, to intentionalDrops[%q].",
					group, id, key, group)
			}
			count++
		}
	}
	return count
}
