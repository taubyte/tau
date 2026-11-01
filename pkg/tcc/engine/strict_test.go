package engine

import (
	"testing"

	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

// The vocabulary under test: one kind with a nested block, an Either() switch, an
// All() level, and a name-keyed child group.
func strictSchema() Schema {
	return SchemaDefinition(Root(
		Attributes(String("id")),
		DefineGroup("things",
			DefineIter(
				[]*Attribute{
					String("id", Required()),
					String("name"),
					String("call", Path("execution", "call")),
					String("mem", Path("execution", "memory")),
					String("regex", Path("regex")),
					String("kind", Path("engine", Either("local", "remote")), Captures()),
					String("host", Path("engine", Either("local", "remote"), "host")),
					String("anything", Path("labels", All(), "value")),
				},
				Nested(DefineGroup("uses",
					DefineIter([]*Attribute{String("ops")}),
				)),
			),
		),
	))
}

func compileDoc(t *testing.T, doc string) error {
	t.Helper()
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/config.yaml", []byte("id: root\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/things/one.yaml", []byte(doc), 0o644))

	e, err := New(strictSchema(), yaseer.VirtualFS(fs, "/"))
	assert.NilError(t, err)
	_, err = e.Process()
	return err
}

func TestStrictAcceptsEverythingTheSchemaReads(t *testing.T) {
	assert.NilError(t, compileDoc(t, `
id: one
name: one
execution:
    call: main
    memory: 32MB
regex: "yes"
engine:
    local:
        host: here
labels:
    whatever-the-author-wants:
        value: x
uses:
    other:
        ops: get
`))
}

func TestStrictRejectsUnknownKey(t *testing.T) {
	err := compileDoc(t, "id: one\nsource: true\n")
	assert.ErrorContains(t, err, "unknown key 'source'")
	// column is the VALUE's, which is where every other error in this engine points
	assert.ErrorContains(t, err, "/things/one.yaml:2:", "at the line that caused it")
	assert.ErrorContains(t, err, "valid keys here:", "and saying what was allowed")
}

func TestStrictRejectsUnknownNestedKey(t *testing.T) {
	err := compileDoc(t, "id: one\nexecution:\n    entrypoint: main\n")
	assert.ErrorContains(t, err, "unknown key 'entrypoint'")
	assert.ErrorContains(t, err, "call, memory", "the suggestion is scoped to this level")
}

func TestStrictRejectsUnlistedEitherBranch(t *testing.T) {
	err := compileDoc(t, "id: one\nengine:\n    sideways:\n        host: here\n")
	assert.ErrorContains(t, err, "unknown key 'sideways'")
	assert.ErrorContains(t, err, "local, remote")
}

func TestStrictChecksInsideNestedGroupEntries(t *testing.T) {
	const withEngine = "id: one\nengine:\n    local:\n        host: here\n"
	assert.NilError(t, compileDoc(t, withEngine+"uses:\n    anything:\n        ops: get\n"))

	err := compileDoc(t, withEngine+"uses:\n    other:\n        opz: get\n")
	assert.ErrorContains(t, err, "unknown key 'opz'")
}

func TestStrictIgnoresANodeThatClaimsNothing(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/config.yaml", []byte("whatever: 1\n"), 0o644))

	e, err := New(SchemaDefinition(&Node{}), yaseer.VirtualFS(fs, "/"))
	assert.NilError(t, err)
	_, err = e.Process()
	assert.NilError(t, err)
}

func TestStrictIgnoresNeighbouringFilesOnDisk(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/config.yaml", []byte("id: root\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/things/one.yaml",
		[]byte("id: one\nengine:\n    local:\n        host: here\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/.git/HEAD", []byte("ref: refs/heads/main\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/README.md", []byte("# hi\n"), 0o644))
	assert.NilError(t, fs.MkdirAll("/docs", 0o755))

	e, err := New(strictSchema(), yaseer.VirtualFS(fs, "/"))
	assert.NilError(t, err)
	_, err = e.Process()
	assert.NilError(t, err, "a neighbouring directory is not an unknown key")
}

func TestEveryVocabularyRefusesAnUnknownKey(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/config.yaml", []byte("id: root\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/things/one.yaml",
		[]byte("id: one\nengine:\n    local:\n        host: here\nreplicas: 3\n"), 0o644))

	plain := SchemaDefinition(Root(
		Attributes(String("id")),
		DefineGroup("things", DefineIter([]*Attribute{
			String("id", Required()),
			String("kind", Path("engine", Either("local", "remote")), Captures()),
			String("host", Path("engine", Either("local", "remote"), "host")),
		})),
	))

	e, err := New(plain, yaseer.VirtualFS(fs, "/"))
	assert.NilError(t, err)
	_, err = e.Process()
	assert.ErrorContains(t, err, "unknown key 'replicas'")
}

func TestStrictRejectsBlockWhereValueBelongs(t *testing.T) {
	err := compileDoc(t, "id: one\nname:\n    first: x\n")
	assert.ErrorContains(t, err, "'name' takes a value, not a block")

	assert.NilError(t, compileDoc(t, "id: one\nname: x\n"))
}

func TestStrictReportsTheSameUnknownKeyEveryTime(t *testing.T) {
	const doc = "id: one\nzeta: 1\nalpha: 2\nmid: 3\n"
	first := compileDoc(t, doc)
	assert.ErrorContains(t, first, "unknown key 'alpha'")
	for range 50 {
		assert.Equal(t, compileDoc(t, doc).Error(), first.Error())
	}
}

func TestAnEmptyBranchIsABlock(t *testing.T) {
	schema := SchemaDefinition(Root(Attributes(String("id")),
		DefineGroup("things", DefineIter([]*Attribute{
			String("id", Required()),
			String("kind", Path("engine", Either("local", "remote")), Captures()),
		}))))
	flat, err := processFiles(t, schema, nil, nil, map[string]string{"/things/one.yaml": "id: one\nengine:\n    local: {}\n"})
	assert.NilError(t, err)
	assert.Equal(t, thing(t, flat, "one")["kind"], "local")
	_, err = processFiles(t, schema, nil, nil, map[string]string{"/things/one.yaml": "id: one\nengine:\n    local:\n        port: 1\n"})
	assert.ErrorContains(t, err, "unknown key 'port'")
}
