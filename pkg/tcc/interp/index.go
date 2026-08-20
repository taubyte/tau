package interp

import (
	"fmt"
	"slices"
	"sort"

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

// IndexCtx is what the driver knows about one resource when it asks what to index. No
// tau types: a path is the vocabulary's to compute, from these.
type IndexCtx struct {
	// Branch is the compile branch, Project the project's id, Scope the id of the scope
	// this resource is in ("" at the project root).
	Branch, Project, Scope string
	// Kind, ID and Name are the resource itself.
	Kind, ID, Name string
	// Obj is the compiled instance: wire keys, references already resolved.
	Obj object.Object[object.Refrence]
	// Lookup finds another compiled resource by (group, id), this scope first and then
	// the project root — the same order a reference resolves in.
	Lookup func(group, id string) (object.Object[object.Refrence], bool)
}

// IndexEntry is one thing to write into the index.
type IndexEntry struct {
	// Path is where, in whatever path language the vocabulary uses. Opaque here.
	Path string
	// Value is what goes there. A nil value writes a hole, which is a real thing to want:
	// it reserves a key without claiming anything is at it yet.
	Value any
	// Append accumulates into a de-duplicated list instead of setting a single value —
	// the difference between a bucket every resource adds to and a fact about one.
	Append bool
}

// Indexer is asked what one resource contributes to the index.
type Indexer func(IndexCtx) ([]IndexEntry, error)

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
func Indexes(position int, index Indexer) engine.NodeOption {
	return engine.GroupAnnotate("indexes", indexSpec{position: position, index: index})
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
	container := containerKey(d.root)
	groups := indexedGroups(d.root)
	pipe := make([]transform.Transformer[object.Refrence], 0, len(groups))
	for _, key := range groups {
		g := findGroup(d.root, key)
		if g == nil || len(g.Children) == 0 {
			continue
		}
		idField, nameField := engine.IdentityOf(d.root)
		gi := &groupIndexer{
			groupKey: key, iter: g.Children[0], branch: d.branch, container: container,
			idField: idField, nameField: nameField,
		}
		pipe = append(pipe, utils.Sub(utils.Global(container, gi), ProjectKey))
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
	container          string
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

	appId := ""
	if configRoot != config {
		appsObj, err := configRoot.Child(gi.container).Object()
		if err != nil {
			return nil, fmt.Errorf("fetching %s failed with %w", gi.container, err)
		}
		appId = appsObj.Child(config).Name()
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
	lookup := makeLookup(config, configRoot)

	for _, id := range groupObj.Children() {
		instObj, err := groupObj.Child(id).Object()
		if err != nil {
			return nil, fmt.Errorf("fetching %s object for %s failed with %w", gi.groupKey, id, err)
		}
		name, _ := instObj.GetString(gi.nameField)

		entries, err := spec.index(IndexCtx{
			Branch: gi.branch, Project: projectId, Scope: appId,
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

		if err := emitAttrValidations(ct, instObj, gi.iter.Attributes, projectId, appId); err != nil {
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

// makeLookup builds the (group, id) resolver with pass4's app-local-then-global
// rule: the current scope's group is primary, the project-root group is
// secondary; an app with no such group falls back to the project-root one; at
// project scope there is no secondary.
func makeLookup(config, configRoot object.Object[object.Refrence]) func(string, string) (object.Object[object.Refrence], bool) {
	return func(group, id string) (object.Object[object.Refrence], bool) {
		secondary, _ := configRoot.Child(group).Object()
		primary, _ := config.Child(group).Object()
		if primary == nil {
			primary = secondary
		}
		if primary == secondary {
			secondary = nil
		}
		if primary != nil {
			if o, err := primary.Child(id).Object(); err == nil {
				return o, true
			}
		}
		if secondary != nil {
			if o, err := secondary.Child(id).Object(); err == nil {
				return o, true
			}
		}
		return nil, false
	}
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
		val, err := instObj.GetString(a.Name)
		if err != nil {
			return fmt.Errorf("%s is not a string: %w", a.Name, err)
		}
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
	return nil
}
