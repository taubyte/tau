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
	return utils.Global(&resolveRefs{groups: root.Children})
}

type resolveRefs struct {
	groups []*engine.Node
}

// refAttr is a ref-carrying attribute paired with the compiled wire key it lives
// under (Tag ?? Name — e.g. functions `http-domains` compiles to `domains`).
type refAttr struct {
	wireKey string
	spec    engine.RefSpec
}

func (r *resolveRefs) Process(ct transform.Context[object.Refrence], scope object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	for _, g := range r.groups {
		groupKey, _ := g.Match.(string)
		if len(g.Children) == 0 {
			continue
		}
		refs := collectRefAttrs(g.Children[0].Attributes)
		if len(refs) == 0 {
			continue
		}

		groupObj, err := scope.Child(groupKey).Object()
		if err == object.ErrNotExist {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("fetching %s failed with %w", groupKey, err)
		}

		for _, instName := range groupObj.Children() {
			sel := groupObj.Child(instName)
			for _, ra := range refs {
				if err := resolveRefAttr(ct, sel, ra); err != nil {
					return nil, err
				}
			}
		}
	}
	return scope, nil
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
		key := a.Name
		if t, ok := tagOf(a); ok {
			key = t
		}
		out = append(out, refAttr{wireKey: key, spec: spec})
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
func resolveRefAttr(ct transform.Context[object.Refrence], sel object.Selector[object.Refrence], ra refAttr) error {
	val, err := sel.Get(ra.wireKey)
	if err != nil || val == nil {
		return nil
	}

	if ra.spec.Prefix == "" {
		names, ok := val.([]string)
		if !ok {
			return fmt.Errorf("%s is not a []string", ra.wireKey)
		}
		ids, err := utils.ResolveNamesToId(ct, ra.spec.Group, names)
		if err != nil {
			return fmt.Errorf("resolving %s names to IDs failed with %w", ra.spec.Group, err)
		}
		return sel.Set(ra.wireKey, ids)
	}

	s, ok := val.(string)
	if !ok {
		return fmt.Errorf("%s is not a string", ra.wireKey)
	}
	if !strings.HasPrefix(s, ra.spec.Prefix) {
		return nil
	}
	name := strings.TrimPrefix(s, ra.spec.Prefix)
	ids, err := utils.ResolveNamesToId(ct, ra.spec.Group, []string{name})
	if err != nil {
		return fmt.Errorf("resolving %s names to IDs failed with %w", ra.spec.Group, err)
	}
	if len(ids) == 0 {
		return fmt.Errorf("resolving %s names to IDs failed", ra.spec.Group)
	}
	return sel.Set(ra.wireKey, ra.spec.Prefix+ids[0])
}
