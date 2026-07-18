package driver

import (
	"fmt"

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

// FlattenClouds promotes the project's `clouds.<fqdn>.{account, plan}` entry for
// the compile-target cloud into flat `account` / `plan` scalars at the project
// root, then drops the entire `clouds` map. Empty fqdn (dream / non-cloud-aware
// tooling) drops the map without promotion. A partial entry (one half set) fails
// compile so `tau validate config` flags the bad shape too. Ported verbatim from
// the old pass1/cloud.go (c.fqdn -> tc.Cloud), wired onto cloudsGroup() in the
// schema via GroupTransform.
func FlattenClouds(tc *TC, o object.Object[object.Refrence]) error {
	cloudsObj, err := o.Child("clouds").Object()
	if err == object.ErrNotExist {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading clouds map failed with %w", err)
	}
	defer o.Delete("clouds")

	if tc.Cloud == "" {
		return nil
	}

	entryObj, err := cloudsObj.Child(tc.Cloud).Object()
	if err == object.ErrNotExist {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading clouds[%q] failed with %w", tc.Cloud, err)
	}

	account, _ := entryObj.GetString("account")
	plan, _ := entryObj.GetString("plan")

	if (account == "") != (plan == "") {
		return fmt.Errorf(
			"project config: clouds[%q] is incomplete; both `account` and `plan` must be set or the entry must be omitted (got account=%q plan=%q)",
			tc.Cloud, account, plan,
		)
	}
	if account == "" {
		return nil
	}

	o.Set("account", account)
	o.Set("plan", plan)
	return nil
}
