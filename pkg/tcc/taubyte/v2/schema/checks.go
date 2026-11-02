package schema

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/interp"
)

// The cross-resource checks, run by tcc over the compiled object: each needs a
// second resource to answer, which an attribute Validator cannot. What tcc
// cannot know becomes a NextValidation answered by the caller's Resolver.
// Broken cannot work, Check is probably not meant, Note is a fact worth seeing.

type Severity string

const (
	Broken Severity = "broken"
	Check  Severity = "check"
	Note   Severity = "note"
)

type Finding struct {
	Severity Severity
	Kind     string // the resource kind, e.g. "application"
	Name     string
	Scope    string // namespace path, "" at the project root
	Title    string
	Detail   string
	// ID is the compiled resource this finding is about, so a caller holding the
	// compiled object can point back at it directly rather than re-resolving
	// Kind/Name/Scope into an id itself. "" when there genuinely is none to point at —
	// an unresolved reference, or two candidates for one slot with no single winner.
	ID string
}

func (f Finding) String() string {
	where := f.Kind + "/" + f.Name
	if f.Scope != "" {
		where += " in " + f.Scope
	}
	return fmt.Sprintf("%s: %s: %s", f.Severity, where, f.Title)
}

// Resolver answers what tcc cannot see, nil when fine. The stub says yes to
// everything so an unwired check never blocks a compile.
type Resolver interface {
	// ZoneDelegated reports whether this cloud is authoritative for the zone apex.
	ZoneDelegated(fqdn string) error
	// ImagePullable reports whether an image reference can be pulled with the
	// credential named, "" meaning anonymously.
	ImagePullable(image, pullSecret string) error
}

type allGood struct{}

func (allGood) ZoneDelegated(string) error         { return nil }
func (allGood) ImagePullable(string, string) error { return nil }

// StubResolver answers yes to everything it is asked.
func StubResolver() Resolver { return allGood{} }

type compiled struct {
	kind, name, scope string
	// id is the KEY a compiled resource is filed under, which is where its id lives —
	// a compiled resource does not carry it as a field.
	id     string
	fields map[string]any
}

// AuditOption narrows what an audit can know about the compile it is reading.
type AuditOption func(*auditOf)

type auditOf struct {
	environment string
	regions     Regions
	// lowered is what the compile lowered, so what a shell produced is placed with it.
	lowered interp.Lowered
}

func auditOptions(opts []AuditOption) auditOf {
	in := auditOf{regions: WithNoRegions()}
	for _, opt := range opts {
		opt(&in)
	}
	if in.regions == nil {
		in.regions = WithNoRegions()
	}
	return in
}

// projectOf is the project inside a compiled object, or the object when it is one.
func projectOf(obj map[string]any) map[string]any {
	if inner, ok := obj[interp.ProjectKey].(map[string]any); ok {
		return inner
	}
	return obj
}

// AuditIn names the environment the object was compiled for, which the object
// itself no longer says; without it the region checks cannot pick a list.
func AuditIn(environment string) AuditOption {
	return func(a *auditOf) { a.environment = environment }
}

// Audit runs every cross-resource check over a compiled object.
func Audit(obj map[string]any, r Resolver, opts ...AuditOption) []Finding {
	if r == nil {
		r = StubResolver()
	}
	in := auditOptions(opts)
	in.lowered = interp.LoweredOf(obj)
	root := projectOf(obj)

	all := collect(root, "")
	byKind := map[string][]compiled{}
	for _, res := range all {
		byKind[res.kind] = append(byKind[res.kind], res)
	}
	pkgs := packagesByID(byKind)

	var out []Finding
	out = append(out, checkReachable(all, byKind)...)
	out = append(out, checkPortsAreFed(all)...)
	out = append(out, checkDelivery(all, pkgs)...)
	out = append(out, checkVariables(all)...)
	out = append(out, checkPackages(all)...)
	out = append(out, checkWorkloadPackages(all, pkgs)...)
	out = append(out, checkTransformerWorlds(all)...)
	out = append(out, checkBusIsGuarded(all, byKind)...)
	out = append(out, checkDelegatedFills(obj, all)...)
	out = append(out, checkMountedSites(all)...)
	out = append(out, checkHostWorlds(all)...)
	out = append(out, checkHandlerPorts(all)...)
	out = append(out, checkAccessDuplicates(all)...)
	out = append(out, checkDatabaseOps(all)...)
	out = append(out, checkWorkflows(all)...)
	out = append(out, checkRegions(root, all, in)...)
	// The record of what the compile produced sits BESIDE the project, so it is read
	// from the object rather than from the project inside it.
	out = append(out, checkAuthoredBuiltins(interp.LoweredOf(obj), all)...)
	out = append(out, checkLoweringCollisions(root, interp.LoweredOf(obj), all)...)
	out = append(out, checkLoweredLinks(root, all)...)
	out = append(out, checkExternal(all, r)...)

	sort.SliceStable(out, func(i, j int) bool {
		rank := map[Severity]int{Broken: 0, Check: 1, Note: 2}
		return rank[out[i].Severity] < rank[out[j].Severity]
	})
	return out
}

