package interp

import (
	"fmt"
	"slices"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp/utils"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

// decompileOrder is the groups to run the per-resource inverse over: every top-level
// group that declares a resource descriptor, in the order the vocabulary declares them.
//
// It was a HARDCODED LIST of v1's nine group names, taken verbatim from the
// hand-written pass it replaced. That made the decompiler v1-shaped rather than
// schema-driven, and the consequence was not cosmetic: for any other vocabulary the
// list matched nothing, so no resource was ever inverse-processed, `id` was never
// restored from the key, and decompiling failed on the first resource with "required
// attribute 'id' is missing". A v2 project could be compiled and not written back.
//
// Order does not affect the result — each inverse touches only its own group and
// disjoint keys — which is what makes reading it off the schema safe for v1 too.
func decompileOrder(root *engine.Node) []string {
	var out []string
	for _, g := range root.Children {
		key, _ := g.Match.(string)
		if key == "" || len(g.Children) == 0 {
			continue
		}
		if _, ok := g.Children[0].Meta["resource"].([4]string); !ok {
			continue // the scope container, and leaf maps like v1's clouds
		}
		out = append(out, key)
	}
	return out
}

// NewDecompileDriver returns the mechanical inverse of CompileDriver+ResolveRefs,
// driven by the SAME DSL node tree. It replaces the whole id->name ref resolution and
// per-resource wire-projection-inverse layers with one generic transform. The chroot
// unwrap and engine.Dump (the nested-YAML writer) stay: this driver only rewrites the
// flat compiled keys back to their authored shape, exactly as the old passes did.
func NewDecompileDriver(root *engine.Node) transform.Transformer[object.Refrence] {
	return &decompileDriver{root: root}
}

type decompileDriver struct {
	root *engine.Node
}

func (d *decompileDriver) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	// The pipe mirrors decompile/pass2.Pipe() + decompile/pass3.Pipe() exactly:
	//   - ref inversion first, over the whole object, BEFORE any id->name key swap
	//     renames the ref-target groups out from under the id->name map; then
	//   - the per-resource inverse in the fixed pass3 order, applications first so
	//     the resource walks that follow can iterate apps by whatever key they now
	//     carry (the walk is key-agnostic).
	container := containerKey(d.root)
	idField, nameField := engine.IdentityOf(d.root)
	pipe := []transform.Transformer[object.Refrence]{
		utils.Global(container, &decompileRefs{groups: d.root.Children, nameField: nameField}),
		&decompileApps{container: container, idField: idField, nameField: nameField},
	}
	for _, key := range decompileOrder(d.root) {
		g := findGroup(d.root, key)
		if g == nil || len(g.Children) == 0 {
			continue
		}
		pipe = append(pipe, utils.Global(container, &decompileResource{
			groupKey: key, iter: g.Children[0], idField: idField, nameField: nameField,
		}))
	}
	return transform.Pipe(ct, o, pipe...)
}

