package interp

import (
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

// mkGroup builds a minimal DSL group node (a config group whose single child is
// the iterator carrying attrs/meta) for the schema-shape tests.
func mkGroup(key string, attrs []*engine.Attribute, meta map[string]any) *engine.Node {
	if meta == nil {
		meta = map[string]any{}
	}
	return &engine.Node{
		Group: true,
		Match: key,
		Children: []*engine.Node{
			{Attributes: attrs, Meta: meta},
		},
	}
}

// mkResGroup is mkGroup with a Resource(...) descriptor on the iterator plus any
// extra meta (e.g. an index annotation) merged in.
func mkResGroup(key string, attrs []*engine.Attribute, extraMeta map[string]any) *engine.Node {
	meta := map[string]any{"resource": [4]string{key, "X", "X", key}}
	for k, v := range extraMeta {
		meta[k] = v
	}
	return mkGroup(key, attrs, meta)
}

func mkRoot(groups ...*engine.Node) *engine.Node {
	return &engine.Node{Group: true, Children: groups}
}

// A root with no Ref/AttachesToAll/index annotations must yield a pipe with none
// of those passes; turning each feature on adds exactly its pass.
func TestCompilePipe_SelectsPassesFromSchema(t *testing.T) {
	// Bare schema: one resource group, plain id/name attrs, no annotations.
	bare := mkRoot(mkResGroup("widgets", []*engine.Attribute{
		{Name: "id"}, {Name: "name"},
	}, nil))

	assert.Assert(t, !usesRefs(bare), "bare schema should not use refs")
	assert.Assert(t, !usesAttachesToAll(bare), "bare schema should not attach-to-all")
	assert.Assert(t, !UsesIndexing(bare), "bare schema should not index")

	// Only the CompileDriver — no resolveRefs / attachAll / chroot / indexDriver.
	assert.Equal(t, len(compilePipe(bare, "", "main")), 1)

	// A Ref annotation alone adds exactly ResolveRefs.
	refRoot := mkRoot(mkResGroup("widgets", []*engine.Attribute{
		{Name: "id"},
		{Name: "target", Meta: map[string]any{"ref": true}},
	}, nil))
	assert.Assert(t, usesRefs(refRoot))
	assert.Assert(t, !usesAttachesToAll(refRoot))
	assert.Assert(t, !UsesIndexing(refRoot))
	assert.Equal(t, len(compilePipe(refRoot, "", "main")), 2)

	// An AttachesToAll marker alone adds exactly AttachAll.
	attachRoot := mkRoot(mkResGroup("ops", []*engine.Attribute{{Name: "id"}},
		map[string]any{"attachesToAll": true}))
	assert.Assert(t, usesAttachesToAll(attachRoot))
	assert.Assert(t, !usesRefs(attachRoot))
	assert.Assert(t, !UsesIndexing(attachRoot))
	assert.Equal(t, len(compilePipe(attachRoot, "", "main")), 2)

	// An index annotation alone adds chroot + IndexDriver (two transforms).
	indexRoot := mkRoot(mkResGroup("widgets", []*engine.Attribute{{Name: "id"}},
		map[string]any{"indexName": true}))
	assert.Assert(t, UsesIndexing(indexRoot))
	assert.Assert(t, !usesRefs(indexRoot))
	assert.Assert(t, !usesAttachesToAll(indexRoot))
	assert.Equal(t, len(compilePipe(indexRoot, "", "main")), 3)

	// The full set: every predicate true -> the historical fixed sequence
	// {compileDriver, resolveRefs, attachAll, chroot, indexDriver} = 5 transforms.
	full := mkRoot(
		mkResGroup("widgets", []*engine.Attribute{
			{Name: "id"},
			{Name: "target", Meta: map[string]any{"ref": true}},
		}, map[string]any{"indexName": true}),
		mkResGroup("ops", []*engine.Attribute{{Name: "id"}}, map[string]any{"attachesToAll": true}),
	)
	assert.Assert(t, usesRefs(full))
	assert.Assert(t, usesAttachesToAll(full))
	assert.Assert(t, UsesIndexing(full))
	assert.Equal(t, len(compilePipe(full, "", "main")), 5)
}

// containerKey derives the nested-container config key structurally (a top-level
// group whose iterator is itself a group with children and no Resource
// descriptor), so "applications" need never be restated in the interpreter.
func TestContainerKey_DerivesNestedContainer(t *testing.T) {
	// No container group -> "".
	assert.Equal(t, containerKey(mkRoot(mkResGroup("widgets", nil, nil))), "")

	// Container: iterator is a group with children and carries no Resource meta.
	appIter := &engine.Node{
		Group:    true,
		Meta:     map[string]any{},
		Children: []*engine.Node{mkResGroup("widgets", nil, nil)},
	}
	root := mkRoot(
		mkResGroup("widgets", nil, nil),
		&engine.Node{Group: true, Match: "applications", Children: []*engine.Node{appIter}},
	)
	assert.Equal(t, containerKey(root), "applications")

	// Ref/index nested only inside the container are still detected (the predicates
	// recurse into container iterators).
	nestedIter := &engine.Node{
		Group: true,
		Meta:  map[string]any{},
		Children: []*engine.Node{
			mkResGroup("widgets", []*engine.Attribute{
				{Name: "target", Meta: map[string]any{"ref": true}},
			}, map[string]any{"indexName": true}),
		},
	}
	nestedRoot := mkRoot(&engine.Node{Group: true, Match: "applications", Children: []*engine.Node{nestedIter}})
	assert.Assert(t, usesRefs(nestedRoot))
	assert.Assert(t, UsesIndexing(nestedRoot))
}