// ScopeKindNames is every kind whose instances are scopes: namespace, and every
// kind lowering into a container of its own. Exported so a reader resolving
// `<scope-kind>/<id>` segments does not assume `namespace`.
func ScopeKindNames() []string {
	out := make([]string, 0, len(scopeGroups()))
	for kind := range scopeGroups() {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}

// scopeGroups is every group whose instances are scopes in the COMPILED shape. A
// lowerable kind joins namespace here without becoming another vocabulary kind.
func scopeGroups() map[string]bool {
	out := map[string]bool{}
	for _, key := range interp.ContainerKeys(kindRootNode()) {
		out[key] = true
	}
	return out
}

// collect walks the compiled object into a flat list, carrying each resource's scope.
// Scopes nest, so this recurses on them rather than assuming depth.
func collect(scope map[string]any, path string) []compiled {
	scopes := scopeGroups()
	groups := compiledGroups()
	var out []compiled
	for group, val := range scope {
		if !groups[group] {
			continue
		}
		instances, ok := val.(map[string]any)
		if !ok {
			continue
		}
		for id, v := range instances {
			res, ok := v.(map[string]any)
			if !ok {
				continue
			}
			// One key, two things: inside a kind that is also a container, a group key holds
			// produced resources and access entries.
			if !interp.HeldResource(kindRootNode(), group, id) {
				continue
			}
			name, _ := res["name"].(string)
			if name == "" {
				continue
			}
			if scopes[group] {
				if isResourceKind(group) {
					out = append(out, compiled{kind: group, name: name, scope: path, id: id, fields: res})
				}
				child := path
				child = strings.TrimPrefix(path+"/"+name, "/")
				out = append(out, collect(res, child)...)
				continue
			}
			out = append(out, compiled{kind: group, name: name, scope: path, id: id, fields: res})
		}
	}
	return out
}

func compiledGroups() map[string]bool {
	out := map[string]bool{}
	for _, group := range KindRoot() {
		key, _ := group.Match.(string)
		if key != "" {
			out[key] = true
		}
	}
	return out
}

func isResourceKind(kind string) bool {
	for _, group := range KindRoot() {
		key, _ := group.Match.(string)
		if key != kind || len(group.Children) == 0 {
			continue
		}
		_, ok := group.Children[0].Meta["resource"].([4]string)
		return ok
	}
	return false
}

// at reads a value nested under a key — a reference is a block, so its id sits at
// <field>/name rather than on the field itself.
func (r compiled) at(segments ...string) string {
	// A caller may hand a joined key — InboundBlocks yields "route/handler" — so the
	// segments are flattened first. A field's identity IS its key joined, so the two
	// spellings have to mean the same walk.
	var flat []string
	for _, seg := range segments {
		flat = append(flat, strings.Split(seg, "/")...)
	}
	s, _ := valueAt(r.fields, flat...).(string)
	return s
}

// valueAt walks one level of an object per segment. An attribute's identity is its key
// joined by "/", so `fieldValue(fields, "memory/embedder/name")` and this are the same
// question asked two ways.
func valueAt(fields map[string]any, segments ...string) any {
	cur := fields
	for _, seg := range segments[:len(segments)-1] {
		next, ok := cur[seg].(map[string]any)
		if !ok {
			return nil
		}
		cur = next
	}
	return cur[segments[len(segments)-1]]
}

// fieldValue reads a field by its identity — the key, joined.
func fieldValue(fields map[string]any, field string) any {
	return valueAt(fields, strings.Split(field, "/")...)
}

func (r compiled) str(key string) string {
	s, _ := fieldValue(r.fields, key).(string)
	return s
}

// listAt reads a string list, []string or, through JSON, []any, off any object, because
// a SCOPE is not a compiled resource —
// only a container that is also a kind ends up in the flat list, so the allowance on a
// namespace has to be read off the tree.
func listAt(fields map[string]any, key string) []string {
	switch v := fieldValue(fields, key).(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// elements reads a list of objects — a DefineList group — off the compiled object.
func (r compiled) elements(key string) []map[string]any {
	items, ok := fieldValue(r.fields, key).([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if m, isMap := it.(map[string]any); isMap {
			out = append(out, m)
		}
	}
	return out
}

// field reads a string off a list element.
func field(el map[string]any, key string) string {
	s, _ := el[key].(string)
	return s
}

func (r compiled) block(key string) map[string]any {
	m, _ := fieldValue(r.fields, key).(map[string]any)
	return m
}

// checkReachable: an ingress names a workload, or something delivers work to it, or
// nothing ever starts it.
func checkReachable(all []compiled, byKind map[string][]compiled) []Finding {
	fronted := map[string]bool{}
	for _, res := range all {
		// The ways something is reached, derived from the references whose kind the
		// author picks rather than listed here — a list is a copy, and a copy goes
		// stale the next time a kind gains an inbound block.
		for _, prefix := range InboundBlocks() {
			kind, name := res.at(prefix, "kind"), res.at(prefix, "name")
			if kind != "" && name != "" {
				fronted[kind+"/"+name] = true
			}
		}
	}

	var out []Finding
	for _, res := range all {
		if !ServedByRoute(res.kind) || res.kind == "gateway" || res.kind == "function" {
			continue
		}
		if fronted[res.kind+"/"+res.name] {
			continue
		}
		if consumes(res) {
			out = append(out, Finding{Note, res.kind, res.name, res.scope,
				"nothing fronts it, and nothing needs to",
				"It is started by work arriving rather than by a request. Listed so the absence is a decision you can see rather than one you have to remember.", res.id})
			continue
		}
		out = append(out, Finding{Broken, res.kind, res.name, res.scope,
			"nothing reaches it",
			"No gateway route and no proxy names it, and it consumes nothing. It runs and answers no one.", res.id})
	}
	return out
}

// consumes reports whether work ARRIVES at this resource — a delivered verb on any
// access block it declares. Read off the verb table, so a new delivered verb counts
// without an edit here.
func consumes(res compiled) bool {
	for group, val := range res.fields {
		entries, ok := val.(map[string]any)
		if !ok {
			continue
		}
		for _, e := range entries {
			entry, ok := e.(map[string]any)
			if !ok {
				continue
			}
			for _, op := range ops(entry) {
				for _, v := range OpsOf(group) {
					if v.Name == op && v.Delivered {
						return true
					}
				}
			}
		}
	}
	return false
}

func ops(entry map[string]any) []string {
	switch v := entry["ops"].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// checkPortsAreFed: a port nothing targets is dead config that reads as working.
func checkPortsAreFed(all []compiled) []Finding {
	var out []Finding
	for _, res := range all {
		ports := res.block("ports")
		if len(ports) == 0 {
			continue
		}
		used := map[string]bool{}
		for _, probe := range []string{"readiness/http/port", "liveness/http/port"} {
			if p := res.at(probe, "http", "path"); p != "" {
				used[p] = true
			}
		}
		for _, val := range res.fields {
			entries, ok := val.(map[string]any)
			if !ok {
				continue
			}
			for _, e := range entries {
				if entry, ok := e.(map[string]any); ok {
					if p, _ := fieldValue(entry, "deliver/port").(string); p != "" {
						used[p] = true
					}
				}
			}
		}
		for _, other := range all {
			for _, prefix := range InboundBlocks() {
				if other.at(prefix, "name") == res.name {
					if p := other.at(prefix, "port"); p != "" {
						used[p] = true
					}
				}
			}
		}
		for port := range ports {
			if used[port] {
				continue
			}
			out = append(out, Finding{Check, res.kind, res.name, res.scope,
				fmt.Sprintf("port %q is declared and nothing reaches it", port),
				"No ingress names it, no probe polls it and nothing is delivered to it. Either something should target this port by name, or the workload is not listening on what it says it is.", res.id})
		}
	}
	return out
}

// checkDelivery: a container has no host ABI, so an item has to be posted to a port
// it declared. A WASM workload is dispatched and needs none.
func checkDelivery(all []compiled, pkgs map[string]compiled) []Finding {
	var out []Finding
	for _, res := range all {
		if !runsAsContainer(res, pkgs) {
			continue // dispatched, not posted to
		}
		for group, val := range res.fields {
			entries, ok := val.(map[string]any)
			if !ok {
				continue
			}
			for target, e := range entries {
				entry, ok := e.(map[string]any)
				if !ok {
					continue
				}
				delivered := false
				for _, op := range ops(entry) {
					for _, v := range OpsOf(group) {
						if v.Name == op && v.Delivered {
							delivered = true
						}
					}
				}
				if !delivered {
					continue
				}
				if p, _ := fieldValue(entry, "deliver/port").(string); p != "" {
					continue
				}
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("receives from %s/%s and declares no deliver port", group, target),
					"A container has no host ABI, so each item has to be posted to a port it declared. Without deliver.port nothing arrives and the workload idles.", res.id})
			}
		}
	}
	return out
}

// runsAsContainer: whether something must be posted to a port. The tier is the
// type of the package the workload names; an image field is not the test, since
// a Dockerfile-built package has none.
func runsAsContainer(res compiled, pkgs map[string]compiled) bool {
	return tierOf(res, pkgs) == ociPackage
}

// packagesByID is every package, by the id a workload's reference compiles to.
func packagesByID(byKind map[string][]compiled) map[string]compiled {
	out := map[string]compiled{}
	for _, p := range byKind["package"] {
		out[p.id] = p
	}
	return out
}

// tierOf is the tier a workload runs on: its package's type's, or the kind's own
// where it has one tier and names no package. "" when neither says.
func tierOf(res compiled, pkgs map[string]compiled) string {
	if p, ok := pkgs[res.at("package", "name")]; ok {
		return p.str("type")
	}
	if engines := EnginesOf(res.kind); len(engines) == 1 {
		return engines[0]
	}
	return ""
}

// checkVariables: a variable delivered as a file needs a path to be mounted at.
func checkVariables(all []compiled) []Finding {
	var out []Finding
	for _, res := range all {
		for name, v := range res.block("variables") {
			entry, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if as, _ := entry["as"].(string); as == "file" {
				if p, _ := entry["path"].(string); p == "" {
					out = append(out, Finding{Broken, res.kind, res.name, res.scope,
						fmt.Sprintf("variable %q is delivered as a file and names no path", name),
						"A file has to be mounted somewhere the workload can open it.", res.id})
				}
			}
		}
	}
	return out
}

// checkPackages: what a package's type, source and build say has to agree, across
// fields no single switch reaches.
//
//	an image AND a build          the image is finished; nothing the build says would run
//	an image that is not oci      an image is what an oci package is
//	a Dockerfile that is not oci  a Dockerfile makes an image
//	oci with neither              there is no image and no recipe for one
//	steps with no output          the build would leave nothing to take
//	a Dockerfile with an output   its artifact is the image it makes
func checkPackages(all []compiled) []Finding {
	var out []Finding
	broken := func(res compiled, what, why string) {
		out = append(out, Finding{Broken, res.kind, res.name, res.scope, what, why, res.id})
	}
	for _, res := range all {
		if res.kind != "package" {
			continue
		}
		typ, way, from := res.str("type"), res.at("build", "kind"), res.at("source", "kind")
		switch {
		case from == "image" && way != "":
			broken(res, "takes its artifact from an image and also declares a build",
				"The image is already built, so nothing the build says would run. Take one out: name the image, or the build that produces one.")
		case from == "image" && typ != ociPackage:
			broken(res, fmt.Sprintf("names an image and is of type %s", typ),
				"An image is what a package of type oci is. Make it oci, or point source at the code this type is built from.")
		case way == dockerfileBuild && typ != ociPackage:
			broken(res, fmt.Sprintf("is built from a Dockerfile and is of type %s", typ),
				"A Dockerfile makes an image, which is a package of type oci. Build a "+typ+" package with steps.")
		case typ == ociPackage && way == "" && from != "image" && from != "builtin":
			broken(res, "is of type oci and names no image and no build",
				"An oci package is an image: point source at one somebody built, or declare the build that makes it.")
		}
		switch output := res.str("output"); {
		case way == stepsBuild && output == "":
			broken(res, "is built with steps and names no output",
				"Steps leave files behind; output says which of them is the artifact, e.g. \"dist\" or \"target/release/app.wasm\".")
		case way == dockerfileBuild && output != "":
			broken(res, "is built from a Dockerfile and names an output",
				"A Dockerfile build's artifact is the image it makes. Take output out.")
		}
	}
	return out
}

// checkWorkloadPackages: a workload runs on its package's tier, which its kind has
// to be able to run, and a wasm component is entered through a named export.
func checkWorkloadPackages(all []compiled, pkgs map[string]compiled) []Finding {
	var out []Finding
	for _, res := range all {
		p, ok := pkgs[res.at("package", "name")]
		if !ok {
			continue // nothing named, or unresolved: the reference's to refuse
		}
		tier := p.str("type")
		if engines := EnginesOf(res.kind); !slices.Contains(engines, tier) {
			out = append(out, Finding{Broken, res.kind, res.name, res.scope,
				fmt.Sprintf("runs package %s, which is of type %s", p.name, p.str("type")),
				fmt.Sprintf("A %s runs on %s. Name a package that one of those runs.", res.kind, strings.Join(engines, " or ")), res.id})
			continue
		}
		if tier == wasmPackage && res.at("execution", "call") == "" {
			out = append(out, Finding{Broken, res.kind, res.name, res.scope,
				fmt.Sprintf("runs the wasm package %s and names no entrypoint", p.name),
				"A component exports many functions and the host calls one. Name it in execution.call.", res.id})
		}
	}
	return out
}

// checkMountedSites: a static site built for / and mounted at /docs answers every page
// with a 404 unless the prefix is stripped, and the config looks entirely correct while
// it does. tcc knows both halves — the route's path and that the handler is a website —
// so it is the one that can say.
func checkMountedSites(all []compiled) []Finding {
	var out []Finding
	for _, res := range all {
		if res.at("route", "handler", "kind") != "website" {
			continue
		}
		path := res.at("route", "path")
		if path == "" || path == "/" {
			continue
		}
		if strip, _ := valueAt(res.fields, "route", "strip_prefix").(bool); strip {
			continue
		}
		out = append(out, Finding{Check, res.kind, res.name, res.scope,
			fmt.Sprintf("serves a website at %q and does not strip the prefix", path),
			fmt.Sprintf("The site receives %s/... and was almost certainly built for /, so every page is a file it does not have. Set route.strip-prefix, or build the site with %s as its base.", path, path), res.id})
	}
	return out
}

// checkHostWorlds: every USE of a function is a place tcc can see, and the function
// says which world it was built against. Comparing the two is what turns "compiles
// clean, fails on live traffic" into an error at compile.
func checkHostWorlds(all []compiled) []Finding {
	// "scope\x00name" -> declared world and the function's own compiled id.
	world := map[string]hostWorld{}
	for _, res := range all {
		if res.kind != "function" {
			continue
		}
		w := res.str("implements")
		if w == "" {
			w = "handler"
		}
		world[res.scope+"\x00"+res.name] = hostWorld{w, res.id}
	}

	// A use names a function and expects one world of it.
	// own is where the use was written; ns is what it said about where to look.
	type use struct{ name, own, ns, wants, where string }
	var uses []use
	for _, res := range all {
		// AN AUTHORIZER'S WORLD DEPENDS ON WHAT IS ASKING. A kind with Ops()
		// names one that is asked `who: caller` about a workload; a gateway
		// names one that is asked about the REQUEST, because whoever reached
		// the edge is not a workload and has none of a caller's fields.
		authorizerWorld := "authorizer"
		if res.kind == "gateway" {
			authorizerWorld = "http-authorizer"
		}
		if n := res.at("authorizer", "name"); n != "" {
			uses = append(uses, use{n, res.scope, res.at("authorizer", "namespace"), authorizerWorld, res.kind + "/" + res.name + " authorizer"})
		}
		// AND A ROUTE MAY NAME ITS OWN, beside the gateway-wide one above --
		// the second site routerAttributes already has for a router.
		if n := res.at("route", "authorizer", "name"); n != "" && res.kind == "gateway" {
			uses = append(uses, use{n, res.scope, res.at("route", "authorizer", "namespace"), "http-authorizer", res.kind + "/" + res.name + " route authorizer"})
		}
		wants := ""
		switch res.kind {
		case "gateway":
			wants = "gateway-transformer"
		case "proxy":
			wants = "proxy-transformer"
		}
		if wants != "" {
			for _, el := range res.elements("transformers") {
				uses = append(uses, use{field(el, "name"), res.scope, field(el, "namespace"), wants, res.kind + "/" + res.name + " transformers"})
			}
		}
		if res.at("route", "handler", "kind") == "function" {
			uses = append(uses, use{res.at("route", "handler", "name"), res.scope, res.at("route", "handler", "namespace"), "handler", res.kind + "/" + res.name + " route"})
		}
		// A ROUTER ANSWERS WITH A DESTINATION, not a response, so it is built against
		// its own world — and a gateway's is not a proxy's: one is handed a parsed
		// request, the other bytes on a connection.
		if n := res.at("router", "name"); n != "" {
			w := ""
			switch res.kind {
			case "gateway":
				w = "gateway-router"
			case "proxy":
				w = "proxy-router"
			}
			if w != "" {
				uses = append(uses, use{n, res.scope, res.at("router", "namespace"), w, res.kind + "/" + res.name + " router"})
			}
		}
		// A route may hand its decision to a router, a second site beside the
		// gateway-wide one.
		if n := res.at("route", "router", "name"); n != "" && res.kind == "gateway" {
			uses = append(uses, use{n, res.scope, res.at("route", "router", "namespace"), "gateway-router", res.kind + "/" + res.name + " route router"})
		}
		// An admission validator: instances/validator/name is its only use site, and its
		// world is the same whatever holds the instances.
		if n := res.at("instances", "validator", "name"); n != "" {
			uses = append(uses, use{n, res.scope, res.at("instances", "validator", "namespace"), "instance-validator", res.kind + "/" + res.name + " instances validator"})
		}
	}

	var out []Finding
	for _, u := range uses {
		found, known := lookupWorld(world, u.name, u.own, u.ns)
		if !known {
			// A name resolving to nothing compiles clean as an empty field, so it is
			// reported here: the transformer, router or authorizer would never run.
			out = append(out, Finding{Broken, "function", u.name, u.ns,
				fmt.Sprintf("is named as a %s by %s, and does not exist", u.wants, u.where),
				"The name is the key of the entry, and a key is not a reference the compiler resolves — so this one names nothing and the step never runs.", ""})
			continue
		}
		if found.w == u.wants {
			continue
		}
		out = append(out, Finding{Broken, "function", u.name, u.ns,
			fmt.Sprintf("is used as a %s by %s, and implements %s", u.wants, u.where, found.w),
			"Those are different host interfaces. The function will be handed a world it was not built to read, which compiles clean and fails on live traffic.", found.id})
	}
	return out
}

// hostWorld is a function's declared world and its own compiled id.
type hostWorld struct{ w, id string }

// lookupWorld resolves a named function the way every name is resolved.
func lookupWorld(world map[string]hostWorld, name, own, ns string) (hostWorld, bool) {
	for _, scope := range scopeChain(own, ns) {
		if w, ok := world[scope+"\x00"+name]; ok {
			return w, true
		}
	}
	return hostWorld{}, false
}

// checkWorkflows: what only a workflow can get wrong. A step naming a function that
// does not exist is NOT here — that one is a link in the lowering, and checkLoweredLinks
// reports it generically, with the trail.
func checkWorkflows(all []compiled) []Finding {
	var out []Finding
	for _, res := range all {
		if res.kind != "workflow" {
			continue
		}
		// A workflow is started, never self-starting: v2 has no triggers, so what runs
		// one is whatever declares `start` on it — a cron, a function, a gateway route.
		// Nothing declaring it is a pipeline that is never fed, which is the same
		// silence as a workload no ingress names.
		if !startedBy(all, res) {
			out = append(out, Finding{Broken, res.kind, res.name, res.scope,
				"nothing starts it, so its first step is never fed",
				"A workflow does not run itself. Something has to declare `start` on it — a cron, a function, or a route — the same way anything else in this vocabulary is reached.", res.id})
		}

		steps := res.elements("steps")
		if len(steps) == 0 {
			out = append(out, Finding{Broken, res.kind, res.name, res.scope,
				"has no steps, so it lowers to nothing and starting it does nothing",
				"A workflow IS its steps: with none, the queues it would become are not created and a start has nowhere to go.", res.id})
			continue
		}

		named := map[string]bool{}
		for _, step := range steps {
			named[field(step, "name")] = true
		}

		// Which key is present is the step's type, so exactly one must be: a captured
		// branch inside a list element would not decompile, so this is a check rather
		// than an Either().
		for _, step := range steps {
			name := field(step, "name")
			// a step element nests, so the call's fields are a level down
			_, calls := fieldValue(step, "call/function").(string)
			_, waits := fieldValue(step, "wait").(string)
			switch {
			case calls && waits:
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("step %q both calls a function and waits", name),
					"A step does one thing. Give the wait a step of its own, before or after the one that calls.", res.id})
			case !calls && !waits:
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("step %q does nothing: it neither calls nor waits", name),
					"A step is what it opens: `call:` to run a function, `wait:` to pause. With neither, the run passes through it and the queue it became is never consumed.", res.id})
			}
		}
		// A step that is BOTH the next in the line and somebody's error target runs on
		// success as well as on failure, which is almost never what a compensation step
		// is for: "put the stock back" after a charge that WORKED. The line has one
		// order and no branch, so the two roles cannot be told apart — the config reads
		// correctly and does the wrong thing.
		for i, step := range steps {
			if i == 0 {
				continue
			}
			name := field(step, "name")
			for _, other := range steps {
				if field(other, "on_error") != name || field(steps[i-1], "name") != field(other, "name") {
					continue
				}
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("step %q runs after %q AND is where %q sends failure", name, field(other, "name"), field(other, "name")),
					"It would run when the step before it succeeded and when it failed, which is not what a compensation step is for. A line has one order and no branch: give the handler its own workflow, or let failure land in the dead-letter queue.", res.id})
			}
		}

		// A start has to land on a step of THIS workflow: it is what the lowering writes
		// into the entry record, and a name that is not a step is a queue that was never
		// produced.
		if start := res.str("start"); start != "" && !named[start] {
			out = append(out, Finding{Broken, res.kind, res.name, res.scope,
				fmt.Sprintf("starts at %q, which is not a step of this workflow", start),
				"A start lands on the named step's queue. Naming something else means a start goes to a queue that was never produced.", res.id})
		}

		for _, step := range steps {
			name := field(step, "name")
			// the default names no step on purpose: it is the terminal queue
			if target := field(step, "on_error"); target != "" && target != terminalStep && !named[target] {
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("step %q sends failure to %q, which is not a step of this workflow", name, target),
					"On error the run would jump to a step that does not exist, which is the same as having no handler at all — except it reads like it has one.", res.id})
			}
		}
	}
	return out
}

