package engine

import (
	"testing"

	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

// A list of objects: the entries are a PIPELINE, so the document's order is the
// meaning and the elements have positions rather than names.
func listSchema() Schema {
	return SchemaDefinition(Root(
		Attributes(String("id")),
		DefineGroup("things",
			DefineIter([]*Attribute{String("id", Required())},
				Nested(DefineList("steps", []*Attribute{
					String("name", Required()),
					String("when", InSet("request", "response"), Default("request")),
				})),
			),
		),
	))
}

func loadList(t *testing.T, doc string) (map[string]any, error) {
	t.Helper()
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/config.yaml", []byte("id: root\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/things/one.yaml", []byte(doc), 0o644))
	e, err := New(listSchema(), yaseer.VirtualFS(fs, "/"))
	assert.NilError(t, err)
	obj, err := e.Process()
	if err != nil {
		return nil, err
	}
	things := obj.Flat()["things"].(map[string]any)
	return things["one"].(map[string]any), nil
}

func steps(t *testing.T, doc string) []any {
	t.Helper()
	one, err := loadList(t, doc)
	assert.NilError(t, err)
	s, ok := one["steps"].([]any)
	assert.Assert(t, ok, "steps compiled to %T, not a list", one["steps"])
	return s
}

// The order is the document's, and it survives past nine elements — which an
// index-keyed map would not, since "10" sorts before "2".
func TestListKeepsAuthoredOrder(t *testing.T) {
	doc := "id: one\nsteps:\n"
	for i := 0; i < 12; i++ {
		doc += "    - name: s" + string(rune('a'+i)) + "\n"
	}
	got := steps(t, doc)
	assert.Equal(t, len(got), 12)
	for i, el := range got {
		assert.Equal(t, el.(map[string]any)["name"], "s"+string(rune('a'+i)))
	}
}

// An element is a full resource: defaults, InSet, Required and unknown keys all
// apply INSIDE it, which is the whole reason a list of objects beats a list of
// strings with a convention.
func TestListElementIsCheckedLikeAnyResource(t *testing.T) {
	got := steps(t, "id: one\nsteps:\n    - name: redact\n    - name: stamp\n      when: response\n")
	assert.Equal(t, got[0].(map[string]any)["when"], "request", "the default applies inside an element")
	assert.Equal(t, got[1].(map[string]any)["when"], "response")

	_, err := loadList(t, "id: one\nsteps:\n    - when: response\n")
	assert.ErrorContains(t, err, "required attribute 'name'")

	_, err = loadList(t, "id: one\nsteps:\n    - name: redact\n      whn: response\n")
	assert.ErrorContains(t, err, "unknown key 'whn'")
	assert.ErrorContains(t, err, "one.yaml:4", "the error points at the offending line INSIDE the element")

	_, err = loadList(t, "id: one\nsteps:\n    - name: redact\n      when: sideways\n")
	assert.ErrorContains(t, err, "invalid value", "InSet applies inside an element")
}

// A list authored as a mapping used to be the silent case: the entries would load
// and run in whatever order the map yielded.
func TestListRejectsAMapping(t *testing.T) {
	_, err := loadList(t, "id: one\nsteps:\n    redact:\n        name: redact\n")
	assert.ErrorContains(t, err, "ordered list")
}

func TestListRoundTrips(t *testing.T) {
	const doc = "id: one\nsteps:\n    - name: redact\n      when: response\n    - name: stamp\n"

	src := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(src, "/config.yaml", []byte("id: root\n"), 0o644))
	assert.NilError(t, afero.WriteFile(src, "/things/one.yaml", []byte(doc), 0o644))
	in, err := New(listSchema(), yaseer.VirtualFS(src, "/"))
	assert.NilError(t, err)
	obj, err := in.Process()
	assert.NilError(t, err)

	dst := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(dst, "/config.yaml", []byte(""), 0o644))
	out, err := New(listSchema(), yaseer.VirtualFS(dst, "/"))
	assert.NilError(t, err)
	assert.NilError(t, out.Dump(obj))

	back, err := afero.ReadFile(dst, "/things/one.yaml")
	assert.NilError(t, err)
	assert.Equal(t, string(back), "id: one\nsteps:\n    - name: redact\n      when: response\n    - name: stamp\n      when: request\n",
		"a list must come back a sequence, in order — not a map keyed by index")
}

// The terms that run ABOVE the engine cannot run on a value, and a list is a value.
// Refusing at construction is the difference between a build failure the author
// reads and a compiled object where one Duration is a string.
func TestDefineListRefusesTermsItCannotRun(t *testing.T) {
	defer func() {
		r := recover()
		assert.Assert(t, r != nil, "a Duration inside a list element must not be accepted")
		assert.ErrorContains(t, errFromPanic(r), "does not run inside a list element")
	}()
	DefineList("steps", []*Attribute{Duration("timeout")})
}

func errFromPanic(r any) error {
	if e, ok := r.(error); ok {
		return e
	}
	return errString(r.(string))
}

type errString string

func (e errString) Error() string { return string(e) }
