package engine

import (
	"testing"

	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

// A block is one object under a fixed key, holding fields and groups of its own: a
// package's `build:`, with its steps in order and its inputs by name.
func blockSchema() Schema {
	return SchemaDefinition(Root(
		Attributes(String("id")),
		DefineGroup("things",
			DefineIter([]*Attribute{String("id", Required()), String("type")},
				Nested(DefineBlock("build", []*Attribute{
					String(Key("dockerfile", "path")),
					String(Key("dockerfile", "target"), Default("last")),
				},
					DefineList("steps", []*Attribute{
						String("name", Required()),
						StringSlice("commands", Required()),
					}),
					DefineGroup("inputs", DefineIter([]*Attribute{String("name", Required())})),
				)),
			),
		),
	))
}

const blockDoc = `id: one
type: static
build:
    dockerfile:
        path: Dockerfile
        target: prod
    steps:
        - name: site
          commands:
            - pnpm build
        - name: shrink
          commands:
            - wasm-opt
    inputs:
        ui:
            name: ui
`

func compileBlock(t *testing.T, files map[string]string) (map[string]any, error) {
	t.Helper()
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/config.yaml", []byte("id: root\n"), 0o644))
	for path, body := range files {
		assert.NilError(t, afero.WriteFile(fs, path, []byte(body), 0o644))
	}
	e, err := New(blockSchema(), yaseer.VirtualFS(fs, "/"))
	assert.NilError(t, err)
	obj, err := e.Process()
	if err != nil {
		return nil, err
	}
	return obj.Flat()["things"].(map[string]any)["one"].(map[string]any), nil
}

func TestABlockHoldsItsFieldsAListAndAGroup(t *testing.T) {
	one, err := compileBlock(t, map[string]string{"/things/one.yaml": blockDoc})
	assert.NilError(t, err)
	build := one["build"].(map[string]any)
	assert.DeepEqual(t, build["dockerfile"], map[string]any{"path": "Dockerfile", "target": "prod"})
	steps := build["steps"].([]any)
	assert.Equal(t, len(steps), 2)
	assert.Equal(t, steps[0].(map[string]any)["name"], "site")
	assert.Equal(t, steps[1].(map[string]any)["name"], "shrink")
	assert.DeepEqual(t, build["inputs"], map[string]any{"ui": map[string]any{"name": "ui"}})
}

func TestABlockMayBeItsOwnFile(t *testing.T) {
	one, err := compileBlock(t, map[string]string{
		"/things/one/id.yaml":   "one\n",
		"/things/one/type.yaml": "static\n",
		"/things/one/build.yaml": `steps:
    - name: site
      commands:
        - pnpm build
`,
	})
	assert.NilError(t, err)
	steps := one["build"].(map[string]any)["steps"].([]any)
	assert.Equal(t, steps[0].(map[string]any)["name"], "site")
}

func TestABlockRefusesAKeyItDoesNotDeclare(t *testing.T) {
	_, err := compileBlock(t, map[string]string{"/things/one.yaml": "id: one\nbuild:\n    stpes: []\n"})
	assert.ErrorContains(t, err, "unknown key 'stpes'; valid keys here: dockerfile, inputs, steps")
}

func TestABlockChecksWhatItHolds(t *testing.T) {
	_, err := compileBlock(t, map[string]string{"/things/one.yaml": "id: one\nbuild:\n    steps:\n        - name: site\n"})
	assert.ErrorContains(t, err, "commands")
}

func TestABlockRoundTrips(t *testing.T) {
	src := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(src, "/config.yaml", []byte("id: root\n"), 0o644))
	assert.NilError(t, afero.WriteFile(src, "/things/one.yaml", []byte(blockDoc), 0o644))
	in, err := New(blockSchema(), yaseer.VirtualFS(src, "/"))
	assert.NilError(t, err)
	obj, err := in.Process()
	assert.NilError(t, err)

	dst := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(dst, "/config.yaml", []byte(""), 0o644))
	out, err := New(blockSchema(), yaseer.VirtualFS(dst, "/"))
	assert.NilError(t, err)
	assert.NilError(t, out.Dump(obj))

	back, err := New(blockSchema(), yaseer.VirtualFS(dst, "/"))
	assert.NilError(t, err)
	again, err := back.Process()
	assert.NilError(t, err)
	assert.DeepEqual(t, again.Flat()["things"], obj.Flat()["things"])

	written, err := afero.ReadFile(dst, "/things/one.yaml")
	assert.NilError(t, err)
	assert.Assert(t, len(written) > 0)
	t.Logf("%s", written)
}
