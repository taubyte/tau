package interp

import (
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp/utils"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

// ResolveRefs is the reference-resolution transform. For every attribute annotated
// with Ref(...), it reads the compiled value and resolves the referenced name(s) to the target group's compiled id(s) against
// the name->id index the CompileDriver already populated.
//
// It is wrapped in utils.Global so it walks the project root, then each
// application, which is what makes utils.ResolveNamesToId
// key the store identically (app -> project -> root scope walk). Resolution runs
// after the CompileDriver, so the index is complete before any ref is read.
func ResolveRefs(root *engine.Node) transform.Transformer[object.Refrence] {
	idField, nameField := engine.IdentityOf(root)
	return utils.Global(ContainerKeys(root), &resolveRefs{groups: root.Children, idField: idField, nameField: nameField})
}

type resolveRefs struct {
	groups  []*engine.Node
	idField string
	// what the schema calls a resource's name: a reference is resolved through the
	// name->id index, and that index is keyed by it
	nameField string
}

// refAttr is a ref-carrying attribute paired with the compiled key it lives under.
type refAttr struct {
	// segments is the compiled key, one level of the object per entry.
	segments []string
	// list says the field holds MANY names rather than one — which is the attribute's
	// own type, not a second thing to declare.
	list bool
	spec engine.RefSpec
}

// name is the key as a person reads it, for an error message.
func (r refAttr) name() string { return strings.Join(r.segments, "/") }

// refHolder descends to the object a ref's value lives in, and the key it lives at.
//
// It never CREATES the levels on the way: a resource that did not write the block has no
// reference to resolve, and creating one would file an empty block on every resource of
// the kind — which then reads as "declared and empty" to everything downstream.
func refHolder(sel object.Selector[object.Refrence], segs []string) (object.Object[object.Refrence], string, error) {
	o, err := sel.Object()
	if err != nil {
		return nil, "", err
	}
	last := len(segs) - 1
	if last == 0 {
		return o, segs[0], nil
	}
	parent, err := o.Fetch(segs[:last]...)
	if err != nil {
		return nil, "", err
	}
	return parent, segs[last], nil
}

func (r *resolveRefs) Process(ct transform.Context[object.Refrence], scope object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	if err := resolveGroups(ct, scope, r.groups, r.idField, r.nameField); err != nil {
		return nil, err
	}
	return scope, nil
}

// resolveGroups resolves every Ref-annotated attribute of each group at this scope,
// then recurses into a nested resource's own groups with the SAME context — a nested
// group is data inside the resource, not a scope, so a reference written there
// resolves against the index of the scope the resource was authored in.
func resolveGroups(ct transform.Context[object.Refrence], scope object.Object[object.Refrence], groups []*engine.Node, idField, nameField string) error {
	for _, g := range groups {
		groupKey, _ := g.Match.(string)
		if len(g.Children) == 0 {
			continue
		}
		iter := g.Children[0]
		refs := collectRefAttrs(iter.Attributes)
		if len(refs) == 0 && !iter.Nested {
			continue
		}

		groupObj, err := scope.Child(groupKey).Object()
		if err == object.ErrNotExist {
			continue
		}
		if err != nil {
			return fmt.Errorf("fetching %s failed with %w", groupKey, err)
		}

		for _, instName := range groupObj.Children() {
			if !keyedByIdentity(iter, idField, instName) {
				continue
			}
			sel := groupObj.Child(instName)
			for _, ra := range refs {
				if err := resolveRefAttr(ct, sel, ra, nameField); err != nil {
					return err
				}
			}
			// Only the groups the kind declares for ITSELF. A container's resource groups
			// are scopes and the scope walk visits them; descending into them here
			// resolved every reference in them twice, and the second pass read a name it
			// had already turned into an id.
			own := ownGroups(iter)
			if len(own) == 0 {
				continue
			}
			instObj, err := sel.Object()
			if err != nil {
				return fmt.Errorf("fetching %s/%s failed with %w", groupKey, instName, err)
			}
			if err := resolveGroups(ct, instObj, own, idField, nameField); err != nil {
				return err
			}
		}
	}
	return nil
}

// collectRefAttrs picks the Ref-annotated attributes off an iterator's attribute
// list, resolving each to the compiled wire key it will be found under.
func collectRefAttrs(attrs []*engine.Attribute) []refAttr {
	var out []refAttr
	for _, a := range attrs {
		spec, ok := a.Meta["ref"].(engine.RefSpec)
		if !ok {
			continue
		}
		// A ByName reference is a reference for everyone who READS the schema and not
		// for the resolver: the authored name is what the vocabulary keeps, and
		// resolving it would rewrite a value the audit and the runtime expect to be a
		// name.
		if spec.ByName {
			continue
		}
		out = append(out, refAttr{segments: engine.CompiledSegments(a), list: a.Type == engine.TypeStringSlice, spec: spec})
	}
	return out
}

// resolveRefAttr resolves a single ref attribute on one resource instance: a list
// of names to a list of ids, or one name to one id. A missing/nil value is a no-op.
func resolveRefAttr(ct transform.Context[object.Refrence], sel object.Selector[object.Refrence], ra refAttr, nameField string) error {
	holder, key, err := refHolder(sel, ra.segments)
	if err != nil {
		return nil
	}
	val := holder.Get(key)
	if val == nil {
		return nil
	}

	if ra.list {
		names, ok := val.([]string)
		if !ok {
			return fmt.Errorf("%s is not a []string", ra.name())
		}
		ids, err := utils.ResolveNamesToId(ct, ra.spec.Group, names, nameField)
		if err != nil {
			return fmt.Errorf("resolving %s names to IDs failed with %w", ra.spec.Group, err)
		}
		holder.Set(key, ids)
		return nil
	}

	name, ok := val.(string)
	if !ok {
		return fmt.Errorf("%s is not a string", ra.name())
	}
	if name == "" {
		return nil
	}
	ids, err := utils.ResolveNamesToId(ct, ra.spec.Group, []string{name}, nameField)
	if err != nil {
		return fmt.Errorf("resolving %s names to IDs failed with %w", ra.spec.Group, err)
	}
	if len(ids) == 0 {
		return fmt.Errorf("resolving %s names to IDs failed", ra.spec.Group)
	}

	// A RESOLVED REFERENCE IS AN ID, and nothing else. Ids are unique, so which KIND one
	// belongs to adds no information — it is already on the Ref — and a kind written
	// into the value is that same fact free to disagree with it. Which NAMESPACE the
	// name was looked up in is authored beside the name, not packed into it.
	holder.Set(key, object.Refrence(ids[0]))
	return nil
}