// decompileRefs is the inverse of ResolveRefs: for every Ref(...)-annotated
// attribute it maps the compiled id(s) back to the referenced instance's authored
// name(s). Unlike the forward resolver (which reads the compile-time store index),
// it rebuilds an id->name map straight off the target group's objects — the store
// is empty on decompile — with the same app-local-then-project-root scope the old
// decompile/pass2/common.go used.
type decompileRefs struct {
	groups []*engine.Node
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
		// A NESTED GROUP'S ENTRIES CARRY REFERENCES TOO — a variables entry naming the
		// secret it takes its value from — and they compile to ids like any other. The
		// inversion walked the resource's own attributes and stopped, so a project
		// decompiled with `secret: secret/Qm…` in it: a document that reads like config
		// and does not compile, because a name is what a reference resolves FROM.
		// Only a resource that declares NESTED DATA groups — `Nested(...)` — has entries
		// of its own to invert. A container's children are SCOPES, and the scope walk
		// already visits those: descending into them here inverted every id twice, and
		// the second pass looked up a name as though it were an id and failed.
		nested := map[string][]refAttr{}
		for _, child := range iter.Children {
			if !iter.Nested {
				break
			}
			key, _ := child.Match.(string)
			if key == "" || len(child.Children) == 0 {
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

// buildIdToNameMap reproduces decompile/pass2/common.go: it maps every compiled
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
// exact inverse of resolveRefAttr:
//   - no Prefix (functions/websites `domains`): a []string of ids -> []string of names.
//   - with Prefix (functions/smartops `source`, "libraries/"): only a value under
//     the prefix is a ref — strip it, map id->name, re-prefix; anything else (".")
//     is left untouched.
//
// A missing/nil value is a no-op, matching decompile/pass2.
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

	if ra.spec.Prefix == "" {
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

	if ra.spec.FlatValue {
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("%s is not a string", ra.name())
		}
		if !strings.HasPrefix(s, ra.spec.Prefix) || len(s) <= len(ra.spec.Prefix) {
			return nil
		}
		name, ok := m[strings.TrimPrefix(s, ra.spec.Prefix)]
		if !ok {
			return fmt.Errorf("%s ID %s not found", ra.spec.Group, strings.TrimPrefix(s, ra.spec.Prefix))
		}
		holder.Set(key, object.Refrence(ra.spec.Prefix+name))
		return nil
	}

	// A resolved reference is `{<kind>: <id>}` — two levels, because a kind and an id
	// are two facts. Authored it is one string, `<kind>/<name>`, which is the address a
	// person writes; that is what goes back.
	nested, nErr := holder.Child(key).Object()
	if nErr != nil {
		return nil
	}
	id, gErr := nested.GetString(ra.spec.Group)
	if gErr != nil || id == "" {
		return nil
	}
	name, ok := m[id]
	if !ok {
		return fmt.Errorf("%s ID %s not found", ra.spec.Group, id)
	}
	holder.Delete(key)
	holder.Set(key, object.Refrence(ra.spec.Prefix+name))
	return nil
}

// decompileApps inverts the CompileDriver's container id-promotion: it swaps each
// application's compiled id key back to its authored name (restoring the id field),
// mirroring decompile/pass3/applications.go. It does NOT recurse — the per-resource
// walks that follow handle each app's nested resources via utils.Global.
type decompileApps struct {
	container          string
	idField, nameField string
}

func (a *decompileApps) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	if a.container == "" {
		return o, nil
	}
	apps, err := o.Child(a.container).Object()
	if err == object.ErrNotExist {
		return o, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetching %s failed with %w", a.container, err)
	}
	for _, id := range apps.Children() {
		sel := apps.Child(id)
		// Innermost first: a scope may hold scopes, and renaming this one before its
		// children would move the subtree out from under the walk.
		if inner, err := sel.Object(); err == nil {
			if _, err := a.Process(ct, inner); err != nil {
				return nil, fmt.Errorf("renaming inside %s failed with %w", id, err)
			}
		}
		if _, err := utils.RenameByName(sel, a.idField, a.nameField); err != nil {
			return nil, fmt.Errorf("renaming scope %s failed with %w", id, err)
		}
	}
	return o, nil
}

// decompileResource is the generic form of every decompile/pass3/<resource>.go: it
// walks a resource group at one scope and runs the per-instance inverse
// projections. Wrapped in utils.Global so it covers the project scope and every
// application scope, exactly like the passes it replaces.
type decompileResource struct {
	groupKey           string
	iter               *engine.Node
	idField, nameField string
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
		sel := groupObj.Child(key)
		// A NESTED GROUP's entries carry the same wire projections the resource's own
		// attributes do — an access entry's `timeout` is nanoseconds on the wire and a
		// human string in the document — and nothing was putting them back. The forward
		// pass walks them (driver.go's iter.Nested branch); the inverse has to walk them
		// too, or a resource with an access block cannot be written out at all.
		//
		// Before the instance inverse, because that one renames the instance by name and
		// the nested walk wants the object it is holding.
		if ri.iter.Nested {
			inst, err := sel.Object()
			if err != nil {
				return nil, fmt.Errorf("fetching %s %s failed with %w", ri.groupKey, key, err)
			}
			if err := applyNestedInverse(inst, ri.iter.Children); err != nil {
				return nil, fmt.Errorf("%s %s: %w", ri.groupKey, key, err)
			}
		}
		if err := applyInstanceInverse(sel, ri.iter, ri.idField, ri.nameField); err != nil {
			return nil, fmt.Errorf("%s %s: %w", ri.groupKey, key, err)
		}
	}
	return o, nil
}

// applyNestedInverse walks a resource's own child groups — its access blocks, ports,
// variables — and undoes the wire projections on each entry. An entry is not a resource:
// it is keyed by a name the author chose and never had an id, so there is no key swap to
// invert, only the scalar and rename work.
func applyNestedInverse(inst object.Object[object.Refrence], groups []*engine.Node) error {
	for _, g := range groups {
		key, _ := g.Match.(string)
		if key == "" || len(g.Children) == 0 {
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
			sel := groupObj.Child(name)
			for _, a := range iter.Attributes {
				if sc, ok := a.Meta["scalar"].(engine.ScalarSpec); ok {
					holder, field := holderOf(sel, a)
					if err := sc.Format(holder, field); err != nil {
						return fmt.Errorf("%s/%s: %w", key, name, err)
					}
				}
				if tag, ok := tagOf(a); ok {
					sel.Move(tag, a.Name)
				}
			}
		}
	}
	return nil
}

