package driver

import (
	"fmt"
	"slices"

	"github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

// IndexCtx is the per-instance environment a resource's index-footprint closure
// receives. The driver computes every field before invoking the closure, so the
// closure only declares WHICH tns paths/entries the resource contributes — the
// driver owns the append/dedup/Set mechanics.
type IndexCtx struct {
	Branch, Project, App, Id, Name string
	// IndexValue is the resource's IndexValue(branch, proj, app, id) — the value
	// appended into every link bucket the closure names. nil is never produced for
	// the eight indexed groups (all declare HasIndex); groups whose footprint does
	// not read it (domains) simply ignore it.
	IndexValue *common.TnsPath
	// Obj is the compiled instance (wire keys, refs already resolved).
	Obj object.Object[object.Refrence]
	// Lookup resolves a referenced instance by (group, id) with the pass4
	// app-local-then-project-root rule: the current scope's group first, then the
	// project-root group. ok is false when neither has it.
	Lookup func(group, id string) (object.Object[object.Refrence], bool)
}

// IndexEntry is one direct key/value the IndexSet closure writes. IfAbsent limits
// the write to keys whose current value is nil (the domain nil-placeholder).
type IndexEntry struct {
	Path     *common.TnsPath
	Value    any
	IfAbsent bool
}

// IndexLinkFunc names the link buckets a resource contributes to; the driver
// appends IndexValue at each path's Versioning().Links() (or verbatim, for Raw).
type IndexLinkFunc func(*IndexCtx) ([]*common.TnsPath, error)

// IndexSetFunc names the direct key/value entries a resource writes.
type IndexSetFunc func(*IndexCtx) ([]IndexEntry, error)

// IndexLink stores a link-footprint closure on a group's iterator node. For each
// returned path the driver appends ic.IndexValue.String() to
// path.Versioning().Links().String(), de-duplicated with slices.Contains.
func IndexLink(fn IndexLinkFunc) engine.NodeOption {
	return engine.GroupAnnotate("indexLink", fn)
}

// IndexLinkRaw is IndexLink but appends at path.String() verbatim (no Links()
// suffix) — the messaging websocket bucket, which aggregates many instances of a
// scope under one key.
func IndexLinkRaw(fn IndexLinkFunc) engine.NodeOption {
	return engine.GroupAnnotate("indexLinkRaw", fn)
}

// IndexSet stores a direct-set closure on a group's iterator node. The driver
// Set()s each entry at Path.String(); IfAbsent entries write only when the key is
// currently unset (Get() == nil).
func IndexSet(fn IndexSetFunc) engine.NodeOption {
	return engine.GroupAnnotate("indexSet", fn)
}

// IndexByName declares the mechanical "keyed by Name" index link most resources
// share: the driver appends the resource's IndexValue to cap(Name).Links(), where
// cap is one of the resource's Addressing capabilities (wasm -> WasmModulePath,
// indexPath -> IndexPath). It is declared explicitly rather than derived from
// Addressing() because a capability being present does not imply it is indexed —
// e.g. websites declare HasWasmModule but write no wasm index link.
func IndexByName(cap engine.Capability) engine.NodeOption {
	return engine.GroupAnnotate("indexByName", cap)
}

// indexByNamePath maps a capability to its by-Name index path, computed
// generically from the group key (its PathVariable) — the exact paths the old
// per-resource pass4 files built via each spec's Tns() helper.
func indexByNamePath(cap engine.Capability, project, app, name, groupKey string) (*common.TnsPath, error) {
	switch cap.String() {
	case "wasm":
		return methods.WasmModulePath(project, app, name, common.PathVariable(groupKey))
	case "indexPath":
		return methods.IndexPath(project, app, name), nil
	default:
		return nil, fmt.Errorf("IndexByName: unsupported capability %q", cap.String())
	}
}

// indexOrder is the fixed V1 index order, taken verbatim from pass4/pipe.go. It is
// NOT the DSL declaration order: link buckets shared across scopes accumulate in
// this order, so it is load-bearing for parity and must not be derived from the
// schema. services is absent (it declares HasIndex but has no index footprint).
var indexOrder = []string{
	"functions",
	"websites",
	"libraries",
	"storages",
	"databases",
	"messaging",
	"smartops",
	"domains",
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
	if _, err := o.CreatePath("indexes"); err != nil {
		return nil, fmt.Errorf("creating indexes path failed with %w", err)
	}

	// Build the pass4-shaped pipe: each group is Sub("object")+Global so the
	// context path (root, project-object-subtree[, app]) and the project-then-apps
	// accumulation order are byte-for-byte what the hand-written passes produced.
	pipe := make([]transform.Transformer[object.Refrence], 0, len(indexOrder))
	for _, key := range indexOrder {
		g := findGroup(d.root, key)
		if g == nil || len(g.Children) == 0 {
			continue
		}
		gi := &groupIndexer{groupKey: key, iter: g.Children[0], branch: d.branch}
		pipe = append(pipe, utils.Sub(utils.Global(gi), "object"))
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
	groupKey string
	iter     *engine.Node
	branch   string
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
		appsObj, err := configRoot.Child("applications").Object()
		if err != nil {
			return nil, fmt.Errorf("fetching applications failed with %w", err)
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

	projectId, err := configRoot.GetString("id")
	if err != nil {
		return nil, fmt.Errorf("project id is not a string: %w", err)
	}

	index, err := root.CreatePath("indexes")
	if err != nil {
		return nil, fmt.Errorf("creating path for indexes failed with %w", err)
	}

	linkFn, _ := gi.iter.Meta["indexLink"].(IndexLinkFunc)
	rawFn, _ := gi.iter.Meta["indexLinkRaw"].(IndexLinkFunc)
	setFn, _ := gi.iter.Meta["indexSet"].(IndexSetFunc)
	byNameCap, _ := gi.iter.Meta["indexByName"].(engine.Capability)

	lookup := makeLookup(config, configRoot)

	for _, id := range groupObj.Children() {
		instObj, err := groupObj.Child(id).Object()
		if err != nil {
			return nil, fmt.Errorf("fetching %s object for %s failed with %w", gi.groupKey, id, err)
		}

		name, _ := instObj.GetString("name")

		indexValue, err := methods.IndexValue(gi.branch, projectId, appId, id, common.PathVariable(gi.groupKey))
		if err != nil {
			return nil, fmt.Errorf("getting index value for %s %s failed with %w", gi.groupKey, id, err)
		}

		ic := &IndexCtx{
			Branch:     gi.branch,
			Project:    projectId,
			App:        appId,
			Id:         id,
			Name:       name,
			IndexValue: indexValue,
			Obj:        instObj,
			Lookup:     lookup,
		}

		if byNameCap != nil {
			p, err := indexByNamePath(byNameCap, projectId, appId, name, gi.groupKey)
			if err != nil {
				return nil, fmt.Errorf("index-by-name for %s %s failed with %w", gi.groupKey, id, err)
			}
			appendLink(index, p.Versioning().Links().String(), indexValue.String())
		}

		if linkFn != nil {
			paths, err := linkFn(ic)
			if err != nil {
				return nil, fmt.Errorf("index links for %s %s failed with %w", gi.groupKey, id, err)
			}
			for _, p := range paths {
				appendLink(index, p.Versioning().Links().String(), indexValue.String())
			}
		}

		if rawFn != nil {
			paths, err := rawFn(ic)
			if err != nil {
				return nil, fmt.Errorf("raw index links for %s %s failed with %w", gi.groupKey, id, err)
			}
			for _, p := range paths {
				appendLink(index, p.String(), indexValue.String())
			}
		}

		if setFn != nil {
			entries, err := setFn(ic)
			if err != nil {
				return nil, fmt.Errorf("index set for %s %s failed with %w", gi.groupKey, id, err)
			}
			for _, e := range entries {
				key := e.Path.String()
				if e.IfAbsent {
					if index.Get(key) == nil {
						index.Set(key, e.Value)
					}
					continue
				}
				index.Set(key, e.Value)
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
