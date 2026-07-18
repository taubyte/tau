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

// CompileDriver replaces the whole pass1 layer. It walks the project root and
// each application scope alongside the DSL groups, reproducing pass1 exactly —
// but reading the behavior off attribute/node annotations rather than a file per
// resource.
type CompileDriver struct {
	root   *engine.Node
	cloud  string
	branch string
}

// newCompileDriver builds a CompileDriver from the schema root node. `root` carries
// the project-scope attributes (id -> project_id validation, tags -> wire drop via
// annotations) and the resource/container/clouds groups as its children. cloud
// and branch are threaded into the TC that group-transform closures (clouds)
// receive. Takes the node tree as data so this package never imports schema.
func newCompileDriver(root *engine.Node, cloud, branch string) transform.Transformer[object.Refrence] {
	return &CompileDriver{root: root, cloud: cloud, branch: branch}
}

func (d *CompileDriver) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	// Fork once with the project root so the context path matches pass1's
	// utils.Global(...) shape ([root]) — the name->id index keys derive from it.
	ctRoot := ct.Fork(o)

	// Project-scope attribute ops: drop the root `tags`, emit the project_id
	// validation. These live on the root node's own attributes.
	if err := d.applyRootOps(ctRoot, o, d.root.Attributes); err != nil {
		return nil, err
	}

	// Groups: resources (project scope), the applications container (id-promote
	// each app, then recurse into its scope), and clouds (a GroupTransform).
	if err := d.processGroups(ctRoot, o, d.root.Children); err != nil {
		return nil, err
	}

	return o, nil
}

// processGroups runs each group at the given scope (project root or an app).
func (d *CompileDriver) processGroups(ct transform.Context[object.Refrence], scope object.Object[object.Refrence], groups []*engine.Node) error {
	for _, g := range groups {
		if err := d.processGroup(ct, scope, g); err != nil {
			return err
		}
	}
	return nil
}

func (d *CompileDriver) processGroup(ct transform.Context[object.Refrence], scope object.Object[object.Refrence], g *engine.Node) error {
	groupKey, _ := g.Match.(string)
	if len(g.Children) == 0 {
		return nil
	}
	iter := g.Children[0]

	// A GroupTransform closure owns the whole projection for this group (clouds:
	// flatten the active fqdn to root scalars and drop the map). It reads the
	// scope object directly, so there is no per-instance walk.
	if fn, ok := iter.Meta["groupTransform"].(GroupTransformFunc); ok {
		return fn(&TC{Cloud: d.cloud, Branch: d.branch}, scope)
	}

	groupObj, err := scope.Child(groupKey).Object()
	if err == object.ErrNotExist {
		return nil
	}
	if err != nil {
		return fmt.Errorf("fetching %s failed with %w", groupKey, err)
	}

	// A container group (its iterator is itself a group holding nested resource
	// groups — the applications container). Each instance is id-promoted with NO
	// index (no Resource descriptor, matching pass1/applications.go), then its
	// nested resource groups are processed in that instance's scope.
	if iter.Group && len(iter.Children) > 0 {
		for _, instName := range groupObj.Children() {
			sel := groupObj.Child(instName)
			instObj, err := sel.Object()
			if err != nil {
				return fmt.Errorf("fetching %s/%s failed with %w", groupKey, instName, err)
			}
			if _, err := utils.RenameById(sel, instName); err != nil {
				return fmt.Errorf("promoting %s %s failed with %w", groupKey, instName, err)
			}
			if err := d.processGroups(ct.Fork(instObj), instObj, iter.Children); err != nil {
				return err
			}
		}
		return nil
	}

	// A resource group: per-instance attribute projections, then id-promotion,
	// then (for Resource groups) the name->id index.
	_, isResource := iter.Meta["resource"].([4]string)
	promote := hasAttr(iter, "id")
	for _, instName := range groupObj.Children() {
		sel := groupObj.Child(instName)
		if err := applyInstanceOps(sel, iter.Attributes); err != nil {
			return fmt.Errorf("%s %s: %w", groupKey, instName, err)
		}
		if promote {
			idStr, err := utils.RenameById(sel, instName)
			if err != nil {
				return fmt.Errorf("promoting %s %s failed with %w", groupKey, instName, err)
			}
			if isResource {
				if err := utils.IndexById(ct, groupKey, instName, idStr); err != nil {
					return fmt.Errorf("indexing %s %s failed with %w", groupKey, instName, err)
				}
			}
		}
	}
	return nil
}

