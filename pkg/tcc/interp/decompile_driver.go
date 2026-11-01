package interp

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp/utils"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

// decompileOrder is the groups to run the per-resource inverse over: every top-level
// group that declares a resource descriptor, in the order the vocabulary declares them.
//
// Order does not affect the result — every nested walk runs before any rename, and
// each inverse touches only its own group and disjoint keys.
func decompileOrder(root *engine.Node) []string {
	var out []string
	for _, g := range root.Children {
		key, _ := g.Match.(string)
		if key == "" || len(g.Children) == 0 {
			continue
		}
		if _, ok := g.Children[0].Meta["resource"].([4]string); !ok {
			continue // the scope container
		}
		out = append(out, key)
	}
	return out
}

// NewDecompileDriver is the inverse of CompileDriver and ResolveRefs, driven by
// the same DSL tree: it rewrites the flat compiled keys back to their authored
// shape. The chroot unwrap and engine.Dump stay separate.
func NewDecompileDriver(root *engine.Node) transform.Transformer[object.Refrence] {
	return &decompileDriver{root: root}
}

type decompileDriver struct {
	root *engine.Node
}

func (d *decompileDriver) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	// Refs are inverted first, over the whole object, before any id->name key swap
	// renames their target groups; then the per-resource inverse, containers first.
	containers := ContainerKeys(d.root)
	idField, nameField := engine.IdentityOf(d.root)
	resourceContainers := map[string]bool{}
	for _, key := range containers {
		if group := findGroup(d.root, key); group != nil && len(group.Children) > 0 {
			_, resourceContainers[key] = group.Children[0].Meta["resource"].([4]string)
		}
	}
	pipe := []transform.Transformer[object.Refrence]{
		utils.Global(containers, &decompileRefs{groups: d.root.Children, idField: idField, nameField: nameField}),
		&decompileApps{containers: containers, resources: resourceContainers, idField: idField, nameField: nameField},
	}
	// Every nested walk runs before any instance is renamed: a held resource is told
	// from a declared entry by its id key, which its own group's rename takes away.
	for _, nestedOnly := range []bool{true, false} {
		for _, key := range decompileOrder(d.root) {
			g := findGroup(d.root, key)
			if g == nil || len(g.Children) == 0 {
				continue
			}
			pipe = append(pipe, utils.Global(containers, &decompileResource{
				groupKey: key, iter: g.Children[0], groups: d.root.Children,
				idField: idField, nameField: nameField, nestedOnly: nestedOnly,
			}))
		}
	}
	return transform.Pipe(ct, o, pipe...)
}

// decompileRefs is the inverse of ResolveRefs: compiled ids back to authored
// names, from an id->name map rebuilt off each target group (the store is empty
// on decompile), scope first, then the project root.
type decompileRefs struct {
	groups  []*engine.Node
	idField string
	// nameField is what this vocabulary calls the name of a resource, from the schema:
	// inverting a reference means finding the resource an id belongs to and writing what
	// it is CALLED, and the word for that is not this package's.
	nameField string
}

func (r *decompileRefs) Process(ct transform.Context[object.Refrence], scope object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	for _, g := range r.groups {
		groupKey, _ := g.Match.(string)
		if len(g.Children) == 0 {
			continue
		}
		iter := g.Children[0]
		refs := collectRefAttrs(iter.Attributes)
		// A nested group's entries carry references too. Only children marked Nested are
		// the resource's own data; a container's other children are scopes the scope
		// walk visits, and inverting them here too would invert every id twice.
		nested := map[string][]refAttr{}
		for _, child := range iter.Children {
			if !iter.Nested {
				break
			}
			key, _ := child.Match.(string)
			if key == "" || len(child.Children) == 0 || !child.Nested {
				continue
			}
			if inner := collectRefAttrs(child.Children[0].Attributes); len(inner) > 0 {
				nested[key] = inner
			}
		}
		if len(refs) == 0 && len(nested) == 0 {
			continue
		}

		groupObj, err := scope.Child(groupKey).Object()
		if err == object.ErrNotExist {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("fetching %s failed with %w", groupKey, err)
		}

		// id->name maps are per ref-target group; build each at most once per scope.
		maps := map[string]map[string]string{}
		getMap := func(target string) map[string]string {
			if m, ok := maps[target]; ok {
				return m
			}
			m := buildIdToNameMap(ct, scope, target, r.nameField)
			maps[target] = m
			return m
		}

		for _, instName := range groupObj.Children() {
			if !keyedByIdentity(iter, r.idField, instName) {
				continue
			}
			sel := groupObj.Child(instName)
			for _, ra := range refs {
				if err := invertRefAttr(sel, ra, getMap); err != nil {
					return nil, err
				}
			}
			if len(nested) == 0 {
				continue
			}
			inst, err := sel.Object()
			if err != nil {
				return nil, fmt.Errorf("fetching %s/%s failed with %w", groupKey, instName, err)
			}
			for key, inner := range nested {
				entries, err := inst.Child(key).Object()
				if err == object.ErrNotExist {
					continue
				}
				if err != nil {
					return nil, fmt.Errorf("fetching %s/%s/%s failed with %w", groupKey, instName, key, err)
				}
				for _, entryName := range entries.Children() {
					// Under a container that is also a resource this key holds both: skip
					// the ones that are resources it holds, which the scope walk inverts.
					if heldUnder(r.groups, r.idField, key, entryName) {
						continue
					}
					entry := entries.Child(entryName)
					for _, ra := range inner {
						if err := invertRefAttr(entry, ra, getMap); err != nil {
							return nil, err
						}
					}
				}
			}
		}
	}
	return scope, nil
}

