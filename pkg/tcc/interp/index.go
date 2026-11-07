package interp

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp/utils"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

// An index is paths and values, and which paths is the vocabulary's business.
// The driver walks the indexed groups in their declared order over every scope,
// works out each resource's identity, hands it to the vocabulary and writes what
// comes back.

// ScopeLevel is one scope between the project root and a resource: the KIND of container
// it was found in, and its own id.
//
// The kind is here because a vocabulary may declare more than one kind of scope — one an
// author writes, one a lowering synthesizes — and a chain of ids alone cannot tell them
// apart. An index key that announces every level with the same word says a produced scope
// is a namespace, which is the one thing it is not.
type ScopeLevel struct{ Kind, ID string }

// ScopeChain is where a resource lives: the scopes from the project root down to
// it, outermost first, empty at the root. A vocabulary with one kind of scope
// reads IDs().
type ScopeChain []ScopeLevel

// IDs is the scope ids joined by "/", empty at the root.
func (s ScopeChain) IDs() string {
	out := make([]string, 0, len(s))
	for _, level := range s {
		out = append(out, level.ID)
	}
	return strings.Join(out, "/")
}

// IndexCtx is what the driver knows about one resource when it asks what to index. No
// tau types: a path is the vocabulary's to compute, from these.
type IndexCtx struct {
	// Branch is the compile branch, Project the project's id.
	Branch, Project string
	// Scope is where this resource lives: the scopes between the project root and it,
	// outermost first, and empty at the root. A vocabulary whose scopes cannot hold
	// scopes reads one level.
	Scope ScopeChain
	// Kind, ID and Name are the resource itself.
	Kind, ID, Name string
	// Obj is the compiled instance: wire keys, references already resolved.
	Obj object.Object[object.Refrence]
	// Lookup finds another compiled resource by (group, id), this scope first and then
	// the project root — the same order a reference resolves in.
	Lookup func(group, id string) (object.Object[object.Refrence], bool)
}

// IndexEntry is one thing to write into the index. Build one with Link or Set rather
// than by hand: a struct literal with a bool in it makes the reader work out which.
type IndexEntry struct {
	// Path is where, in whatever path language the vocabulary uses. Opaque here.
	Path string
	// Value is what goes there.
	Value any
	// Append accumulates into a de-duplicated list instead of setting a single value.
	Append bool
}

// Link appends a value to the bucket at a path — the shape most of an index is: many
// resources accumulating under one key, each named once however many times it is added.
func Link(path string, value any) IndexEntry {
	return IndexEntry{Path: path, Value: value, Append: true}
}

// Set puts a single value at a path: a fact about this resource rather than a bucket.
func Set(path string, value any) IndexEntry {
	return IndexEntry{Path: path, Value: value}
}

// Indexer is asked what one resource contributes to the index.
type Indexer func(IndexCtx) ([]IndexEntry, error)

// Indexes composes several footprints into one, in order. A bucket accumulates, so the
// order within a resource matters for the same reason the order of groups does.
func indexAll(fns ...Indexer) Indexer {
	return func(ic IndexCtx) ([]IndexEntry, error) {
		var out []IndexEntry
		for _, fn := range fns {
			entries, err := fn(ic)
			if err != nil {
				return nil, err
			}
			out = append(out, entries...)
		}
		return out, nil
	}
}

// Each resolves a reference field to the values it points at: for every id in
// refField, that resource's targetKey field. An absent field is no values.
func (ic IndexCtx) Each(refField, targetGroup, targetKey string) ([]string, error) {
	refs, ok := ic.Obj.Get(refField).([]string)
	if !ok {
		return nil, nil
	}

	out := make([]string, 0, len(refs))
	for _, id := range refs {
		target, found := ic.Lookup(targetGroup, id)
		if !found {
			return nil, fmt.Errorf("%s/%s, which %s references, is not in the compiled configuration", targetGroup, id, refField)
		}
		value, err := target.GetString(targetKey)
		if err != nil {
			return nil, fmt.Errorf("%s of %s/%s is not a string: %w", targetKey, targetGroup, id, err)
		}
		out = append(out, value)
	}
	return out, nil
}

// indexSpec is what a group declares with Indexes.
type indexSpec struct {
	position int
	index    Indexer
}

// Indexes declares that a group contributes to the index, at a position: buckets
// shared across scopes accumulate in visit order, so the order is part of the
// artifact.
func Indexes(position int, index ...Indexer) engine.NodeOption {
	return engine.GroupAnnotate("indexes", indexSpec{position: position, index: indexAll(index...)})
}

