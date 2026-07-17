package compiler

import (
	"context"
	"testing"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/utils/mapstructure"
	"gotest.tools/v3/assert"
)

// TestCompiledObjectDecodesIntoStructs checks that tcc's compiled flat object is
// consumable by the structureSpec structs — the real downstream contract, since
// the object is stored ~as-is on TNS and later mapstructure-decoded into these
// structs (see pkg/tcc/internal/parity/decompile/*.go). Unlike TestCompile this does
// NOT compare against the old config-compiler, so it stays a valid guard once that
// compiler is retired: it catches tcc emitting a missing/mistyped field that no
// longer decodes into the struct model.
//
// Each entry decodes an instance map into its struct and returns the decoded Name,
// which every resource must carry (the id is the map key, not a field).
var resourceDecoders = map[string]func(any) (string, error){
	"functions": func(o any) (string, error) {
		var v structureSpec.Function
		e := mapstructure.Decode(o, &v)
		return v.Name, e
	},
	"databases": func(o any) (string, error) {
		var v structureSpec.Database
		e := mapstructure.Decode(o, &v)
		return v.Name, e
	},
	"domains": func(o any) (string, error) {
		var v structureSpec.Domain
		e := mapstructure.Decode(o, &v)
		return v.Name, e
	},
	"libraries": func(o any) (string, error) {
		var v structureSpec.Library
		e := mapstructure.Decode(o, &v)
		return v.Name, e
	},
	"messaging": func(o any) (string, error) {
		var v structureSpec.Messaging
		e := mapstructure.Decode(o, &v)
		return v.Name, e
	},
	"services": func(o any) (string, error) {
		var v structureSpec.Service
		e := mapstructure.Decode(o, &v)
		return v.Name, e
	},
	"smartops": func(o any) (string, error) {
		var v structureSpec.SmartOp
		e := mapstructure.Decode(o, &v)
		return v.Name, e
	},
	"storages": func(o any) (string, error) {
		var v structureSpec.Storage
		e := mapstructure.Decode(o, &v)
		return v.Name, e
	},
	"websites": func(o any) (string, error) {
		var v structureSpec.Website
		e := mapstructure.Decode(o, &v)
		return v.Name, e
	},
}

func TestCompiledObjectDecodesIntoStructs(t *testing.T) {
	compiler, err := New(WithLocal("fixtures/config"), WithBranch("master"))
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

			name, err := decode(m)
			assert.NilError(t, err, "%s/%s does not decode into its struct", group, id)
			assert.Assert(t, name != "", "%s/%s decoded with an empty Name", group, id)
			count++
		}
	}
	return count
}