// startedBy reports whether anything declares a start on this workflow, resolved the
// way every access entry is: the entry's namespace, or the accessor's own scope and
// then its parents.
func startedBy(all []compiled, target compiled) bool {
	for _, res := range all {
		for label, e := range res.block(target.kind) {
			entry, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if entryName(label, entry) != target.name {
				continue
			}
			for _, scope := range scopeChain(res.scope, field(entry, "namespace")) {
				if scope == target.scope {
					return true
				}
			}
		}
	}
	return false
}

// scopeAllowance is one scope's own `regions` line, and where it sits.
type scopeAllowance struct {
	parent   string // the scope path above this one; "" is the project root
	kind     string // what KIND of scope it is, for the finding
	name, id string
	regions  []string // as declared here; empty means "whatever the parent allows"
}

// scopeAllowances records the allowance each scope declares, keyed by the scope
// path collect() files resources under. A namespace is a container, not a kind,
// so it never appears in `all`; and the walk covers every group ContainerKeys
// names, since a chain also passes through levels like `workflow/checkout`.
func scopeAllowances(root map[string]any) map[string]scopeAllowance {
	scopes := scopeGroups()
	out := map[string]scopeAllowance{"": {regions: listAt(root, "regions")}}
	var walk func(scope map[string]any, path string)
	walk = func(scope map[string]any, path string) {
		for group, val := range scope {
			if !scopes[group] {
				continue
			}
			instances, ok := val.(map[string]any)
			if !ok {
				continue
			}
			for id, v := range instances {
				res, ok := v.(map[string]any)
				if !ok || !interp.HeldResource(kindRootNode(), group, id) {
					continue
				}
				name, _ := res["name"].(string)
				if name == "" {
					continue
				}
				child := strings.TrimPrefix(path+"/"+name, "/")
				out[child] = scopeAllowance{parent: path, kind: group, name: name, id: id,
					regions: listAt(res, "regions")}
				walk(res, child)
			}
		}
	}
	walk(root, "")
	return out
}

