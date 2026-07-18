package interp

import (
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
)

// TC carries the per-compile runtime parameters a GroupTransform closure needs
// but the static DSL annotation cannot hold: the compile-target cloud FQDN and
// the branch. The CompileDriver constructs it from its own runtime fields.
type TC struct {
	Cloud  string
	Branch string
}

// GroupTransformFunc is a whole-group projection: the driver runs it at the scope
// where the annotated group appears, in place of the generic per-instance walk.
type GroupTransformFunc func(tc *TC, scope object.Object[object.Refrence]) error

// GroupTransform stores a whole-group projection closure on a group's iterator
// node (via engine.GroupAnnotate). The CompileDriver runs it instead of the
// generic per-instance processing — used for `clouds`, whose map is flattened to
// project-root scalars and dropped, never promoted per instance.
func GroupTransform(fn GroupTransformFunc) engine.NodeOption {
	return engine.GroupAnnotate("groupTransform", fn)
}