// buildIdToNameMap maps every compiled
// resource id in `group` to its authored name, checking the current scope first
// and — when in an application scope (ctx path depth > 1) — falling back to the
// project-root group for globals the app references (local names take precedence).
func buildIdToNameMap(ct transform.Context[object.Refrence], o object.Object[object.Refrence], group, nameField string) map[string]string {
	idToName := map[string]string{}

	if groupObj, err := o.Child(group).Object(); err == nil {
		for _, id := range groupObj.Children() {
			name, err := groupObj.Child(id).GetString(nameField)
			if err != nil {
				continue
			}
			idToName[id] = name
		}
	}

	ctp := ct.Path()
	if len(ctp) > 1 {
		if root, ok := ctp[0].(object.Object[object.Refrence]); ok {
			if rootGroupObj, err := root.Child(group).Object(); err == nil {
				for _, id := range rootGroupObj.Children() {
					if _, exists := idToName[id]; exists {
						continue
					}
					if name, err := rootGroupObj.Child(id).GetString(nameField); err == nil {
						idToName[id] = name
					}
				}
			}
		}
	}

	return idToName
}

// invertRefAttr maps a single ref attribute's compiled id(s) back to name(s) — the
// exact inverse of resolveRefAttr. A missing/nil value is a no-op.
func invertRefAttr(sel object.Selector[object.Refrence], ra refAttr, getMap func(string) map[string]string) error {
	holder, key, err := refHolder(sel, ra.segments)
	var val any
	if err == nil {
		val = holder.Get(key)
	}
	if err != nil {
		return nil
	}
	m := getMap(ra.spec.Group)

	if ra.list {
		if val == nil {
			return nil
		}
		ids, ok := val.([]string)
		if !ok {
			return fmt.Errorf("%s is not a []string", ra.name())
		}
		names := make([]string, 0, len(ids))
		for _, id := range ids {
			name, ok := m[id]
			if !ok {
				return fmt.Errorf("%s ID %s not found", ra.spec.Group, id)
			}
			names = append(names, name)
		}
		holder.Set(key, names)
		return nil
	}

	// A resolved reference is the id alone; authored it is the NAME, in a block that
	// says which namespace beside it. The namespace is an attribute of its own and
	// needs no inverting — only the name was rewritten on the way in.
	id, isStr := val.(string)
	if !isStr || id == "" {
		return nil
	}
	name, ok := m[id]
	if !ok {
		return fmt.Errorf("%s ID %s not found", ra.spec.Group, id)
	}
	holder.Set(key, object.Refrence(name))
	return nil
}

// decompileApps swaps each container instance's compiled id key back to its
// name, restoring the id field. It does not recurse; the resource walks handle
// nested resources.
type decompileApps struct {
	containers         []string
	resources          map[string]bool
	idField, nameField string
}

func (a *decompileApps) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	for _, container := range a.containers {
		apps, err := o.Child(container).Object()
		if err == object.ErrNotExist {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("fetching %s failed with %w", container, err)
		}
		for _, id := range apps.Children() {
			sel := apps.Child(id)
			// Innermost first: a scope may hold scopes — of either kind — and renaming
			// this one before its children would move the subtree out from under the walk.
			if inner, err := sel.Object(); err == nil {
				if _, err := a.Process(ct, inner); err != nil {
					return nil, fmt.Errorf("renaming inside %s failed with %w", id, err)
				}
			}
			if a.resources[container] {
				continue // it is also a resource; decompileResource performs this inverse
			}
			if _, err := utils.RenameByName(sel, a.idField, a.nameField); err != nil {
				return nil, fmt.Errorf("renaming scope %s failed with %w", id, err)
			}
		}
	}
	return o, nil
}

