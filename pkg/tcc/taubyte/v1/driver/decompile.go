package driver

import (
	"fmt"
	"slices"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

// decompileOrder is the fixed inverse-processing order, taken verbatim from
// decompile/pass3/pipe.go. Like indexOrder it is NOT the DSL declaration order:
// the per-resource inverses touch only their own group and disjoint keys, so the
// order does not affect the result, but it is kept explicit to mirror the
// hand-written passes it replaces one-for-one.
var decompileOrder = []string{
	"functions",
	"smartops",
	"websites",
	"databases",
	"storages",
	"domains",
	"libraries",
	"messaging",
	"services",
}

// NewDecompileDriver returns the mechanical inverse of CompileDriver+ResolveRefs,
// driven by the SAME DSL node tree. It replaces the whole decompile/pass2 (id->name
// ref resolution) and decompile/pass3 (per-resource wire-projection inverse) layers
// with one generic transform. decompile/pass1 (chroot unwrap) and engine.Dump (the
// nested-YAML writer) stay: this driver only rewrites the flat compiled keys back to
// their authored shape, exactly as the hand-written passes did.
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
	pipe := []transform.Transformer[object.Refrence]{
		utils.Global(&decompileRefs{groups: d.root.Children}),
		&decompileApps{},
	}
	for _, key := range decompileOrder {
		g := findGroup(d.root, key)
		if g == nil || len(g.Children) == 0 {
			continue
		}
		pipe = append(pipe, utils.Global(&decompileResource{groupKey: key, iter: g.Children[0]}))
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
}

func (r *decompileRefs) Process(ct transform.Context[object.Refrence], scope object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
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

		// id->name maps are per ref-target group; build each at most once per scope.
		maps := map[string]map[string]string{}
		getMap := func(target string) map[string]string {
			if m, ok := maps[target]; ok {
				return m
			}
			m := buildIdToNameMap(ct, scope, target)
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
		}
	}
	return scope, nil
}

// buildIdToNameMap reproduces decompile/pass2/common.go: it maps every compiled
// resource id in `group` to its authored name, checking the current scope first
// and — when in an application scope (ctx path depth > 1) — falling back to the
// project-root group for globals the app references (local names take precedence).
func buildIdToNameMap(ct transform.Context[object.Refrence], o object.Object[object.Refrence], group string) map[string]string {
	idToName := map[string]string{}

	if groupObj, err := o.Child(group).Object(); err == nil {
		for _, id := range groupObj.Children() {
			name, err := groupObj.Child(id).GetString("name")
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
					if name, err := rootGroupObj.Child(id).GetString("name"); err == nil {
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
	val, err := sel.Get(ra.wireKey)
	if err != nil || val == nil {
		return nil
	}
	m := getMap(ra.spec.Group)

	if ra.spec.Prefix == "" {
		ids, ok := val.([]string)
		if !ok {
			return fmt.Errorf("%s is not a []string", ra.wireKey)
		}
		names := make([]string, 0, len(ids))
		for _, id := range ids {
			name, ok := m[id]
			if !ok {
				return fmt.Errorf("%s ID %s not found", ra.spec.Group, id)
			}
			names = append(names, name)
		}
		return sel.Set(ra.wireKey, names)
	}

	s, ok := val.(string)
	if !ok {
		return fmt.Errorf("%s is not a string", ra.wireKey)
	}
	if !strings.HasPrefix(s, ra.spec.Prefix) || len(s) <= len(ra.spec.Prefix) {
		return nil
	}
	id := strings.TrimPrefix(s, ra.spec.Prefix)
	name, ok := m[id]
	if !ok {
		return fmt.Errorf("%s ID %s not found", ra.spec.Group, id)
	}
	return sel.Set(ra.wireKey, ra.spec.Prefix+name)
}

// decompileApps inverts the CompileDriver's container id-promotion: it swaps each
// application's compiled id key back to its authored name (restoring the id field),
// mirroring decompile/pass3/applications.go. It does NOT recurse — the per-resource
// walks that follow handle each app's nested resources via utils.Global.
type decompileApps struct{}

func (a *decompileApps) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	apps, err := o.Child("applications").Object()
	if err == object.ErrNotExist {
		return o, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetching applications failed with %w", err)
	}
	for _, id := range apps.Children() {
		if _, err := utils.RenameByName(apps.Child(id)); err != nil {
			return nil, fmt.Errorf("renaming application %s failed with %w", id, err)
		}
	}
	return o, nil
}

// decompileResource is the generic form of every decompile/pass3/<resource>.go: it
// walks a resource group at one scope and runs the per-instance inverse
// projections. Wrapped in utils.Global so it covers the project scope and every
// application scope, exactly like the passes it replaces.
type decompileResource struct {
	groupKey string
	iter     *engine.Node
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
		if err := applyInstanceInverse(groupObj.Child(key), ri.iter); err != nil {
			return nil, fmt.Errorf("%s %s: %w", ri.groupKey, key, err)
		}
	}
	return o, nil
}

// applyInstanceInverse runs the per-resource-instance wire projections in reverse:
// DerivedBool delete, EnumBool restore, OnlyWhen-gated rename-back, plain Tag
// rename-back, scalar reformat, then the id->name key swap. Each stage is driven by
// attribute annotations; a resource missing an annotation no-ops that stage (this
// is why smartops needs no special-casing — it has no trigger/type attrs, so every
// rename-back and bool op naturally does nothing, absorbing pass3's dead moves).
// The stages touch disjoint keys, so this order (which differs from pass3's textual
// order) yields the identical result.
func applyInstanceInverse(sel object.Selector[object.Refrence], iter *engine.Node) error {
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

	// 5. scalar reformat: ns -> "20s", bytes -> "32GB" (inverse of the compile parse).
	for _, a := range attrs {
		switch scalarOf(a) {
		case "duration":
			if err := utils.FormatTimeout(sel, a.Name); err != nil {
				return err
			}
		case "bytes":
			if err := utils.FormatMemory(sel, a.Name); err != nil {
				return err
			}
		}
	}

	// 6. id->name key swap (inverse of RenameById): every keyed resource — i.e.
	//    every DefineIter carrying an `id` attr — which is all nine here.
	if hasAttr(iter, "id") {
		if _, err := utils.RenameByName(sel); err != nil {
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