// effectiveAllowance is a scope's own list if it declared one, else the nearest one
// above it. Nothing anywhere means no ceiling — wherever the Regions places it.
func effectiveAllowance(scopes map[string]scopeAllowance, path string) []string {
	for {
		s, known := scopes[path]
		if !known {
			return nil
		}
		if len(s.regions) > 0 {
			return s.regions
		}
		if path == "" {
			return nil
		}
		path = s.parent
	}
}

// checkRegions: a scope narrows the regions it inherits and never widens them,
// and a resource is placed inside its chain's allowance. Both span documents, so
// only the audit sees both ends; both are Broken, since a widening runs a child
// where its parent forbade and an outside placement has nowhere to run. A chain
// with no allowance anywhere is the zero-config project and is not checked.
func checkRegions(root map[string]any, all []compiled, in auditOf) []Finding {
	scopes := scopeAllowances(root)
	out := checkEnvironmentRegions(root)

	paths := make([]string, 0, len(scopes))
	for path := range scopes {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		s := scopes[path]
		if path == "" || len(s.regions) == 0 {
			continue
		}
		above := effectiveAllowance(scopes, s.parent)
		if len(above) == 0 {
			continue
		}
		for _, r := range s.regions {
			if slices.Contains(above, r) {
				continue
			}
			out = append(out, Finding{Broken, s.kind, s.name, s.parent,
				fmt.Sprintf("allows region %q, which the scope above it does not", r),
				"A scope narrows the regions it inherits and never widens them: a child may not run where its parent said nothing may.", s.id})
		}
	}

	for _, res := range all {
		allowed := effectiveAllowance(scopes, res.scope)
		// Every field a label can be written in. They are read by identity — the
		// attribute's key, joined — so a branch that is not taken simply holds nothing.
		for _, r := range placedIn(res) {
			if len(allowed) == 0 || slices.Contains(allowed, r) {
				continue
			}
			out = append(out, Finding{Broken, res.kind, res.name, res.scope,
				fmt.Sprintf("is placed in region %q, which its scope does not allow", r),
				"Placement is bounded by every scope above it. Widen the scope's regions, or place this within them.", res.id})
		}

		// Failover's two regions must differ: a standby equal to its main fails over to
		// what is already gone. And a policy does not change between environments:
		// `failover:` beside a `pinned_preview:` would resolve one policy with the other
		// branch's regions.
		if res.str("regions/policy") == "failover" && len(listAt(res.fields, "regions/in")) > 0 {
			out = append(out, Finding{Broken, res.kind, res.name, res.scope,
				"fails over in one environment and is pinned in another",
				"A policy is the same in every deployment. Write this resource for that environment instead of suffixing another branch.", res.id})
		}

		if main, standby := res.str("regions/main"), res.str("regions/standby"); main != "" && main == standby {
			out = append(out, Finding{Broken, res.kind, res.name, res.scope,
				"fails over from " + main + " to itself",
				"Failover takes two regions; the standby exists for when the main is gone.", res.id})
		}
	}

	out = append(out, place(root, all, in)...)
	return append(out, checkPlacedInsideTheEnvironment(root, all, in.environment)...)
}