// indexerOf reads a group's declaration back.
func indexerOf(iter *engine.Node) (indexSpec, bool) {
	if iter == nil {
		return indexSpec{}, false
	}
	spec, ok := iter.Meta["indexes"].(indexSpec)
	return spec, ok
}

// indexedGroups is every group declaring an index footprint, in position order.
func indexedGroups(root *engine.Node) []string {
	type at struct {
		key      string
		position int
	}
	var groups []at
	for _, g := range root.Children {
		key, _ := g.Match.(string)
		if key == "" || len(g.Children) == 0 {
			continue
		}
		if spec, ok := indexerOf(g.Children[0]); ok {
			groups = append(groups, at{key: key, position: spec.position})
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].position < groups[j].position })

	out := make([]string, 0, len(groups))
	for _, g := range groups {
		out = append(out, g.key)
	}
	return out
}

// IndexDriver ensures the root `indexes` object exists, then walks each indexed
// group across every scope, running its declared footprints per instance.
type IndexDriver struct {
	root   *engine.Node
	branch string
}

// NewIndexDriver builds the driver from the schema root node; branch reaches
// every IndexCtx.
func NewIndexDriver(root *engine.Node, branch string) transform.Transformer[object.Refrence] {
	return &IndexDriver{root: root, branch: branch}
}

func (d *IndexDriver) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	// `indexes` exists even with no resources.
	if _, err := o.CreatePath(IndexesKey); err != nil {
		return nil, fmt.Errorf("creating indexes path failed with %w", err)
	}

	// Each group is Sub("object")+Global, so the context path and the
	// project-then-scopes accumulation order are fixed.
	containers := ContainerKeys(d.root)
	groups := indexedGroups(d.root)
	pipe := make([]transform.Transformer[object.Refrence], 0, len(groups))
	for _, key := range groups {
		g := findGroup(d.root, key)
		if g == nil || len(g.Children) == 0 {
			continue
		}
		idField, nameField := engine.IdentityOf(d.root)
		gi := &groupIndexer{
			groupKey: key, iter: g.Children[0], branch: d.branch, containers: containers,
			idField: idField, nameField: nameField,
		}
		pipe = append(pipe, utils.Sub(utils.Global(containers, gi), ProjectKey))
	}

	return transform.Pipe(ct, o, pipe...)
}

// findGroup returns the resource group node named key, or nil.
func findGroup(root *engine.Node, key string) *engine.Node {
	for _, g := range root.Children {
		if s, ok := g.Match.(string); ok && s == key {
			return g
		}
	}
	return nil
}

// groupIndexer indexes one resource group at one scope: it computes each
// resource's identity and lookup, then runs the group's footprints.
type groupIndexer struct {
	// what the schema calls a resource's id and its name (engine.Identity): an index is
	// keyed by one and carries the other, and neither word is this package's
	idField, nameField string
	groupKey           string
	iter               *engine.Node
	branch             string
	containers         []string
}

func (gi *groupIndexer) Process(ct transform.Context[object.Refrence], config object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	if len(ct.Path()) < 2 {
		return nil, fmt.Errorf("path %v is too short", ct.Path())
	}

	root, ok := ct.Path()[0].(object.Object[object.Refrence])
	if !ok {
		return nil, fmt.Errorf("root is not an object")
	}

	configRoot, ok := ct.Path()[1].(object.Object[object.Refrence])
	if !ok {
		return nil, fmt.Errorf("config root is not an object")
	}

	var scope ScopeChain
	if configRoot != config {
		scope = scopeOf(configRoot, config, gi.containers)
	}

	groupObj, err := config.Child(gi.groupKey).Object()
	if err == object.ErrNotExist {
		return config, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetching %s config failed with %w", gi.groupKey, err)
	}

	projectId, err := configRoot.GetString(gi.idField)
	if err != nil {
		return nil, fmt.Errorf("project id is not a string: %w", err)
	}

	index, err := root.CreatePath(IndexesKey)
	if err != nil {
		return nil, fmt.Errorf("creating path for indexes failed with %w", err)
	}

	spec, ok := indexerOf(gi.iter)
	if !ok {
		return config, nil
	}
	lookup := makeLookup(ct.Path())

	for _, id := range groupObj.Children() {
		if !keyedByIdentity(gi.iter, gi.idField, id) {
			continue
		}
		instObj, err := groupObj.Child(id).Object()
		if err != nil {
			return nil, fmt.Errorf("fetching %s object for %s failed with %w", gi.groupKey, id, err)
		}
		name, _ := instObj.GetString(gi.nameField)

		entries, err := spec.index(IndexCtx{
			Branch: gi.branch, Project: projectId, Scope: scope,
			Kind: gi.groupKey, ID: id, Name: name,
			Obj: instObj, Lookup: lookup,
		})
		if err != nil {
			return nil, fmt.Errorf("indexing %s %s failed with %w", gi.groupKey, id, err)
		}
		for _, e := range entries {
			switch {
			case e.Append:
				appendLink(index, e.Path, fmt.Sprint(e.Value))
			default:
				index.Set(e.Path, e.Value)
			}
		}

		if err := emitAttrValidations(ct, instObj, gi.iter.Attributes, projectId, scope.IDs()); err != nil {
			return nil, err
		}
	}

	return config, nil
}

