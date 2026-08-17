package engine

import (
	"testing"

	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

// The vocabulary under test: one kind with a nested block, an Either() switch, a
// Compat path, an All() level, and a name-keyed child group.
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
					String("regex", Path("regex"), Compat("useRegex")),
					String("kind", Path("engine", Either("local", "remote")), Key()),
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

// Everything the schema CAN read is accepted — including the shapes that are easy to
// reject by accident: an Either() branch, a Compat name, and an All() level where the
// key is the author's to choose.
func TestStrictAcceptsEverythingTheSchemaReads(t *testing.T) {
	assert.NilError(t, compileDoc(t, `
id: one
name: one
execution:
    call: main
    memory: 32MB
useRegex: "yes"
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

// The defect this exists for: a key nothing reads, dropped in silence.
func TestStrictRejectsUnknownKey(t *testing.T) {
	err := compileDoc(t, "id: one\nsource: true\n")
	assert.ErrorContains(t, err, "unknown key 'source'")
	// column is the VALUE's, which is where every other error in this engine points
	assert.ErrorContains(t, err, "/things/one.yaml:2:", "at the line that caused it")
	assert.ErrorContains(t, err, "valid keys here:", "and saying what was allowed")
}

// A typo one level down is the same mistake and has to fail the same way — this is
// where a flat key list would quietly stop working.
func TestStrictRejectsUnknownNestedKey(t *testing.T) {
	err := compileDoc(t, "id: one\nexecution:\n    entrypoint: main\n")
	assert.ErrorContains(t, err, "unknown key 'entrypoint'")
	assert.ErrorContains(t, err, "call, memory", "the suggestion is scoped to this level")
}

// An Either() enumerates its branches, so an unlisted one is refused by name rather
// than compiling into a switch that captures nothing.
func TestStrictRejectsUnlistedEitherBranch(t *testing.T) {
	err := compileDoc(t, "id: one\nengine:\n    sideways:\n        host: here\n")
	assert.ErrorContains(t, err, "unknown key 'sideways'")
	assert.ErrorContains(t, err, "local, remote")
}

// A nested group's ENTRIES are keyed by a name the author picks, but the fields
// inside one are the schema's.
func TestStrictChecksInsideNestedGroupEntries(t *testing.T) {
	const withEngine = "id: one\nengine:\n    local:\n        host: here\n"
	assert.NilError(t, compileDoc(t, withEngine+"uses:\n    anything:\n        ops: get\n"))

	err := compileDoc(t, withEngine+"uses:\n    other:\n        opz: get\n")
	assert.ErrorContains(t, err, "unknown key 'opz'")
}

// A node that declares nothing has no opinion to violate. Rejecting every key there
// would be vacuous rather than stricter, and it would break a partial schema.
func TestStrictIgnoresANodeThatClaimsNothing(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/config.yaml", []byte("whatever: 1\n"), 0o644))

	e, err := New(SchemaDefinition(&Node{}), yaseer.VirtualFS(fs, "/"))
	assert.NilError(t, err)
	_, err = e.Process()
	assert.NilError(t, err)
}

// A config tree lives inside a real repository, so the project root holds `.git`, a
// README, whatever else — none of it named by the schema and none of it a mistake.
// Query.List() falls back to directory entries when a node is not a mapping, so a
// check that did not tell those apart refused every project on disk. The dreaming
// suite is what found it: `unknown key '.git'`.
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