// checkEnvironmentRegions: an environment narrows the project's allowance and
// never widens it, as a namespace does; otherwise `regions: [ap]` under a
// project allowing only `eu` would be honoured at runtime.
func checkEnvironmentRegions(root map[string]any) []Finding {
	allowed := listAt(root, "regions")
	if len(allowed) == 0 {
		return nil
	}

	var out []Finding
	for _, env := range EnvironmentsIn(root) {
		for _, region := range env.Regions {
			if slices.Contains(allowed, region) {
				continue
			}
			out = append(out, Finding{Broken, "environments", env.Name, "",
				fmt.Sprintf("keeps state in region %q, which the project does not allow", region),
				"An environment narrows the project's regions and never widens them: a deployment may not keep state where the project said nothing may.", ""})
		}
	}
	return out
}

// checkPlacedInsideTheEnvironment: a resource is placed within the regions its
// environment keeps state in. Silent without the environment's name (AuditIn):
// the placement read is already resolved for this compile's environment.
func checkPlacedInsideTheEnvironment(root map[string]any, all []compiled, environment string) []Finding {
	if environment == "" {
		return nil
	}

	// A shadow keeps no state of its own, so it is bounded by the environment it was
	// written inside; see environmentRegions.
	allowed := environmentRegions(root, environment)
	if len(allowed) == 0 {
		return nil
	}

	var out []Finding
	for _, res := range all {
		for _, region := range placedIn(res) {
			if slices.Contains(allowed, region) {
				continue
			}
			out = append(out, Finding{Broken, res.kind, res.name, res.scope,
				fmt.Sprintf("is placed in region %q, which %s does not keep state in", region, environment),
				"A deployment keeps its state in the regions it declares. Widen the environment's regions, or place this within them.", res.id})
		}
	}
	return out
}

