package driver

import (
	"fmt"
	"slices"

	"github.com/taubyte/tau/core/common/repositorytype"
	"github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

// IndexCtx is the per-instance environment the driver's index-annotation routines
// read. The driver computes every field before running an annotation, so each
// routine only reads WHICH tns paths/entries its resource contributes — the driver
// owns the append/dedup/Set mechanics.
type IndexCtx struct {
	Branch, Project, App, Id, Name string
	// IndexValue is the resource's IndexValue(branch, proj, app, id) — the value
	// appended into every link bucket an annotation names. nil is never produced for
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

// foreignKeyIndex is the declared footprint of an IndexForeignKey annotation: the
// capability whose path form is written, the wire field holding the resolved
// target ids, and the (group, key) the target's index value is read from.
type foreignKeyIndex struct {
	cap                              engine.Capability
	refField, targetGroup, targetKey string
}

// IndexForeignKey declares the domain-style fan-out link: for each resolved target
// id in refField (a []string of ids), the driver looks the target up in
// targetGroup, reads targetKey off it, computes cap's path from that value, and
// appends this resource's IndexValue at the path's Links() bucket. Replaces the
// functions/websites domain-http closures — both declare
// IndexForeignKey(HasHttp, "domains", "domains", "fqdn").
func IndexForeignKey(cap engine.Capability, refField, targetGroup, targetKey string) engine.NodeOption {
	if c, ok := cap.(*Cap); !ok || c.ForeignKey == nil {
		panic(fmt.Sprintf("IndexForeignKey: capability %q carries no foreign-key path", cap.String()))
	}
	return engine.GroupAnnotate("indexForeignKey", foreignKeyIndex{cap, refField, targetGroup, targetKey})
}

// IndexRepo declares the git-repo reverse index a website/library contributes: the
// driver rebuilds the repository path from the instance's provider + repository-id
// wire keys and Set()s the repo type at <repo>/type and this resource's IndexValue
// at <repo>/resource/<id>. Replaces the website/library repository IndexSet arms.
func IndexRepo(repoType repositorytype.Type) engine.NodeOption {
	return engine.GroupAnnotate("indexRepo", repoType)
}

// IndexName declares the id-keyed name index a resource contributes: the driver
// Set()s the resource's Name at <resourceType>/<id>. A marker (no payload) — the
// path is generic in the group key. Replaces library's NameIndex IndexSet arm.
func IndexName() engine.NodeOption {
	return engine.GroupAnnotate("indexName", true)
}

// IndexByScope declares a per-(project,app) scope-aggregated RAW link: the driver
// computes cap's scope path and appends this resource's IndexValue at path.String()
// verbatim (NO Links() suffix), so every instance of the scope aggregates under
// one key. Replaces messaging's websocket IndexLinkRaw closure.
func IndexByScope(cap engine.Capability) engine.NodeOption {
	if c, ok := cap.(*Cap); !ok || c.ScopePath == nil {
		panic(fmt.Sprintf("IndexByScope: capability %q carries no scope path", cap.String()))
	}
	return engine.GroupAnnotate("indexByScope", cap)
}

// IndexPlaceholder declares a nil-placeholder link keyed by keyField's value: the
// driver reverses the fqdn at keyField into the group's basic path and, only when
// the key's Links() bucket is currently absent, Set()s it to nil. Replaces
// domains' basic-path nil IndexSet closure.
func IndexPlaceholder(keyField string) engine.NodeOption {
	return engine.GroupAnnotate("indexPlaceholder", keyField)
}

// IndexByName declares the mechanical "keyed by Name" index link most resources
// share: the driver appends the resource's IndexValue to cap(Name).Links(), where
// cap is one of the resource's Addressing capabilities (wasm -> WasmModulePath,
// indexPath -> IndexPath). It is declared explicitly rather than derived from
// Addressing() because a capability being present does not imply it is indexed —
// e.g. websites declare HasWasmModule but write no wasm index link.
func IndexByName(cap engine.Capability) engine.NodeOption {
	if c, ok := cap.(*Cap); !ok || c.ByName == nil {
		panic(fmt.Sprintf("IndexByName: capability %q carries no by-name path", cap.String()))
	}
	return engine.GroupAnnotate("indexByName", cap)
}

// indexByNamePath computes a capability's by-Name index path from its declared
// ByName role, keyed by the group's PathVariable — the exact paths the old
// per-resource pass4 files built via each spec's Tns() helper. The IndexByName
// constructor guarantees the role is present, so a miss here is a programmer error.
func indexByNamePath(cap engine.Capability, project, app, name, groupKey string) (*common.TnsPath, error) {
	c, ok := cap.(*Cap)
	if !ok || c.ByName == nil {
		return nil, fmt.Errorf("IndexByName: capability %q carries no by-name path", cap.String())
	}
	return c.ByName(project, app, name, common.PathVariable(groupKey))
}

// indexForeignKey reproduces the old domainHttpPaths fan-out: for every resolved
// target id in fk.refField, look the target up, read fk.targetKey, compute fk.cap's
// path from that value, and append the instance's IndexValue at its Links() bucket.
func indexForeignKey(ic *IndexCtx, index object.Object[object.Refrence], groupKey string, fk foreignKeyIndex) error {
	refVal := ic.Obj.Get(fk.refField)
	refs, ok := refVal.([]string)
	if !ok && refVal != nil {
		return fmt.Errorf("%s is not a []string", fk.refField)
	}

	for _, targetId := range refs {
		targetObj, ok := ic.Lookup(fk.targetGroup, targetId)
		if !ok {
			return fmt.Errorf("fetching %s object for %s failed", fk.targetGroup, targetId)
		}
		keyVal, err := targetObj.GetString(fk.targetKey)
		if err != nil {
			return fmt.Errorf("%s is not a string for %s %s: %w", fk.targetKey, fk.targetGroup, targetId, err)
		}
		p, err := foreignKeyPath(fk.cap, keyVal, groupKey)
		if err != nil {
			return fmt.Errorf("getting %s path for %s %s failed with %w", fk.cap.String(), fk.targetGroup, targetId, err)
		}
		appendLink(index, p.Versioning().Links().String(), ic.IndexValue.String())
	}
	return nil
}

// foreignKeyPath computes the path form IndexForeignKey writes from a capability's
// ForeignKey role, keyed off the resolved target value (e.g. http ->
// HttpPath(fqdn, group)). The IndexForeignKey constructor guarantees the role.
func foreignKeyPath(cap engine.Capability, value, groupKey string) (*common.TnsPath, error) {
	c, ok := cap.(*Cap)
	if !ok || c.ForeignKey == nil {
		return nil, fmt.Errorf("IndexForeignKey: capability %q carries no foreign-key path", cap.String())
	}
	return c.ForeignKey(value, common.PathVariable(groupKey))
}

// indexRepo reproduces the old repositoryPath + website/library repo IndexSet: it
// rebuilds the repo path from the instance's provider + repository-id wire keys and
// Set()s the repo type at <repo>/type and the IndexValue at <repo>/resource/<id>.
func indexRepo(ic *IndexCtx, index object.Object[object.Refrence], repoType repositorytype.Type) error {
	provider, err := ic.Obj.GetString("provider")
	if err != nil {
		return fmt.Errorf("git provider is not a string: %w", err)
	}
	repoId, err := ic.Obj.GetString("repository-id")
	if err != nil {
		return fmt.Errorf("git repository is not a string: %w", err)
	}
	rp, err := methods.GetRepositoryPath(provider, repoId, ic.Project)
	if err != nil {
		return fmt.Errorf("getting repository path for %s failed with %w", repoId, err)
	}
	index.Set(rp.Type().String(), repoType)
	index.Set(rp.Resource(ic.Id).String(), ic.IndexValue.String())
	return nil
}

// indexByScope reproduces the old messaging websocket IndexLinkRaw: it computes the
// scope path for cap and appends the IndexValue at path.String() verbatim (no
// Links() suffix), so every instance of the scope aggregates under one key.
func indexByScope(ic *IndexCtx, index object.Object[object.Refrence], cap engine.Capability) error {
	c, ok := cap.(*Cap)
	if !ok || c.ScopePath == nil {
		return fmt.Errorf("IndexByScope: capability %q carries no scope path", cap.String())
	}
	p, err := c.ScopePath(ic.Project, ic.App)
	if err != nil {
		return fmt.Errorf("getting scope path for %q failed with %w", cap.String(), err)
	}
	appendLink(index, p.String(), ic.IndexValue.String())
	return nil
}

// indexPlaceholder reproduces the old domain nil-placeholder IndexSet: it reverses
// the fqdn at keyField into the group's basic path and, only when the key's Links()
// bucket is currently absent, Set()s it to nil.
func indexPlaceholder(ic *IndexCtx, index object.Object[object.Refrence], groupKey, keyField string) error {
	fqdn, err := ic.Obj.GetString(keyField)
	if err != nil {
		return fmt.Errorf("domain %s is not a string: %w", keyField, err)
	}
	p, err := methods.ReversedFqdnBasicPath(fqdn, common.PathVariable(groupKey))
	if err != nil {
		return fmt.Errorf("getting basic path for domain failed with %w", err)
	}
	key := p.Versioning().Links().String()
	if index.Get(key) == nil {
		index.Set(key, nil)
	}
	return nil
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

	byNameCap, _ := gi.iter.Meta["indexByName"].(engine.Capability)
	foreignKey, hasForeignKey := gi.iter.Meta["indexForeignKey"].(foreignKeyIndex)
	repoType, hasRepo := gi.iter.Meta["indexRepo"].(repositorytype.Type)
	_, hasName := gi.iter.Meta["indexName"].(bool)
	scopeCap, _ := gi.iter.Meta["indexByScope"].(engine.Capability)
	placeholderField, hasPlaceholder := gi.iter.Meta["indexPlaceholder"].(string)

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

		if hasForeignKey {
			if err := indexForeignKey(ic, index, gi.groupKey, foreignKey); err != nil {
				return nil, fmt.Errorf("index foreign key for %s %s failed with %w", gi.groupKey, id, err)
			}
		}

		if hasRepo {
			if err := indexRepo(ic, index, repoType); err != nil {
				return nil, fmt.Errorf("index repo for %s %s failed with %w", gi.groupKey, id, err)
			}
		}

		if hasName {
			index.Set(methods.NameIndex(ic.Id, common.PathVariable(gi.groupKey)).String(), ic.Name)
		}

		if scopeCap != nil {
			if err := indexByScope(ic, index, scopeCap); err != nil {
				return nil, fmt.Errorf("index by scope for %s %s failed with %w", gi.groupKey, id, err)
			}
		}

		if hasPlaceholder {
			if err := indexPlaceholder(ic, index, gi.groupKey, placeholderField); err != nil {
				return nil, fmt.Errorf("index placeholder for %s %s failed with %w", gi.groupKey, id, err)
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
