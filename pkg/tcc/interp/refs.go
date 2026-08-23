package interp

import (
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp/utils"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

// ResolveRefs folds the whole pass2 reference-resolution layer into a single
// generic transform. It replaces pass2/{functions,smartops,websites}.go: for
// every attribute annotated with Ref(...), it reads the compiled wire value and
// resolves the referenced name(s) to the target group's compiled id(s) against
// the name->id index the CompileDriver already populated.
//
// It is wrapped in utils.Global so it walks the exact same scope shape pass2 did
// (project root, then each application), which is what makes utils.ResolveNamesToId
// key the store identically (app -> project -> root scope walk). Resolution runs
// after the CompileDriver, so the index is complete before any ref is read.
func ResolveRefs(root *engine.Node) transform.Transformer[object.Refrence] {
	_, nameField := engine.IdentityOf(root)
	return utils.Global(ContainerKeys(root), &resolveRefs{groups: root.Children, nameField: nameField})
}

type resolveRefs struct {
	groups []*engine.Node
	// what the schema calls a resource's name: a reference is resolved through the
	// name->id index, and that index is keyed by it
	nameField string
}

// refAttr is a ref-carrying attribute paired with the compiled wire key it lives
// under (Tag ?? Name — e.g. functions `http-domains` compiles to `domains`).
type refAttr struct {
	// segments is the compiled key, one level of the object per entry.
	segments []string
	// list says the field holds MANY names rather than one — which is the attribute's
	// own type, not a second thing to declare. It used to be inferred from a Prefix
	// being absent, so a single-valued reference could not exist without one.
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
	if err := resolveGroups(ct, scope, r.groups, r.nameField); err != nil {
		return nil, err
	}
	return scope, nil
}

// resolveGroups resolves every Ref-annotated attribute of each group at this scope,
// then recurses into a nested resource's own groups with the SAME context — a nested
// group is data inside the resource, not a scope, so a reference written there
// resolves against the index of the scope the resource was authored in.
func resolveGroups(ct transform.Context[object.Refrence], scope object.Object[object.Refrence], groups []*engine.Node, nameField string) error {
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
			sel := groupObj.Child(instName)
			for _, ra := range refs {
				if err := resolveRefAttr(ct, sel, ra, nameField); err != nil {
					return err
				}
			}
			if !iter.Nested {
				continue
			}
			instObj, err := sel.Object()
			if err != nil {
				return fmt.Errorf("fetching %s/%s failed with %w", groupKey, instName, err)
			}
			if err := resolveGroups(ct, instObj, iter.Children, nameField); err != nil {
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
		// By now the Tag rename has happened, so a tagged attribute is at its tag;
		// everything else is at its key, which may be more than one level down.
		segments := engine.CompiledSegments(a)
		if t, ok := tagOf(a); ok {
			segments = []string{t}
		}
		out = append(out, refAttr{segments: segments, list: a.Type == engine.TypeStringSlice, spec: spec})
	}
	return out
}

// resolveRefAttr resolves a single ref attribute on one resource instance,
// reproducing pass2 exactly:
//   - no Prefix (functions/websites `domains`): the value is a []string of names;
//     resolve each name to its id and write the []string of ids back.
//   - with Prefix (functions/smartops `source`, "libraries/"): only values under
//     the prefix are refs — strip it, resolve the single name, re-prefix. Values
//     without the prefix (e.g. ".") pass through untouched.
//
// A missing/nil value is a no-op (matching pass2's `if err == nil && v != nil`).
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
	if ra.spec.Prefix != "" {
		// v1 keeps the kind in the value, and uses it to tell a reference from a value
		// that is not one — `source: "."` is inline code and resolves to nothing.
		if !strings.HasPrefix(name, ra.spec.Prefix) {
			return nil
		}
		name = strings.TrimPrefix(name, ra.spec.Prefix)
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
	if ra.spec.FlatValue {
		// v1 alone: its consumers read `libraries/<id>` off the compiled object.
		holder.Set(key, object.Refrence(ra.spec.Prefix+ids[0]))
		return nil
	}
	holder.Set(key, object.Refrence(ids[0]))
	return nil
}