// placedIn is every region a resource's block names across the branch it took:
// `pinned` and `cdn` carry a list, `failover` its two by name.
func placedIn(res compiled) []string {
	out := listAt(res.fields, "regions/in")
	for _, field := range []string{"regions/main", "regions/standby"} {
		if r := res.str(field); r != "" {
			out = append(out, r)
		}
	}
	return out
}

func checkLoweringCollisions(root map[string]any, produced interp.Lowered, all []compiled) []Finding {
	// What the compile PRODUCED is not authored, and the object holds both now. Without
	// this the check reported every generated resource as colliding with itself, which
	// is the audit accusing the compiler of its own work.
	authored := map[string]compiled{}
	for _, res := range all {
		if produced.Produced(res.id) {
			continue
		}
		authored[res.kind+"\x00"+res.scope+"\x00"+res.name] = res
	}

	var out []Finding
	for _, d := range LoweringPlan(root) {
		for _, res := range all {
			if res.kind+"/"+res.name != d.Of {
				continue
			}
			if collision, taken := authored[d.Kind+"\x00"+res.scope+"\x00"+d.Name]; taken {
				out = append(out, Finding{Broken, d.Kind, d.Name, res.scope,
					fmt.Sprintf("is authored here AND is what %s becomes", d.Of),
					"A transpiled resource takes the name too, and both would be the same key. Rename one — the authored one is the one you can see.", collision.id})
			}
		}
	}
	return out
}

