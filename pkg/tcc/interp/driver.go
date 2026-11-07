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

// newCompileDriver builds a CompileDriver from the schema root node, whose
// attributes are the project scope's and whose children are the groups.
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

// processGroups runs each group at one scope. In a hybrid container's scope its
// own entries and its held resources share a key, so they are told apart by id
// before anything runs: promoting a held resource deletes its id.
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

// ownEntries is every "<group>/<entry>" in scope with no id: the entries a
// hybrid container declares for itself.
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
	if engine.IsBlock(g) {
		return d.processBlock(ct, scope, g)
	}
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

	// A container group: each instance is id-promoted with no index, then its nested
	// resource groups are processed in its scope.
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

	// A resource group: per-instance projections, id promotion, then the name->id
	// index. The identity attribute is the schema's (engine.Identity).
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

		// A nested resource's own groups are processed in the instance's object but the
		// enclosing scope's context, not forked: a nested group is data of the instance,
		// and names in it resolve where the resource was authored.
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

// processBlock runs a block's own projections and then its groups, in the context of
// the resource holding it: a block is data of the resource, like its nested groups.
func (d *CompileDriver) processBlock(ct transform.Context[object.Refrence], scope object.Object[object.Refrence], b *engine.Node) error {
	key := b.Match.(string)
	sel := scope.Child(key)
	blockObj, err := sel.Object()
	if err == object.ErrNotExist {
		return nil
	}
	if err != nil {
		return fmt.Errorf("fetching %s failed with %w", key, err)
	}
	if err := applyInstanceOps(sel, b.Attributes); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return d.processGroups(ct, blockObj, b.Children, false)
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
			// The same read emitAttrValidations does, by the path the key is, one value or
			// a list of them; absent emits nothing.
			for _, val := range attrValues(o, a) {
				store := ct.Store().Validators()
				vals := append(store.Get(), engine.NewNextValidation(ve.Key, val, ve.Validator, map[string]any{}))
				if _, err := store.Set(vals); err != nil {
					return fmt.Errorf("storing validations failed with %w", err)
				}
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
