package interp

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp/utils"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

// CompileDriver walks the project root and each application scope alongside the
// DSL groups, reading the behavior off attribute/node annotations.
type CompileDriver struct {
	root *engine.Node
}

// newCompileDriver builds a CompileDriver from the schema root node. `root` carries
// the project-scope attributes (id -> project_id validation, tags -> wire drop via
// annotations) and the resource/container groups as its children. Takes the node
// tree as data so this package never imports schema.
func newCompileDriver(root *engine.Node) transform.Transformer[object.Refrence] {
	return &CompileDriver{root: root}
}

func (d *CompileDriver) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	// Fork once with the project root so the context path matches the
	// utils.Global(...) shape ([root]) — the name->id index keys derive from it.
	ctRoot := ct.Fork(o)

	// Project-scope attribute ops: drop the root `tags`, emit the project_id
	// validation. These live on the root node's own attributes.
	if err := d.applyRootOps(ctRoot, o, d.root.Attributes); err != nil {
		return nil, err
	}

	// Groups: resources (project scope) and the applications container (id-promote
	// each app, then recurse into its scope).
	if err := d.processGroups(ctRoot, o, d.root.Children, false); err != nil {
		return nil, err
	}

	return o, nil
}

// processGroups runs each group at the given scope (project root or an app).
//
// mixed is a hybrid container's scope, where its own nested entries and the resources
// it holds share a key. They are told apart by the id field BEFORE anything runs:
// promoting a held resource deletes its id, so a group walked after that would take
// it for an entry and project it a second time.
func (d *CompileDriver) processGroups(ct transform.Context[object.Refrence], scope object.Object[object.Refrence], groups []*engine.Node, mixed bool) error {
	var own map[string]bool
	if mixed {
		own = ownEntries(scope, groups, d.root)
	}
	for _, g := range groups {
		if err := d.processGroup(ct, scope, g, mixed, own); err != nil {
			return err
		}
	}
	return nil
}

// ownEntries is every "<group>/<entry>" in scope that carries no id: the entries a
// hybrid container declares for itself rather than the resources it holds.
func ownEntries(scope object.Object[object.Refrence], groups []*engine.Node, root *engine.Node) map[string]bool {
	idField, _ := engine.IdentityOf(root)
	own := map[string]bool{}
	for _, g := range groups {
		key, _ := g.Match.(string)
		groupObj, err := scope.Child(key).Object()
		if err != nil {
			continue
		}
		for _, name := range groupObj.Children() {
			if v, err := groupObj.Child(name).Get(idField); err != nil || v == nil {
				own[key+"/"+name] = true
			}
		}
	}
	return own
}