// checkLoweredLinks reports a fault inside lowered config against what the author
// wrote: a workflow step naming a missing function leaves a produced queue with
// no consumer, so the finding names the workflow and step, with the lowering
// trail.
func checkLoweredLinks(root map[string]any, all []compiled) []Finding {
	// Keyed SCOPE-first, because lookupName walks up the scope and appends the rest of
	// the identity: the parts have to be in that order or every lookup misses.
	exists := map[string]bool{}
	for _, res := range all {
		exists[res.scope+"\x00"+res.kind+"\x00"+res.name] = true
	}

	plan := LoweringPlan(root)
	derived := map[string]bool{}
	for _, d := range plan {
		derived[d.Kind+"\x00"+d.Name] = true
	}

	var out []Finding
	for _, d := range plan {
		// A resource a lowering writes into has to exist: a step naming no function
		// leaves the produced queue with no consumer.
		for _, w := range d.Writes {
			if w.On.Kind == "" || derived[w.On.Kind+"\x00"+w.On.Name] {
				continue // the derived resource, or another part of the same lowering
			}
			scope := w.On.Scope
			if scope == "" {
				scope = d.Scope
			}
			if lookupName(exists, w.On.Kind+"\x00"+w.On.Name, scope) {
				continue
			}
			out = append(out, Finding{Broken, d.Source.Kind, d.Source.Name, d.Source.Scope,
				fmt.Sprintf("%s %s/%s, which does not exist", w.Value, w.On.Kind, w.On.Name),
				"Found in what this becomes rather than in what you wrote: " + d.Trail() +
					". The name is carried through the lowering unchanged, so fixing it where it is written fixes this.", d.Source.ID})
		}

		// So does the resource an access entry points at. The target is in the write's
		// path (`<kind>/<label>/ops`), and when the accessor is itself produced the path
		// is the only place a misspelling shows.
		for _, w := range d.Writes {
			if len(w.Path) != 3 || w.Path[2] != accessOps {
				continue
			}
			kind, name := w.Path[0], w.Path[1]
			if derived[kind+"\x00"+name] || !isResourceKind(kind) {
				continue
			}
			// the entry says where to look, and a bare name resolves outward from the
			// scope the accessor is in
			scope := d.Scope
			if ns := entryScopeOf(d.Writes, kind, name); ns != "" {
				scope = ns
			}
			if lookupName(exists, kind+"\x00"+name, scope) {
				continue
			}
			out = append(out, Finding{Broken, d.Source.Kind, d.Source.Name, d.Source.Scope,
				fmt.Sprintf("%s %s/%s, which does not exist", w.Value, kind, name),
				"Found in what this becomes rather than in what you wrote: " + d.Trail() +
					". The name is carried through the lowering unchanged, so fixing it where it is written fixes this.", d.Source.ID})
		}
	}
	return out
}

// entryScopeOf is the namespace a lowering wrote on the same access entry, when it wrote
// one: the pair of writes that make an entry are `<kind>/<label>/ops` and
// `<kind>/<label>/namespace`, and only the second says where the name resolves.
func entryScopeOf(writes []interp.Written, kind, name string) string {
	for _, w := range writes {
		if len(w.Path) == 3 && w.Path[0] == kind && w.Path[1] == name && w.Path[2] == accessScope {
			return w.Value
		}
	}
	return ""
}

// lookupName is lookupWorld's question without the world: does this key exist in this
// scope, or in one of its parents. The key carries whatever identity the caller needs
// beyond the scope — a bare name, or a kind and a name.
func lookupName(known map[string]bool, name, scope string) bool {
	scope = strings.TrimPrefix(scope, "/")
	for {
		if known[scope+"\x00"+name] {
			return true
		}
		if scope == "" {
			return false
		}
		if cut := strings.LastIndex(scope, "/"); cut >= 0 {
			scope = scope[:cut]
		} else {
			scope = ""
		}
	}
}

// checkAccessDuplicates: two labels in one access group resolving to the same
// target. Map order is not the author's, so whichever entry's ops are visited
// last would silently win.
func checkAccessDuplicates(all []compiled) []Finding {
	byName := map[string]compiled{}
	for _, res := range all {
		byName[res.kind+"\x00"+res.scope+"\x00"+res.name] = res
	}
	find := func(kind, name, own, ns string) (compiled, bool) {
		for _, scope := range scopeChain(own, ns) {
			if r, ok := byName[kind+"\x00"+scope+"\x00"+name]; ok {
				return r, true
			}
		}
		return compiled{}, false
	}

	var out []Finding
	for _, res := range all {
		for _, kind := range Kinds() {
			labelsOf := map[string][]string{} // target id -> the labels that reach it
			for label, e := range res.block(kind) {
				entry, ok := e.(map[string]any)
				if !ok {
					continue
				}
				name := entryName(label, entry)
				to, found := find(kind, name, res.scope, field(entry, accessScope))
				if !found {
					continue // unresolvable is checkReachable's business, not this one's
				}
				labelsOf[to.id] = append(labelsOf[to.id], label)
			}
			for _, labels := range labelsOf {
				if len(labels) < 2 {
					continue
				}
				sort.Strings(labels)
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("reaches the same %s under %d different names: %s", kind, len(labels), strings.Join(labels, ", ")),
					"Two entries that resolve to one resource are two labels for one fact, and only one of them is doing anything — whichever the compiler visits last silently wins. Keep one.", res.id})
			}
		}
	}
	return out
}

// checkDatabaseOps: an entry may only ask for verbs its target database's type
// has. `database` declares the union of its data models' verbs because an access
// block loads before its target resolves, so `ops: [query]` on a kv store passes
// load and is refused here, where the target's `type` is known. Lowered entries
// are checked the same way; an unresolved entry is checkReachable's.
func checkDatabaseOps(all []compiled) []Finding {
	byName := map[string]compiled{}
	for _, res := range all {
		byName[res.kind+"\x00"+res.scope+"\x00"+res.name] = res
	}

	var out []Finding
	for _, res := range all {
		for label, e := range res.block("database") {
			entry, ok := e.(map[string]any)
			if !ok {
				continue
			}
			name := entryName(label, entry)
			var target compiled
			found := false
			for _, scope := range scopeChain(res.scope, field(entry, accessScope)) {
				if r, ok := byName["database\x00"+scope+"\x00"+name]; ok {
					target, found = r, true
					break
				}
			}
			if !found {
				continue
			}
			// A target whose type did not compile is a broken database, reported
			// against the database rather than against everyone who named it.
			t, known := databaseTypeOf(target.str("type"))
			if !known {
				continue
			}
			for _, op := range ops(entry) {
				if slices.Contains(t.ops.Names(), op) {
					continue
				}
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("asks to %s database/%s, which is a %s database and has %s",
						op, name, t.name, strings.Join(t.ops.Names(), ", ")),
					fmt.Sprintf("A database's verbs are its TYPE's: %s. Either the op is wrong, or this is the wrong database to be naming.", strings.Join(t.ops.Names(), ", ")),
					res.id})
			}
		}
	}
	// A map has no order, so two runs would otherwise report the same faults in
	// different sequences.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Title < out[j].Title
	})
	return out
}

// checkHandlerPorts: only a workload that listens has ports; the schema cannot
// condition the handler's port field on the kind.
func checkHandlerPorts(all []compiled) []Finding {
	var out []Finding
	for _, res := range all {
		for _, prefix := range InboundBlocks() {
			port := res.at(prefix, "port")
			kind := res.at(prefix, "kind")
			if port == "" || kind == "" || kind == "application" {
				continue
			}
			out = append(out, Finding{Broken, res.kind, res.name, res.scope,
				fmt.Sprintf("names port %q on a %s", port, kind),
				fmt.Sprintf("A %s does not listen on ports — only a container workload does — so there is nothing for this name to refer to.", kind), res.id})
		}
	}
	return out
}

