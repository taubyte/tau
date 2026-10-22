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

// AN INDEX IS PATHS AND VALUES, and which paths is the vocabulary's business.
//
// This driver used to carry six annotations — by-name, foreign-key, repository, name,
// by-scope, placeholder — and each one computed a tau TNS path: it imported
// specs/methods, specs/common and core/common/repositorytype, which is one vocabulary's
// address space compiled into the layer that must serve every vocabulary. A second one
// could not have an index at all without editing the interpreter.
//
// What is left is the mechanics, which really are generic: walk the indexed groups in
// their declared order, over the project scope and every scope inside it; for each
// resource work out its identity and hand it to the vocabulary; write what comes back.

// ScopeLevel is one scope between the project root and a resource: the KIND of container
// it was found in, and its own id.
//
// The kind is here because a vocabulary may declare more than one kind of scope — one an
// author writes, one a lowering synthesizes — and a chain of ids alone cannot tell them
// apart. An index key that announces every level with the same word says a produced scope
// is a namespace, which is the one thing it is not.
type ScopeLevel struct{ Kind, ID string }

// ScopeChain is where a resource lives: the chain of scopes from the project root down to
// it, outermost first, and empty at the root.
//
// ONE FIELD, TWO READINGS. A vocabulary that has one kind of scope wants the ids and
// nothing else, and reads IDs(); one that has more asks each level what it is. A second
// field carrying the same fact in a second shape is the thing that drifts.
type ScopeChain []ScopeLevel

// IDs is the chain as v1 has always read it: the scope ids joined by "/", empty at the
// root.
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

// IndexEntry is one thing to write into the index. Build one with Link, Set or Hole
// rather than by hand: those are the three things an index entry can be, and a struct
// literal with a bool in it makes the reader work out which.
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

// Hole reserves a key without claiming anything is at it yet — a name that resolves to
// nothing so far, which is different from a name nobody has mentioned. Never written over
// something already there.
func Hole(path string) IndexEntry { return IndexEntry{Path: path} }

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

// Each resolves a reference field to the values it points at: for every id in refField,
// the resource it names, and one of ITS fields. That fan-out — a resource indexed under
// something another resource is called — is the one shape that needs the lookup, and
// leaving it to each vocabulary means each one rewrites the same three failures.
//
// A field that is absent is no values rather than an error: nothing referenced, nothing
// to fan out to.
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

// Indexes declares that a group contributes to the index, at a position, by a function
// the vocabulary supplies.
//
// The POSITION is load-bearing and is why it is not simply declaration order: buckets
// shared across scopes accumulate in the order groups are visited, so a different
// sequence is a different artifact.
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

// indexedGroups is every group that declares an index footprint, in the order the
// vocabulary put them in.
//
// The order is load-bearing: buckets shared across scopes accumulate in it, so a
// different sequence is a different artifact. It used to be a list of eight group names
// here — v1's, verbatim — which for any other vocabulary matches nothing: every indexed
// group is skipped and the indexes come out empty with no error.
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

// IndexDriver replaces the whole pass4 layer with one generic transform. It
// ensures the root `indexes` object exists, then walks each indexed group (in the
// fixed order above) across the project scope and every application scope,
// running that group's DSL-declared index-footprint closures per instance. It is
// driven entirely by the node tree it is handed, so it never restates the schema.
type IndexDriver struct {
	root   *engine.Node
	branch string
}

// NewIndexDriver builds the driver from the schema root node (the same node the
// CompileDriver consumes). branch is threaded into every IndexValue.
func NewIndexDriver(root *engine.Node, branch string) transform.Transformer[object.Refrence] {
	return &IndexDriver{root: root, branch: branch}
}

func (d *IndexDriver) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	// Ensure `indexes` always exists, even with no resources (absorbs
	// pass4/init_indexes.go).
	if _, err := o.CreatePath(IndexesKey); err != nil {
		return nil, fmt.Errorf("creating indexes path failed with %w", err)
	}

	// Build the pass4-shaped pipe: each group is Sub("object")+Global so the
	// context path (root, project-object-subtree[, app]) and the project-then-apps
	// accumulation order are byte-for-byte what the hand-written passes produced.
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

// groupIndexer indexes one resource group at one scope. It is the generic form of
// every pass4/<resource>.go file: it computes the identity/IndexValue/Lookup each
// pass4 file computed by hand, then runs the group's declared closures.
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
			case e.Value == nil:
				// a hole: reserve the key without claiming anything is at it, and never
				// over a value something already put there
				if index.Get(e.Path) == nil {
					index.Set(e.Path, nil)
				}
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

// scopeOf is where a scope sits: the chain from the project root down to it, each level
// carrying the container it was found in and its own id. It walks DOWN because the
// reverse lookup it replaced asked the root's container for the name of the object it had
// been handed, and an object is a direct child of that container only at the first level.
// v1's applications cannot hold applications, so one level was the whole tree; v2's
// namespaces nest, and everything two deep was indexed as though it sat at the project
// root — under a scope of "", against the same keys the root's own resources use.
//
// Every container is searched at every level, because a scope of one kind holds scopes of
// the other: an authored namespace holds the scope a lowering made inside it.
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
		for i := len(scopes) - 1; i >= 1; i-- {
			scope, ok := scopes[i].(object.Object[object.Refrence])
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

// attrValues is every value an attribute contributes to a deferred validation.
//
// ONE PER ELEMENT FOR A LIST, because each element is checked on its own: a
// resource naming three labels has three chances to be wrong about one, and a
// single validation carrying the whole list would make the answer "one of these
// is bad" rather than which.
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

// attrString reads an attribute off a compiled instance BY THE PATH ITS KEY IS.
//
// Segments is the key as declared — one level of the compiled object per entry —
// so `region/name` is a level called `region` holding `name`. A flat lookup of
// the joined name finds nothing there, which is the second reason a nested
// attribute could not declare a deferred validation. An attribute whose key has
// one segment carries no Segments and is its Name.
//
// Absent is reported as not-held rather than as an error: see the caller.
func attrString(instObj object.Object[object.Refrence], a *engine.Attribute) (string, bool) {
	key := a.Segments
	if len(key) == 0 {
		key = []string{a.Name}
	}

	obj := instObj
	if len(key) > 1 {
		var err error
		if obj, err = instObj.Fetch(key[:len(key)-1]...); err != nil || obj == nil {
			return "", false
		}
	}

	val, err := obj.GetString(key[len(key)-1])
	if err != nil || val == "" {
		return "", false
	}
	return val, true
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