// applyInstanceOps runs the per-resource-instance wire projections in the order
// the design review fixed: scalar parse, EnumBool, DerivedBool, OnlyWhen-gated
// rename, then plain Tag renames. Each stage is driven by attribute annotations;
// a resource missing an annotation simply produces nothing for that stage (this
// is why smartops needs no special-casing — it has no trigger/type attrs, so its
// projections and renames naturally no-op).
func applyInstanceOps(sel object.Selector[object.Refrence], attrs []*engine.Attribute) error {
	// 1. scalar parse (Duration -> ns, Bytes -> bytes): each scalar term carries
	//    its own codec, so there is no per-scalar switch to keep in sync.
	for _, a := range attrs {
		if sc, ok := a.Meta["scalar"].(engine.ScalarSpec); ok {
			if err := sc.Parse(sel, a.Name); err != nil {
				return err
			}
		}
	}

	// 2. EnumBool: project the source enum to a bool field, dropping the source
	//    wire key only for the declared DropWhen values.
	for _, a := range attrs {
		if eb, ok := a.Meta["enumBool"].(engine.EnumBoolSpec); ok && eb.GoName != "" {
			if err := projectEnumBool(sel, a.Name, eb); err != nil {
				return err
			}
		}
	}

	// 3. DerivedBool: synthesize a bool from the source value (keeping the source
	//    key); a value with no When entry emits nothing.
	for _, a := range attrs {
		if db, ok := a.Meta["derivedBool"].(engine.DerivedBoolSpec); ok && db.GoName != "" {
			if err := projectDerivedBool(sel, a.Name, db); err != nil {
				return err
			}
		}
	}

	// 4. OnlyWhen-gated rename: keep+rename to Tag when the gate matches, else
	//    delete the key (p2p-protocol -> service only when type == "p2p").
	for _, a := range attrs {
		if ow, ok := a.Meta["onlyWhen"].(engine.OnlyWhenSpec); ok {
			applyOnlyWhen(sel, a, ow)
		}
	}

	// 5. plain Tag rename: move the attribute's own key to its Tag value, for
	//    every tagged attribute that is NOT OnlyWhen-gated (handled above). A
	//    no-op when the source key is absent, matching pass1's blind Move()s.
	for _, a := range attrs {
		tag, ok := tagOf(a)
		if !ok {
			continue
		}
		if _, gated := a.Meta["onlyWhen"]; gated {
			continue
		}
		_ = sel.Move(a.Name, tag)
	}

	return nil
}

// applyRootOps applies the project-scope attribute annotations: EmitValidation
// (push a deferred external validation keyed off the attribute's value) and
// WireDrop (delete the compiled key). Mirrors the old pass1/project.go.
func (d *CompileDriver) applyRootOps(ct transform.Context[object.Refrence], o object.Object[object.Refrence], attrs []*engine.Attribute) error {
	for _, a := range attrs {
		if ve, ok := a.Meta["emitValidation"].(engine.ValidationEmit); ok {
			val, err := o.GetString(a.Name)
			if err != nil {
				return fmt.Errorf("%s is not a string: %w", a.Name, err)
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

// projectEnumBool sets lower(GoName) = (value in TrueWhen), then deletes the
// source wire key when the value is in DropWhen. Runs whenever the instance
// exists (matching pass1's `if err == nil` guard, which is always true for an
// enumerated child); a missing/non-string value yields false and no drop.
func projectEnumBool(sel object.Selector[object.Refrence], name string, eb engine.EnumBoolSpec) error {
	val, err := sel.Get(name)
	if err != nil {
		return nil
	}
	isTrue, drop := false, false
	if s, ok := val.(string); ok {
		isTrue = slices.Contains(eb.TrueWhen, s)
		drop = slices.Contains(eb.DropWhen, s)
	}
	if err := sel.Set(strings.ToLower(eb.GoName), isTrue); err != nil {
		return err
	}
	if drop {
		sel.Delete(name)
	}
	return nil
}

// projectDerivedBool sets lower(GoName) = When[value], emitting nothing when the
// value is absent, non-string, or not a key of When (keeping the source key).
func projectDerivedBool(sel object.Selector[object.Refrence], name string, db engine.DerivedBoolSpec) error {
	val, err := sel.Get(name)
	if err != nil {
		return nil
	}
	s, ok := val.(string)
	if !ok {
		return nil
	}
	b, has := db.When[s]
	if !has {
		return nil
	}
	return sel.Set(strings.ToLower(db.GoName), b)
}

// applyOnlyWhen reproduces the p2p-protocol branch of pass1/functions.go: when
// the gate attribute's value is one of Vals, rename the key to its Tag; on any
// other value (including gate absent/non-string), delete the key.
func applyOnlyWhen(sel object.Selector[object.Refrence], a *engine.Attribute, ow engine.OnlyWhenSpec) {
	match := false
	if g, ok := valueOf(sel, ow.Attr); ok {
		match = slices.Contains(ow.Vals, g)
	}
	if match {
		if tag, ok := tagOf(a); ok {
			_ = sel.Move(a.Name, tag)
			return
		}
		return
	}
	sel.Delete(a.Name)
}

// valueOf reads a string attribute value off the selector; ok is false when the
// key is absent or not a string.
func valueOf(sel object.Selector[object.Refrence], name string) (string, bool) {
	v, err := sel.Get(name)
	if err != nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func tagOf(a *engine.Attribute) (string, bool) {
	t, ok := a.Meta["tag"].(string)
	return t, ok && t != ""
}

func hasAttr(n *engine.Node, name string) bool {
	for _, a := range n.Attributes {
		if a.Name == name {
			return true
		}
	}
	return false
}