func (d *CompileDriver) processGroup(ct transform.Context[object.Refrence], scope object.Object[object.Refrence], g *engine.Node, mixed bool, own map[string]bool) error {
	groupKey, _ := g.Match.(string)
	if len(g.Children) == 0 {
		return nil
	}
	if g.Sequence {
		return nil
	}
	iter := g.Children[0]
	_, isResource := iter.Meta["resource"].([4]string)

	groupObj, err := scope.Child(groupKey).Object()
	if err == object.ErrNotExist {
		return nil
	}
	if err != nil {
		return fmt.Errorf("fetching %s failed with %w", groupKey, err)
	}

	// A container group (its iterator is itself a group holding nested resource
	// groups — the applications container). Each instance is id-promoted with NO
	// index (no Resource descriptor), then its
	// nested resource groups are processed in that instance's scope.
	if iter.Group && len(iter.Children) > 0 {
		idField, nameField := engine.IdentityOf(d.root)
		for _, instName := range groupObj.Children() {
			sel := groupObj.Child(instName)
			instObj, err := sel.Object()
			if err != nil {
				return fmt.Errorf("fetching %s/%s failed with %w", groupKey, instName, err)
			}
			if isResource {
				if err := applyInstanceOps(sel, iter.Attributes); err != nil {
					return fmt.Errorf("%s %s: %w", groupKey, instName, err)
				}
			}
			id, err := utils.RenameById(sel, instName, idField, nameField)
			if err != nil {
				return fmt.Errorf("promoting %s %s failed with %w", groupKey, instName, err)
			}
			if isResource {
				if err := utils.IndexById(ct, groupKey, instName, id, nameField); err != nil {
					return fmt.Errorf("indexing %s %s failed with %w", groupKey, instName, err)
				}
			}
			if err := d.processGroups(ct.Fork(instObj), instObj, iter.Children, isResource && iter.Nested); err != nil {
				return err
			}
		}
		return nil
	}

	// A resource group: per-instance attribute projections, then id-promotion,
	// then (for Resource groups) the name->id index.
	// WHICH ATTRIBUTE IS THE IDENTITY is the schema's to say (engine.Identity, on the
	// root): a group whose iterator declares it is keyed by it, and the document's own
	// name becomes the resource's.
	idField, nameField := engine.IdentityOf(d.root)
	promote := idField != "" && hasAttr(iter, idField)
	for _, instName := range groupObj.Children() {
		sel := groupObj.Child(instName)
		if mixed && g.Nested != own[groupKey+"/"+instName] {
			continue
		}
		if err := applyInstanceOps(sel, iter.Attributes); err != nil {
			return fmt.Errorf("%s %s: %w", groupKey, instName, err)
		}
		if promote {
			idStr, err := utils.RenameById(sel, instName, idField, nameField)
			if err != nil {
				return fmt.Errorf("promoting %s %s failed with %w", groupKey, instName, err)
			}
			if isResource {
				if err := utils.IndexById(ct, groupKey, instName, idStr, nameField); err != nil {
					return fmt.Errorf("indexing %s %s failed with %w", groupKey, instName, err)
				}
			}
		}

		// A nested resource's own groups (a resource's `caches:`) are processed in
		// the INSTANCE's object but the ENCLOSING scope's context — deliberately not
		// forked. A nested group is data belonging to the instance, not a scope: the
		// instance has already had its per-instance ops, its id promotion and its
		// index, and names inside the group resolve where the resource was authored.
		// Forking here would make the resource a scope and break both.
		if iter.Nested {
			instObj, err := sel.Object()
			if err != nil {
				return fmt.Errorf("fetching %s/%s failed with %w", groupKey, instName, err)
			}
			if err := d.processGroups(ct, instObj, iter.Children, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyInstanceOps runs the per-resource-instance projections: each scalar term
// (Duration -> ns, Bytes -> bytes) parses its own value.
func applyInstanceOps(sel object.Selector[object.Refrence], attrs []*engine.Attribute) error {
	for _, a := range attrs {
		if sc, ok := a.Meta["scalar"].(engine.ScalarSpec); ok {
			holder, field := holderOf(sel, a)
			if err := sc.Parse(holder, field); err != nil {
				return err
			}
		}
	}
	return nil
}

// holderOf is the selector a value actually sits under, and the key it sits at. A
// single-segment key is on the resource itself; a multi-segment one is a level down,
// because that is what Key("execution", "call") compiles to.
func holderOf(sel object.Selector[object.Refrence], a *engine.Attribute) (object.Selector[object.Refrence], string) {
	segments := engine.CompiledSegments(a)
	last := len(segments) - 1
	if last == 0 {
		return sel, segments[0]
	}
	o, err := sel.Object()
	if err != nil {
		return sel, a.Name
	}
	holder := o.Child(segments[0])
	for _, seg := range segments[1:last] {
		next, err := holder.Object()
		if err != nil {
			return sel, a.Name
		}
		holder = next.Child(seg)
	}
	return holder, segments[last]
}

// applyRootOps applies the project-scope attribute annotations: EmitValidation
// (push a deferred external validation keyed off the attribute's value) and
// WireDrop (delete the compiled key).
func (d *CompileDriver) applyRootOps(ct transform.Context[object.Refrence], o object.Object[object.Refrence], attrs []*engine.Attribute) error {
	for _, a := range attrs {
		if ve, ok := a.Meta["emitValidation"].(engine.ValidationEmit); ok {
			// THE SAME READ emitAttrValidations does, through the same helper:
			// by the PATH the key is, and absent emits nothing. It was a second
			// copy of the flat lookup, so the project's own region block — which
			// is nested and optional — failed every compile that omitted it.
			val, held := attrString(o, a)
			if !held {
				continue
			}
			store := ct.Store().Validators()
			vals := append(store.Get(), engine.NewNextValidation(ve.Key, val, ve.Validator, map[string]any{}))
			if _, err := store.Set(vals); err != nil {
				return fmt.Errorf("storing validations failed with %w", err)
			}
		}
		if b, _ := a.Meta["wireDrop"].(bool); b {
			o.Delete(a.Name)
		}
	}
	return nil
}

func hasAttr(n *engine.Node, name string) bool {
	for _, a := range n.Attributes {
		if a.Name == name {
			return true
		}
	}
	return false
}