// appendLink appends val to the []string bucket at key, creating it when absent
// and de-duplicating — the exact accumulate-and-set the pass4 files repeated.
func appendLink(index object.Object[object.Refrence], key, val string) {
	links, ok := index.Get(key).([]string)
	if !ok {
		links = []string{}
	}
	if !slices.Contains(links, val) {
		links = append(links, val)
	}
	index.Set(key, links)
}

// scopeOf is the chain from the project root down to a scope, each level with
// its container kind and id. It walks down, searching every container at every
// level, since scopes nest and of either kind.
func scopeOf(from, target object.Object[object.Refrence], containers []string) ScopeChain {
	for _, container := range containers {
		scopes, err := from.Child(container).Object()
		if err != nil {
			continue
		}
		for _, id := range scopes.Children() {
			scope, err := scopes.Child(id).Object()
			if err != nil {
				continue
			}
			here := ScopeLevel{Kind: container, ID: id}
			if scope == target {
				return ScopeChain{here}
			}
			if under := scopeOf(scope, target, containers); len(under) > 0 {
				return append(ScopeChain{here}, under...)
			}
		}
	}
	return nil
}

// makeLookup builds the (group, id) resolver over the chain references resolve
// along (utils.ResolveNameToId): the current scope, then each parent, then the
// project root.
func makeLookup(scopes []any) func(string, string) (object.Object[object.Refrence], bool) {
	return func(group, id string) (object.Object[object.Refrence], bool) {
		for _, at := range utils.Outward(scopes) {
			scope, ok := at[len(at)-1].(object.Object[object.Refrence])
			if !ok {
				continue
			}
			g, err := scope.Child(group).Object()
			if err != nil {
				continue
			}
			if o, err := g.Child(id).Object(); err == nil {
				return o, true
			}
		}
		return nil, false
	}
}

// attrValues is every value an attribute contributes to a deferred validation:
// one per list element, so the answer names which one is wrong.
func attrValues(instObj object.Object[object.Refrence], a *engine.Attribute) []string {
	key := a.Segments
	if len(key) == 0 {
		key = []string{a.Name}
	}

	obj := instObj
	if len(key) > 1 {
		var err error
		if obj, err = instObj.Fetch(key[:len(key)-1]...); err != nil || obj == nil {
			return nil
		}
	}

	switch v := obj.Get(key[len(key)-1]).(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// emitAttrValidations fires the deferred (index-time) validations declared on an
// iterator's attributes — the domain fqdn's EmitValidation("domain","dns"),
// carrying the {project[, app]} scope context pass4/domain.go attached. Distinct
// from the CompileDriver's root EmitValidation, which uses an empty context.
func emitAttrValidations(ct transform.Context[object.Refrence], instObj object.Object[object.Refrence], attrs []*engine.Attribute, projectId, appId string) error {
	for _, a := range attrs {
		ve, ok := a.Meta["emitValidation"].(engine.ValidationEmit)
		if !ok {
			continue
		}
		for _, val := range attrValues(instObj, a) {
			context := map[string]any{"project": projectId}
			if appId != "" {
				context["app"] = appId
			}
			store := ct.Store().Validators()
			vals := append(store.Get(), engine.NewNextValidation(ve.Key, val, ve.Validator, context))
			if _, err := store.Set(vals); err != nil {
				return fmt.Errorf("storing validations failed with %w", err)
			}
		}
	}
	return nil
}
