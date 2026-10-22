package interp

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"gotest.tools/v3/assert"
)

func TestACompileRecordsWhatItLowered(t *testing.T) {
	project, root := inheritSchema()
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml", []byte("id: root\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/pipeline/build.yaml", []byte("id: one\nname: build\n"), 0o644))
	c, err := New(project, root, WithVirtual(fs, "/"), WithLayout(Layout{Resource: "{kind}/{name}.yaml"}))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	flat := obj.Flat()
	l := LoweredOf(flat)
	made, added := l.Of("one")
	assert.Equal(t, len(made), 2)
	assert.Equal(t, len(added), 0)

	kinds := map[string]string{}
	for _, id := range made {
		assert.Assert(t, l.Produced(id))
		p := l.Resources[id]
		assert.Equal(t, p.From, "one")
		kinds[p.Kind] = p.Name
		inner := flat[ProjectKey].(map[string]any)
		_, there := inner[p.Kind].(map[string]any)[id]
		assert.Assert(t, there, "%s/%s is in the compiled object under its id", p.Kind, id)
	}
	assert.DeepEqual(t, kinds, map[string]string{"job": "build-run", "note": "build-note"})
	assert.Assert(t, !l.Produced("one"), "what was written is not what was produced")
}

func TestLoweredOfReadsTheRecord(t *testing.T) {
	assert.DeepEqual(t, LoweredOf(map[string]any{}), Lowered{Resources: map[string]Provenance{}})
	assert.DeepEqual(t, LoweredOf(map[string]any{LoweredKey: "junk"}), Lowered{Resources: map[string]Provenance{}})

	l := LoweredOf(map[string]any{LoweredKey: map[string]any{
		"resources": map[string]any{
			"j1": map[string]any{"from": "p1", "at": "steps/0", "kind": "job", "name": "a", "scope": "shop"},
			"j2": map[string]any{"from": "p1", "kind": "job", "name": "b"},
			"n1": map[string]any{"from": "p2", "kind": "note", "name": "c"},
			"x":  "not a record",
		},
		"entries": []any{
			map[string]any{"on": "j2", "path": []any{"runs", 3, "x"}, "value": "a", "from": "p1"},
			map[string]any{"on": "n1", "path": []string{"by"}, "value": "c", "from": "p2"},
			map[string]any{"on": "j1", "value": "v", "from": "p1"},
		},
	}})
	assert.DeepEqual(t, l.Resources["j1"], Provenance{From: "p1", At: "steps/0", Kind: "job", Name: "a", Scope: "shop"})
	assert.DeepEqual(t, l.Resources["x"], Provenance{})

	made, added := l.Of("p1")
	assert.DeepEqual(t, made, []string{"j1", "j2"})
	assert.DeepEqual(t, added, []Granted{
		{On: "j2", Path: []string{"runs", "x"}, Value: "a", From: "p1"},
		{On: "j1", Value: "v", From: "p1"},
	})
	made, added = l.Of("nobody")
	assert.Assert(t, made == nil && added == nil)
	assert.DeepEqual(t, l.Entries[1].Path, []string{"by"})
}

func TestRecordRoundTrips(t *testing.T) {
	in := Lowered{
		Resources: map[string]Provenance{
			"j1": {From: "p1", At: "steps/0", Kind: "job", Name: "a", Scope: "shop"},
			"j2": {From: "p1", Kind: "job", Name: "b"},
		},
		Entries: []Granted{{On: "j2", Path: []string{"runs"}, Value: "a", From: "p1"}},
	}
	obj := covObj(t, map[string]any{})
	assert.NilError(t, record(obj, in))
	flat := obj.Flat()
	rec := flat[LoweredKey].(map[string]any)
	j2 := rec["resources"].(map[string]any)["j2"].(map[string]any)
	_, hasAt := j2["at"]
	_, hasScope := j2["scope"]
	assert.Assert(t, !hasAt && !hasScope, "empty fields are not written")
	assert.DeepEqual(t, LoweredOf(flat), in)

	empty := covObj(t, map[string]any{})
	assert.NilError(t, record(empty, Lowered{Resources: map[string]Provenance{}}))
	rec = empty.Flat()[LoweredKey].(map[string]any)
	_, hasEntries := rec["entries"]
	assert.Assert(t, !hasEntries, "no entries, no entries key")
}

func TestRecordOfFindsEntriesByScopeAndName(t *testing.T) {
	_, root := inheritSchema()
	obj := covObj(t, map[string]any{ProjectKey: map[string]any{
		"id": "root",
		"job": map[string]any{
			"j1":      map[string]any{"name": "run"},
			"j0":      map[string]any{"name": ""},
			"skipped": "not a resource",
		},
		"space": map[string]any{
			"s1": map[string]any{"name": "shop", "job": map[string]any{"j2": map[string]any{"name": "run"}}},
		},
	}})
	docs := []placement{{id: "j1", kind: "job", name: "run", from: "p1", part: "steps/0", scope: ""}}
	adds := []granted{
		{on: End{Kind: "job", Name: "run"}, path: []string{"by"}, value: "a", from: "p1"},
		{on: End{Kind: "job", Name: "run"}, scope: "shop", path: []string{"by"}, value: "b", from: "p1"},
		{on: End{Kind: "job", Name: "ghost"}, path: []string{"by"}, value: "c", from: "p1"},
	}
	l := recordOf(root, obj, docs, adds)
	assert.DeepEqual(t, l.Resources, map[string]Provenance{"j1": {From: "p1", At: "steps/0", Kind: "job", Name: "run"}})
	assert.DeepEqual(t, l.Entries, []Granted{
		{On: "j1", Path: []string{"by"}, Value: "a", From: "p1"},
		{On: "j2", Path: []string{"by"}, Value: "b", From: "p1"},
	})
}