// applyInstanceInverse runs the per-resource-instance wire projections in reverse:
// DerivedBool delete, EnumBool restore, OnlyWhen-gated rename-back, plain Tag
// rename-back, scalar reformat, then the id->name key swap. Each stage is driven by
// attribute annotations; a resource missing an annotation no-ops that stage (this
// is why smartops needs no special-casing — it has no trigger/type attrs, so every
// rename-back and bool op naturally does nothing, absorbing pass3's dead moves).
// The stages touch disjoint keys, so this order (which differs from pass3's textual
// order) yields the identical result.
func applyInstanceInverse(sel object.Selector[object.Refrence], iter *engine.Node, idField, nameField string) error {
	attrs := iter.Attributes

	// 1. DerivedBool: drop the synthesized bool (functions `secure`); the source
	//    (`type`) survived compile, so nothing is reconstructed.
	for _, a := range attrs {
		if db, ok := a.Meta["derivedBool"].(engine.DerivedBoolSpec); ok && db.GoName != "" {
			sel.Delete(strings.ToLower(db.GoName))
		}
	}

	// 2. EnumBool: restore the source enum, preferring a preserved source key.
	for _, a := range attrs {
		if eb, ok := a.Meta["enumBool"].(engine.EnumBoolSpec); ok && eb.GoName != "" {
			restoreEnumBool(sel, a.Name, eb)
		}
	}

	// 3. OnlyWhen-gated rename-back (functions `service` -> `p2p-protocol` when
	//    type == "p2p").
	for _, a := range attrs {
		if ow, ok := a.Meta["onlyWhen"].(engine.OnlyWhenSpec); ok {
			applyOnlyWhenInverse(sel, a, ow)
		}
	}

	// 4. plain Tag rename-back: move each tagged (non-OnlyWhen) key back to its
	//    authored name. A no-op when the compiled key is absent.
	for _, a := range attrs {
		tag, ok := tagOf(a)
		if !ok {
			continue
		}
		if _, gated := a.Meta["onlyWhen"]; gated {
			continue
		}
		_ = sel.Move(tag, a.Name)
	}

	// 5. scalar reformat: ns -> "20s", bytes -> "32GB" — the inverse codec each
	//    scalar term carries, so there is no per-scalar switch to keep in sync.
	for _, a := range attrs {
		if sc, ok := a.Meta["scalar"].(engine.ScalarSpec); ok {
			holder, field := holderOf(sel, a)
			if err := sc.Format(holder, field); err != nil {
				return err
			}
		}
	}

	// 6. id->name key swap (inverse of RenameById): every keyed resource — i.e. every
	//    DefineIter carrying the identity attribute the schema names.
	if idField != "" && hasAttr(iter, idField) {
		if _, err := utils.RenameByName(sel, idField, nameField); err != nil {
			return fmt.Errorf("renaming by name failed with %w", err)
		}
	}

	return nil
}

// restoreEnumBool inverts projectEnumBool with preserved-key-wins: if the source
// wire key survived compile (a database's authored `subnet`, which is not in
// DropWhen) it is kept as-is and only the synthesized bool is dropped; otherwise the
// source key is rebuilt from DecompileAs, indexed by the bool. This is the fix for
// the latent decompile/pass3/databases.go bug that unconditionally overwrote a
// preserved `subnet` with `all`/`host`.
//
// DecompileAs is [valueWhenTrue, valueWhenFalse] as written in the DSL (databases
// Local ["host","all"], storages Public ["all","subnet"]) — consistent with the
// frozen forward mapping (host -> Local=true) and the old decompiler's
// true->first-branch behaviour.
func restoreEnumBool(sel object.Selector[object.Refrence], name string, eb engine.EnumBoolSpec) {
	boolKey := strings.ToLower(eb.GoName)

	// preserved-key-wins: the authored source value is still on the wire.
	if _, err := sel.GetString(name); err == nil {
		sel.Delete(boolKey)
		return
	}

	b, err := sel.GetBool(boolKey)
	if err != nil {
		return
	}
	sel.Delete(boolKey)

	val := eb.DecompileAs[1]
	if b {
		val = eb.DecompileAs[0]
	}
	_ = sel.Set(name, val)
}

// applyOnlyWhenInverse inverts applyOnlyWhen: when the gate attribute's value is one
// of Vals, move the compiled Tag key back to the attribute's authored name
// (functions `service` -> `p2p-protocol` only when type == "p2p"). A gate miss (or
// an absent key) is a no-op, matching decompile/pass3/functions.go.
func applyOnlyWhenInverse(sel object.Selector[object.Refrence], a *engine.Attribute, ow engine.OnlyWhenSpec) {
	g, ok := valueOf(sel, ow.Attr)
	if !ok || !slices.Contains(ow.Vals, g) {
		return
	}
	if tag, ok := tagOf(a); ok {
		_ = sel.Move(tag, a.Name)
	}
}
