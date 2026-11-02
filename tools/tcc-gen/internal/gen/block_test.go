package gen

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

func blockKinds() []*engine.Node {
	return []*engine.Node{
		engine.DefineGroup("pkg", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required()),
			engine.String("type"),
		},
			engine.Resource("pkg", "Pkg", "Pkg", "pkg"),
			engine.Nested(engine.DefineBlock("build", []*engine.Attribute{
				engine.String(engine.Key("dockerfile", "path")),
			},
				engine.DefineList("steps", []*engine.Attribute{
					engine.String("name", engine.Required()),
					engine.StringSlice("commands"),
				}),
				engine.DefineGroup("inputs", engine.DefineIter([]*engine.Attribute{engine.String("name")})),
			)),
		)),
	}
}

// A block's fields are read one key down, and its groups at their whole path, as
// members of the block's own type.
func TestABlockIsReadAtItsKey(t *testing.T) {
	root := blockKinds()
	var paths []string
	for _, l := range Locations(root, "pkg") {
		paths = append(paths, l.Path())
	}
	require.Contains(t, paths, "build/dockerfile/path")

	var groups []string
	for _, g := range Groups(root, "pkg") {
		groups = append(groups, g.Path())
	}
	require.Equal(t, []string{"build/inputs", "build/steps"}, groups)

	out := string(RenderReaders("specs", root, []string{"pkg"}))
	require.Contains(t, out, `r.String("build", "dockerfile", "path")`)
	require.Contains(t, out, `r.List("build", "steps")`)
	require.Contains(t, out, `r.Keys("build", "inputs")`)
	require.Contains(t, out, `out.Build = ReadPkgBuild(r)`)
	require.Contains(t, out, `r.Has("build", "steps")`, "a block of groups alone is still declared when written")
}
