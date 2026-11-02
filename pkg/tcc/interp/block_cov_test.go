package interp

import (
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

func blockRoot() *engine.Node {
	return engine.Root([]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("pkg", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required()), engine.String("name"), engine.String("type"),
		}, engine.Nested(engine.DefineBlock("build", []*engine.Attribute{
			engine.String("image", engine.Delegable()),
			engine.StringSlice("tags"),
		},
			engine.DefineList("steps", []*engine.Attribute{engine.String("run")}),
			engine.DefineGroup("inputs", engine.DefineIter([]*engine.Attribute{engine.StringSlice("names")})),
		)), engine.Resource("pkg", "Pkg", "Pkg", "pkg"))),
	).With(engine.Leaf("project"), engine.Identity("id", "name"))
}

func TestABlocksFieldsFollowItsKey(t *testing.T) {
	root := blockRoot()
	assert.Assert(t, listAt(root, "pkg", []string{"build", "tags"}))
	assert.Assert(t, !listAt(root, "pkg", []string{"build", "image"}))
	assert.Assert(t, listAt(root, "pkg", []string{"build", "inputs", "ui", "names"}), "a group inside a block still skips its entry name")

	assert.DeepEqual(t, Remainder(root, "pkg"), []string{
		"build/image", "build/inputs/names", "build/steps/run", "build/tags", "type",
	})

	// A delegable field inside a block delegates the block.
	assert.DeepEqual(t, delegableTop(root, "pkg"), map[string]bool{"build": true})
}
