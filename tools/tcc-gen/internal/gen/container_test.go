package gen

import (
	"testing"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

// A container group (its iterator holds resource sub-groups) with no Singular()
// declaration must be a loud generation error — the generator never guesses a
// Go name from the plural key. This guards the whole reason Singular exists.
func TestContainerWithoutSingularErrors(t *testing.T) {
	child := engine.DefineGroup("things", engine.DefineIter([]*engine.Attribute{engine.String("id")}))
	container := engine.DefineGroup("boxes",
		engine.DefineIterGroup([]*engine.Attribute{engine.String("id")}, child))
	root := []*engine.Node{container}

	if _, err := Structs(root); err == nil {
		t.Error("Structs: want error for container without Singular(), got nil")
	}
	if _, err := Resources(root); err == nil {
		t.Error("Resources: want error for container without Singular(), got nil")
	}

	// With Singular(), the same container generates cleanly.
	engine.Singular("Box")(container.Children[0])
	if _, err := Structs(root); err != nil {
		t.Errorf("Structs: unexpected error with Singular() set: %v", err)
	}
	if _, err := Resources(root); err != nil {
		t.Errorf("Resources: unexpected error with Singular() set: %v", err)
	}
}
