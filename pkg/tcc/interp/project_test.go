package interp

import (
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/tcc/engine"
)

// projectionRoot is a vocabulary with two kinds of resource and a scope that holds
// them: enough to show every structural rule a projection keeps.
func projectionRoot() *engine.Node {
	isID := func(v any) error {
		if s, _ := v.(string); len(s) > 2 && s[:2] == "Qm" {
			return nil
		}
		return errors.New("not an id")
	}
	res := func(key string) *engine.Node {
		return mkResGroup(key, []*engine.Attribute{{Name: "id", Validator: isID}, {Name: "name"}}, nil)
	}
	scope := &engine.Node{Group: true, Match: "space", Children: []*engine.Node{{
		Group: true, Meta: map[string]any{},
		Attributes: []*engine.Attribute{{Name: "id", Validator: isID}, {Name: "name"}},
		Children:   []*engine.Node{res("fn"), res("db")},
	}}}
	root := mkRoot(res("fn"), res("db"), scope)
	root.Meta = map[string]any{"identity": [2]string{"id", "name"}}
	return root
}

func projectionObject() map[string]any {
	return map[string]any{
		ProjectKey: map[string]any{
			"id": "QmP", "name": "p",
			"fn": map[string]any{
				"QmF1": map[string]any{"name": "a", "db": map[string]any{"orders": map[string]any{"ops": []any{"get"}}}},
				"QmF2": map[string]any{"name": "b"},
			},
			"db": map[string]any{"QmD1": map[string]any{"name": "orders"}},
			"space": map[string]any{
				"QmS1": map[string]any{"name": "shop", "fn": map[string]any{"QmF3": map[string]any{"name": "c"}}},
				"QmS2": map[string]any{"name": "empty", "db": map[string]any{"QmD2": map[string]any{"name": "x"}}},
			},
		},
		IndexesKey: map[string]any{
			"projects/QmP/fn/a/links":            []string{"branches/main/projects/QmP/fn/QmF1"},
			"projects/QmP/fn/b/links":            []any{"branches/main/projects/QmP/fn/QmF2"},
			"projects/QmP/space/QmS2/db/x/links": []string{"branches/main/projects/QmP/space/QmS2/db/QmD2"},
			"projects/QmP/space/QmS1/fn/c/links": []string{"branches/main/projects/QmP/space/QmS1/fn/QmF3"},
			"projects/QmP/hole":                  []string{},
			"projects/QmP/fact":                  "QmF2",
			"projects/QmP/other":                 42,
		},
		LoweredKey: map[string]any{
			"resources": map[string]any{"QmF2": map[string]any{"from": "QmF1"}, "QmF3": map[string]any{"from": "QmF1"}},
			"entries": []any{
				map[string]any{"on": "QmF2", "value": "x"},
				map[string]any{"on": "QmF1", "value": "y"},
			},
		},
		MountedKey: map[string]any{"QmF1": map[string]any{"repo": "r"}, "QmF2": map[string]any{"repo": "r"}},
		"beside":   "kept",
	}
}

func TestAProjectionKeepsWhatItHoldsAndTheScopesItSitsIn(t *testing.T) {
	root, flat := projectionRoot(), projectionObject()
	holds := map[string]bool{"QmF1": true, "QmD1": true, "QmF3": true}
	got := Project(root, flat, func(id string) bool { return holds[id] })

	project := got[ProjectKey].(map[string]any)
	require.Equal(t, []string{"QmF1"}, keys(project["fn"]))
	require.Equal(t, []string{"QmD1"}, keys(project["db"]))
	// the scope stays because something in it does; the other goes with all it held
	require.Equal(t, []string{"QmS1"}, keys(project["space"]))
	shop := project["space"].(map[string]any)["QmS1"].(map[string]any)
	require.Equal(t, "shop", shop["name"])
	// an access entry is not a resource, and travels with what declares it
	require.Contains(t, project["fn"].(map[string]any)["QmF1"].(map[string]any)["db"], "orders")

	idx := got[IndexesKey].(map[string]any)
	require.Contains(t, idx, "projects/QmP/fn/a/links")
	require.Contains(t, idx, "projects/QmP/space/QmS1/fn/c/links")
	require.NotContains(t, idx, "projects/QmP/fn/b/links", "every value named something dropped")
	require.NotContains(t, idx, "projects/QmP/space/QmS2/db/x/links", "the key names a dropped scope")
	require.NotContains(t, idx, "projects/QmP/fact")
	require.Contains(t, idx, "projects/QmP/hole", "a reservation held nothing to begin with")
	require.Equal(t, 42, idx["projects/QmP/other"])

	low := got[LoweredKey].(map[string]any)
	require.Equal(t, []string{"QmF3"}, keys(low["resources"]))
	require.Len(t, low["entries"], 1)
	require.Equal(t, []string{"QmF1"}, keys(got[MountedKey]))
	require.Equal(t, "kept", got["beside"])

	// THE ORIGINAL IS UNTOUCHED, and every id is the one it had.
	require.Equal(t, projectionObject(), flat)
	require.Equal(t, []string{"QmD1", "QmF1", "QmF3", "QmS1"}, Held(root, got))
}

func TestAProjectionOfEverythingIsTheObject(t *testing.T) {
	root, flat := projectionRoot(), projectionObject()
	got := Project(root, flat, func(string) bool { return true })
	require.Equal(t, flat, got)
	require.Equal(t, []string{"QmD1", "QmD2", "QmF1", "QmF2", "QmF3", "QmS1", "QmS2"}, Held(root, flat))
}

func TestAProjectionOfNothingKeepsOnlyTheProject(t *testing.T) {
	root, flat := projectionRoot(), projectionObject()
	got := Project(root, flat, func(string) bool { return false })
	project := got[ProjectKey].(map[string]any)
	require.Equal(t, []string{"id", "name"}, keys(project))
	require.Empty(t, Held(root, got))
	require.Equal(t, map[string]any{"projects/QmP/hole": []string{}, "projects/QmP/other": 42}, got[IndexesKey])

	// a typed record, as it is fresh from a compile
	flat[LoweredKey] = map[string]any{"entries": []map[string]any{{"on": "QmF1"}, {"on": "QmZ"}}}
	got = Project(root, flat, func(string) bool { return false })
	require.Len(t, got[LoweredKey].(map[string]any)["entries"], 1)
}

func TestAHeldScopeStaysEvenWhenEmpty(t *testing.T) {
	root, flat := projectionRoot(), projectionObject()
	got := Project(root, flat, func(id string) bool { return id == "QmS2" })
	space := got[ProjectKey].(map[string]any)["space"].(map[string]any)
	require.Equal(t, []string{"QmS2"}, keys(space))
	require.NotContains(t, space["QmS2"], "db")
	require.Empty(t, Project(root, map[string]any{}, func(string) bool { return true }))
}

func keys(v any) []string {
	m, _ := v.(map[string]any)
	return slices.Sorted(maps.Keys(m))
}
