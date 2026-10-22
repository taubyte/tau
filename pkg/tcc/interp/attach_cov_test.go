package interp

import (
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

func TestAttachAllTurnsTagsIntoIDs(t *testing.T) {
	flat, _, err := covCompile(t, covVocab{}, covWith(covProject, map[string]string{
		"/space/shop/tag/cold.yaml": "id: t2\n",
		"/space/shop/fn/b.yaml":     "id: f2\nlabels: ['tag:cold', 'tag:hot', '', 'other:hot']\n",
		"/space/shop/fn/q.yaml":     "id: f4\nlabels: [untagged]\n",
	}))
	assert.NilError(t, err)

	assert.DeepEqual(t, covFn(t, flat, "fn", "f1")["tag"], []string{"t1"})
	assert.DeepEqual(t, covFn(t, flat, "space", "s1", "fn", "f2")["tag"], []string{"t2", "t1"})

	_, has := covFn(t, flat, "space", "s1", "fn", "f4")["tag"]
	assert.Assert(t, !has, "no tag entry, nothing attached")
	_, has = covFn(t, flat, "space", "s1", "space", "s2", "fn", "f3")["tag"]
	assert.Assert(t, !has, "no labels at all, nothing attached")
}

func TestAttachAllRefusesAnUnknownTag(t *testing.T) {
	_, _, err := covCompile(t, covVocab{}, covWith(covProject, map[string]string{
		"/fn/a.yaml": "id: f1\nlabels: ['tag:ghost']\n",
	}))
	assert.ErrorContains(t, err, "fn f1: resolving tag tags failed with")
}

func TestAttachesToAllKey(t *testing.T) {
	key, spec := attachesToAllKey(covVocab{}.root().Children)
	assert.Equal(t, key, "tag")
	assert.DeepEqual(t, spec, engine.AttachSpec{From: "labels", Sep: ":"})

	key, spec = attachesToAllKey([]*engine.Node{{Group: true, Match: "empty"}, engine.DefineGroup("db", engine.DefineIter(covIdentity()))})
	assert.Equal(t, key, "")
	assert.DeepEqual(t, spec, engine.AttachSpec{})
}

func TestAttachAllWithoutACrossCuttingKindChangesNothing(t *testing.T) {
	root := engine.Root(nil, engine.DefineGroup("db", engine.DefineIter(covIdentity(), engine.Resource("db", "Db", "Db", "db"))))
	a := &attachAll{groups: root.Children, nameField: "name"}
	scope := covObj(t, map[string]any{"db": map[string]any{"d1": map[string]any{"name": "x"}}})
	out, err := a.Process(nil, scope)
	assert.NilError(t, err)
	assert.DeepEqual(t, out.Flat(), map[string]any{"db": map[string]any{"d1": map[string]any{"name": "x"}}})
}

func TestAttachFromTagsIgnoresWhatIsNotAList(t *testing.T) {
	for _, v := range []any{"tag:hot", 3} {
		o := covObj(t, map[string]any{"labels": v})
		sel := covSel(t, o)
		assert.NilError(t, attachFromTags(nil, sel, "tag", "tag:", "labels", "name"))
		assert.Assert(t, o.Get("tag") == nil)
	}
	o := covObj(t, map[string]any{})
	assert.NilError(t, attachFromTags(nil, covSel(t, o), "tag", "tag:", "labels", "name"))
	assert.Assert(t, o.Get("tag") == nil)
}
