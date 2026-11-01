package gen

import (
	"testing"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

func TestContainerWithoutSingularErrors(t *testing.T) {
	child := engine.DefineGroup("things", engine.DefineIter([]*engine.Attribute{engine.String("id")}))
	container := engine.DefineGroup("boxes",
		engine.DefineIterGroup([]*engine.Attribute{engine.String("id")}, child))
	root := []*engine.Node{container}

	if _, err := Structs(root); err == nil {
		t.Error("Structs: want error for container without Singular(), got nil")
	}

	// With Singular(), the same container generates cleanly.
	engine.Singular("Box")(container.Children[0])
	if _, err := Structs(root); err != nil {
		t.Errorf("Structs: unexpected error with Singular() set: %v", err)
	}
}