// checkDelegatedFills: a resource whose serving block lives in its own repository,
// compiled without that block. A finding rather than a refusal, since a first
// deploy has no artifact yet; never silence, since a fill broken for weeks
// produces the same object. Unlike Resolver's stub, an unwired caller gets the
// finding.
func checkDelegatedFills(obj map[string]any, all []compiled) []Finding {
	filled := interp.MountedOf(obj)

	var out []Finding
	for _, res := range all {
		if res.at("source", "config") == "" {
			continue
		}
		if _, read := filled[res.id]; read {
			continue
		}
		out = append(out, Finding{Check, res.kind, res.name, res.scope,
			"its serving block was not read from " + res.at("source", "git"),
			"This resource says `source.config: " + res.at("source", "config") + "`, so what it serves is decided in that repository -- and this compile did not read it. What was compiled is whatever this repository says instead. That is correct for a first deploy, and it is what a broken fetch also looks like, so it is reported rather than assumed.", res.id})
	}
	return out
}

// checkBusIsGuarded: a route attaching a browser to a bus must name an authorizer.
// An attachment is a standing subscription to an n:n topic, so an unguarded one
// leaves the topic readable by anyone who knows the name. A transformer can
// refuse but is not the policy. The guard sits on the route (http-authorizer
// world), not the bus, whose authorizer is asked about a workload caller a
// browser cannot fill. A gateway-wide authorizer satisfies it too.
func checkBusIsGuarded(all []compiled, byKind map[string][]compiled) []Finding {
	var out []Finding
	for _, res := range all {
		if res.kind != "gateway" || res.at("route", "handler", "kind") != "bus" {
			continue
		}
		if res.at("route", "authorizer", "name") != "" || res.at("authorizer", "name") != "" {
			continue
		}

		out = append(out, Finding{Broken, res.kind, res.name, res.scope,
			"this route attaches to a bus and nothing decides who may",
			"Whoever completes the websocket handshake here receives everything published to the topic from then on. Name an `authorizer:` on this route, or on the gateway to cover every route it serves — a function in the http-authorizer world, asked about the request and answering with the ops the caller gets. The bus's own `authorizer:` cannot do it: that one is asked about a workload caller, and a browser is not one.", res.id})
	}
	return out
}

func checkTransformerWorlds(all []compiled) []Finding {
	worlds := map[string]map[string]bool{}
	for _, res := range all {
		world := ""
		switch res.kind {
		case "gateway":
			world = "gateway"
		case "proxy":
			world = "proxy"
		default:
			continue
		}
		for _, el := range res.elements("transformers") {
			name := field(el, "name")
			if worlds[name] == nil {
				worlds[name] = map[string]bool{}
			}
			worlds[name][world] = true
		}
	}
	var out []Finding
	for name, w := range worlds {
		if len(w) < 2 {
			continue
		}
		// worlds is keyed by name alone, so same-named functions in different namespaces
		// conflate; an id would be just as ambiguous, so "" rather than a guess.
		out = append(out, Finding{Broken, "function", name, "",
			"is a transformer on both a gateway and a proxy",
			"Those are different host interfaces — one is handed a parsed HTTP request, the other bytes on a stream — so one of the two uses is being handed a world the function cannot read.", ""})
	}
	return out
}

// checkExternal: the two questions tcc cannot answer on its own, handed to the caller.
func checkExternal(all []compiled, r Resolver) []Finding {
	var out []Finding
	for _, res := range all {
		if fqdn := res.str("fqdn"); fqdn != "" && res.kind == "zone" {
			if err := r.ZoneDelegated(fqdn); err != nil {
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("zone %q does not answer here", fqdn),
					err.Error(), res.id})
			}
		}
		if image := res.at("source", "image"); image != "" {
			// The pull secret is a reference, `{secret: <id>}`; read as a string it would
			// ask whether the image pulls anonymously.
			if err := r.ImagePullable(image, res.at("source", "image_pull_secret", "name")); err != nil {
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("image %q cannot be pulled", image),
					err.Error(), res.id})
			}
		}
	}
	return out
}

// CheckLoweredOps: every op a lowering asks for is a verb its target kind
// declares. interp.CheckLowerings cannot ask, having no word for a verb; without
// this a mistyped op fails the second compile against a document nobody wrote.
func CheckLoweredOps(root *Node) []string {
	ops := AccessEntryShape().Ops
	var problems []string
	for _, w := range interp.DeclaredWrites(root) {
		// An access entry is <kind>/<name>/<ops>; anything else a lowering writes is an
		// ordinary field and has no verb to check.
		if len(w.Path) != 3 {
			continue
		}
		if last, fixed := w.Path[2].Literal(); !fixed || last != ops {
			continue
		}
		// A write landing on what its own lowering produced arrives with the produced
		// kind already in the path.
		kind, fixed := w.Path[0].Literal()
		if !fixed {
			continue
		}
		op, fixed := w.Value.Literal()
		if !fixed {
			continue // a value the plan fills in is not a verb anybody wrote
		}
		if _, ok := EffectOf(kind, op); !ok {
			problems = append(problems, fmt.Sprintf(
				"%s lowers into %s and asks for %q on a %s, which does not do that; it has %s",
				w.From, w.Into, op, kind, strings.Join(opNames(kind), ", ")))
		}
	}
	sort.Strings(problems)
	return slices.Compact(problems)
}

// checkAuthoredBuiltins: `source: {builtin: …}` names a program the platform
// ships and only a lowering may set it, so the branch is legal exactly where the
// lowering record shows the compiler put it.
func checkAuthoredBuiltins(low interp.Lowered, all []compiled) []Finding {
	var out []Finding
	for _, res := range all {
		name := res.at("source", "builtin")
		if name == "" || low.Produced(res.id) {
			continue
		}
		out = append(out, Finding{Broken, res.kind, res.name, res.scope,
			fmt.Sprintf("names the built-in program %q, which is not an author's to name", name),
			"`source: {builtin: …}` is how a lowering says a workload's code is the platform's own — the agent loop, a workflow's step driver. The set is closed by what the runtime ships, so a resource somebody wrote can only name something that is not there. Point the package's `source` at this repository, a git URL or an image.",
			res.id})
	}
	return out
}