// decompileResource runs the per-instance inverse projections over one resource
// group, at every scope via utils.Global.
type decompileResource struct {
	groupKey string
	iter     *engine.Node
	// groups is the vocabulary's top-level groups, for the one question a nested walk
	// has to ask under a container that is also a resource: is this entry at this key an
	// entry the kind declares, or a resource the container holds?
	groups             []*engine.Node
	idField, nameField string
	// nestedOnly runs the nested walk alone; otherwise the instance inverse alone.
	nestedOnly bool
}

func (ri *decompileResource) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	groupObj, err := o.Child(ri.groupKey).Object()
	if err == object.ErrNotExist {
		return o, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetching %s failed with %w", ri.groupKey, err)
	}
	for _, key := range groupObj.Children() {
		if !keyedByIdentity(ri.iter, ri.idField, key) {
			continue
		}
		sel := groupObj.Child(key)
		// A nested group's entries carry the same wire projections as the resource's own
		// attributes (an access entry's timeout), so the inverse walks them too.
		if !ri.nestedOnly {
			if err := applyInstanceInverse(sel, ri.iter, ri.idField, ri.nameField); err != nil {
				return nil, fmt.Errorf("%s %s: %w", ri.groupKey, key, err)
			}
			continue
		}
		if ri.iter.Nested {
			inst, err := sel.Object()
			if err != nil {
				return nil, fmt.Errorf("fetching %s %s failed with %w", ri.groupKey, key, err)
			}
			if err := applyNestedInverse(inst, ri.iter.Children, ri.groups, ri.idField); err != nil {
				return nil, fmt.Errorf("%s %s: %w", ri.groupKey, key, err)
			}
		}
	}
	return o, nil
}

// applyNestedInverse undoes the wire projections on a resource's own child
// groups' entries, which have no id to swap. Only children marked Nested: a
// container's held resources are walked by the scope walk, and inverting them
// here too would run each scalar's codec twice.
func applyNestedInverse(inst object.Object[object.Refrence], children, groups []*engine.Node, idField string) error {
	for _, g := range children {
		key, _ := g.Match.(string)
		if key == "" || len(g.Children) == 0 || !g.Nested {
			continue
		}
		// A list compiles to one ordered VALUE rather than to children, so there is
		// nothing to walk and nothing was projected: dump writes it back as it stands.
		if g.Sequence {
			continue
		}
		groupObj, err := inst.Child(key).Object()
		if err == object.ErrNotExist {
			continue
		}
		if err != nil {
			return fmt.Errorf("fetching %s failed with %w", key, err)
		}
		iter := g.Children[0]
		for _, name := range groupObj.Children() {
			// One key, two things: skip the resources the container holds here, which
			// the scope walk inverts as the resources they are.
			if heldUnder(groups, idField, key, name) {
				continue
			}
			sel := groupObj.Child(name)
			for _, a := range iter.Attributes {
				if sc, ok := a.Meta["scalar"].(engine.ScalarSpec); ok {
					holder, field := holderOf(sel, a)
					if err := sc.Format(holder, field); err != nil {
						return fmt.Errorf("%s/%s: %w", key, name, err)
					}
				}
			}
		}
	}
	return nil
}

// applyInstanceInverse runs the per-resource-instance projections in reverse:
// inlined-file drop, scalar reformat, then the id->name key swap.
func applyInstanceInverse(sel object.Selector[object.Refrence], iter *engine.Node, idField, nameField string) error {
	attrs := iter.Attributes

	// 0. Drop the key a pointed-at file was read into; the decompiler writes the
	// file itself back (interp.InlinedFiles).
	for _, a := range attrs {
		if into := engine.InlinedInto(a); into != "" {
			sel.Delete(into)
		}
	}

	// 1. scalar reformat: ns -> "20s", bytes -> "32GB" — the inverse codec each
	//    scalar term carries, so there is no per-scalar switch to keep in sync.
	for _, a := range attrs {
		if sc, ok := a.Meta["scalar"].(engine.ScalarSpec); ok {
			holder, field := holderOf(sel, a)
			if err := sc.Format(holder, field); err != nil {
				return err
			}
		}
	}

	// 2. id->name key swap (inverse of RenameById): every keyed resource — i.e. every
	//    DefineIter carrying the identity attribute the schema names.
	if idField != "" && hasAttr(iter, idField) {
		if _, err := utils.RenameByName(sel, idField, nameField); err != nil {
			return fmt.Errorf("renaming by name failed with %w", err)
		}
	}

	return nil
}
