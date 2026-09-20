package schema

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/interp"
)

// The cross-resource checks, run by tcc over the compiled object.
//
// Every one of them needs a SECOND resource to answer — is anything in front of this
// workload, does the port something targets exist, does the access entry spell a verb
// the database it names actually has — and an attribute Validator sees one value at a
// time. That is why they were repeatedly deferred to a hypothetical publish-time pass
// that nobody owned. They belong here: tcc holds the whole object, so tcc can answer.
//
// A check that needs knowledge tcc does NOT have — is this DNS name delegated, is this
// image pullable — emits a NextValidation instead and the caller answers it. That seam
// already exists for `zone`/`dns`; Resolver below is the caller's side of it, with a
// stub that says yes so a compile is never blocked on something nobody has wired yet.
//
// Severity is deliberate and not a count. `Broken` is a config that cannot work — an
// http application no ingress names answers nobody. `Check` is a config that probably
// is not what was meant. `Note` is a fact worth seeing: a queue consumer no ingress
// names is CORRECT, and saying so is how the absence stops being something a reviewer
// has to remember.

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

// Resolver answers what tcc cannot see. Every method takes the value tcc found and
// returns nil when it is fine.
//
// The stub says yes to everything, on purpose: an unwired external check must not
// block a compile, and a caller that has not implemented one should get the same
// answer as a caller that has none. Tests swap in a resolver that says no, which is
// how the edge cases are exercised without a network.
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

type auditOf struct{ environment string }

// AuditIn names the environment the object was compiled FOR.
//
// A COMPILE IS FOR ONE DEPLOYMENT and the object does not say which — the values
// in it are already resolved, so `name_preview` and `name` are indistinguishable
// once compiled. Without the name there is no way to ask whether a placement is
// inside the regions THAT environment keeps its state in, because there is no way
// to know which environment's list to read.
//
// Omitted, every check that does not need it still runs.
func AuditIn(environment string) AuditOption {
	return func(a *auditOf) { a.environment = environment }
}

// Audit runs every cross-resource check over a compiled object.
func Audit(obj map[string]any, r Resolver, opts ...AuditOption) []Finding {
	if r == nil {
		r = StubResolver()
	}
	var in auditOf
	for _, opt := range opts {
		opt(&in)
	}
	root := obj
	if inner, ok := obj[interp.ProjectKey].(map[string]any); ok {
		root = inner
	}

	all := collect(root, "")
	byKind := map[string][]compiled{}
	for _, res := range all {
		byKind[res.kind] = append(byKind[res.kind], res)
	}

	var out []Finding
	out = append(out, checkReachable(all, byKind)...)
	out = append(out, checkPortsAreFed(all)...)
	out = append(out, checkDelivery(all)...)
	out = append(out, checkVariables(all)...)
	out = append(out, checkBuild(all)...)
	out = append(out, checkTransformerWorlds(all)...)
	out = append(out, checkMountedSites(all)...)
	out = append(out, checkHostWorlds(all)...)
	out = append(out, checkHandlerPorts(all)...)
	out = append(out, checkAccessDuplicates(all)...)
	out = append(out, checkDatabaseOps(all)...)
	out = append(out, checkWorkflows(all)...)
	out = append(out, checkRegions(root, all, in.environment)...)
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

// ScopeKindNames is every kind whose instances are SCOPES: a namespace, and
// every kind that lowers into a container of its own.
//
// EXPORTED BECAUSE THE READ PATH NEEDS IT. A consumer resolving `(scope, kind,
// name)` walks index segments of the form `<scope-kind>/<id>`, and until this
// was askable outside the package every such consumer assumed the literal
// `namespace` — which made every resource a lowering produced unreachable.
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
			// ONE KEY, TWO THINGS. Inside a kind that is also a container, `function`
			// holds both the loop it became and the function it reaches — the produced
			// resource keyed by its id, the access entry by the target's label. Asking tcc
			// which is which is the point of the walk living here: a consumer guessing
			// would report an agent's tool as something it contains.
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

// list reads a StringSlice off the compiled object, which arrives as []string or, when
// it came through JSON, as []any.
func (r compiled) list(key string) []string { return listAt(r.fields, key) }

// listAt is that read on any object, because a SCOPE is not a compiled resource —
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

// int reads a numeric field, which arrives as int or, through JSON, as float64.
func (r compiled) int(key string) int {
	switch v := fieldValue(r.fields, key).(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
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
func checkDelivery(all []compiled) []Finding {
	var out []Finding
	for _, res := range all {
		if !runsAsContainer(res) {
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

// runsAsContainer answers "does this have a host ABI, or does something have to be
// posted to a port". It used to be answered by looking for an `image:` field, which was
// true only while a container was always somebody else's: a workload BUILT from a
// Dockerfile has no image reference and would have read as a dispatched WASM component,
// so a queue consumer missing its deliver port would have gone unreported.
//
// The tier is the KIND's, read back off Engines(), or `runtime` where the kind declares
// more than one and therefore has a choice to make. That is the §8c rule applied to a
// check: a consumer — and an audit is one — does not keep its own table of which kinds
// are containers.
//
// A kind that declared two engines and no way to choose between them would answer no
// here, which is the safe direction (nothing is reported) and is unreachable while
// `worker` is the only such kind — TestTheEngineSeamIsAValue holds that.
func runsAsContainer(res compiled) bool {
	if tier := res.str("runtime"); tier != "" {
		return tier == containerEngine
	}
	engines := EnginesOf(res.kind)
	return len(engines) == 1 && engines[0] == containerEngine
}

// checkVariables: exactly one of value or secret. The DSL cannot state a mutual
// exclusion, so it is stated here.
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
			_, literal := entry["value"]
			_, secret := entry["secret"]
			switch {
			case literal && secret:
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("variable %q has both a value and a secret", name),
					"One or the other: a literal is in git, a secret reference is not. With both, which one the workload gets is whatever the runtime happens to prefer.", res.id})
			case !literal && !secret:
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("variable %q has neither a value nor a secret", name),
					"The name is declared and nothing is behind it, so the workload reads an empty string.", res.id})
			}
		}
	}
	return out
}

// checkBuild: three statements about a build that need TWO BLOCKS to make, which is one
// more than a switch can reach. `source:` and `build:` are different attributes at
// different paths, so §3's Either has no single key to hang an exclusion between them
// on, and they land here beside the other cross-block exclusions this file already
// owes — checkVariables' value-or-secret is the same shape.
//
//	an image AND a build      the image is finished; nothing the build says would run
//	neither, on a container   there is no artifact and no recipe for one
//	a build the tier cannot use   a Dockerfile makes an image, not a component
//
// The last one reads `runtime` because that is this vocabulary's word for "which of my
// engines", and only a kind declaring more than one has it to answer. Which kinds those
// are is EnginesOf's, not a list here.
func checkBuild(all []compiled) []Finding {
	var out []Finding
	for _, res := range all {
		if len(EnginesOf(res.kind)) == 0 {
			continue // nothing builds it, and nothing here applies
		}
		way := res.at("build", "kind")
		fromImage := res.at("source", "kind") == "image"

		switch {
		case fromImage && way != "":
			out = append(out, Finding{Broken, res.kind, res.name, res.scope,
				"takes its content from an image and also declares a build",
				"The image is already built, so nothing the build says would run. Take one out: name the image, or name the build that produces one.", res.id})
		case !fromImage && way == "" && runsAsContainer(res):
			// A repository alone is enough for a wasm component or a static site —
			// that is what the absent block means everywhere else. It is not enough
			// for an image: nothing here turns a directory into one without being
			// told how.
			out = append(out, Finding{Broken, res.kind, res.name, res.scope,
				"names no image and declares no build",
				"A container runs an image. Either point source at one somebody built, or declare the build that makes it.", res.id})
		}

		// The way of building and the tier that runs the product, on a kind that
		// chooses between tiers. A Dockerfile produces an image; commands and a script
		// produce what the builder found in its output directory.
		if tier := res.str("runtime"); tier != "" && way != "" {
			if produces := BuildWaysOf(tier); !slices.Contains(produces, way) {
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("runs on the %s tier and is built with %q", tier, way),
					fmt.Sprintf("That way of building does not produce what a %s tier runs. Use one of: %s.", tier, strings.Join(produces, ", ")), res.id})
			}
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
	world := map[string]struct{ w, id string }{}
	for _, res := range all {
		if res.kind != "function" {
			continue
		}
		w := res.str("implements")
		if w == "" {
			w = "handler"
		}
		world[res.scope+"\x00"+res.name] = struct{ w, id string }{w, res.id}
	}

	// A use names a function and expects one world of it.
	// own is where the use was written; ns is what it said about where to look.
	type use struct{ name, own, ns, wants, where string }
	var uses []use
	for _, res := range all {
		if n := res.at("authorizer", "name"); n != "" {
			uses = append(uses, use{n, res.scope, res.at("authorizer", "namespace"), "authorizer", res.kind + "/" + res.name + " authorizer"})
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
		// A ROUTE MAY HAND ITS OWN DECISION TO A ROUTER instead of naming a handler,
		// and that is a SECOND site — `route/router/name`, beside the gateway-wide
		// `router/name` above. Both are declared by one helper that takes the world as
		// an argument (routerAttributes, definition.go:334) and writes it into the
		// field's own documentation, so the vocabulary states `gateway-router` at both
		// and only one of them was read.
		if n := res.at("route", "router", "name"); n != "" && res.kind == "gateway" {
			uses = append(uses, use{n, res.scope, res.at("route", "router", "namespace"), "gateway-router", res.kind + "/" + res.name + " route router"})
		}
		// AN ADMISSION VALIDATOR IS THE ONE WORLD WITH ITS OWN WORD AND NO AUDIT.
		// `instance-validator` is in hostWorlds precisely so a validator need not be a
		// kind (instanceAttributes, definition.go:400-408), and instances/validator/name
		// is its only use site — spliced into four kinds, and read by nothing until now.
		// The world is not the resource's to vary: whatever holds instances, what admits
		// one is handed the same thing.
		if n := res.at("instances", "validator", "name"); n != "" {
			uses = append(uses, use{n, res.scope, res.at("instances", "validator", "namespace"), "instance-validator", res.kind + "/" + res.name + " instances validator"})
		}
	}

	var out []Finding
	for _, u := range uses {
		found, known := lookupWorld(world, u.name, u.own, u.ns)
		if !known {
			// A NAME THAT RESOLVES TO NOTHING IS DROPPED, NOT REFUSED. Every site here
			// carries Ref("function", ByName()), and an unresolvable one leaves the
			// field empty and compiles clean — verified, on each branch. So a typo is
			// silent in exactly the way it must not be: the transformer never runs, the
			// route's router never routes, and the authorizer never guards, each of
			// which reads as working. Nothing compiled answers to this name, so there is
			// no id to point at either.
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

// lookupWorld resolves a named function the way every name is resolved.
func lookupWorld(world map[string]struct{ w, id string }, name, own, ns string) (struct{ w, id string }, bool) {
	for _, scope := range scopeChain(own, ns) {
		if w, ok := world[scope+"\x00"+name]; ok {
			return w, true
		}
	}
	return struct{ w, id string }{}, false
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

		// WHICH KEY IS PRESENT IS THE TYPE, so exactly one of them has to be. This is the
		// one mutual exclusion in this vocabulary that is a check rather than an Either()
		// — see the comment on stepsGroup: a captured branch key is the only projection
		// that happens inside a list element, and decompile skips elements precisely
		// because it assumes none does, so the switch would compile and then fail to
		// write itself back.
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

// checkLoweringCollisions: a lowered resource takes a name, and an authored resource
// can already have it. The two would be the same key in the compiled object, and which
// one wins is not something an author should have to know.
// scopeAllowance is one scope's own `regions` line, and where it sits.
type scopeAllowance struct {
	parent   string // the scope path above this one; "" is the project root
	kind     string // what KIND of scope it is, for the finding
	name, id string
	regions  []string // as declared here; empty means "whatever the parent allows"
}

// scopeAllowances walks every scope in the compiled object and records the allowance it
// declares, keyed by the same scope path collect() files resources under.
//
// It has to be its own walk: a namespace is a container and NOT a resource kind, so it
// never appears in `all` — collect only emits an entry for a container that is also a
// kind, which is what a lowered workflow or agent is. And it walks GENERICALLY, over
// every group ContainerKeys answers with, because the chain has more than one kind of
// level: a walk that knew only the word `namespace` would step over `workflow/checkout`
// and hand what it produced the wrong ceiling.
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
// above it. Nothing anywhere means no ceiling — every region the cloud has.
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

// checkRegions: the ceiling, and the only thing that enforces it.
//
// A region name is an opaque label the implementation defines, so the schema cannot
// refuse one. What it CAN be held to is the project's own statement about itself: a
// scope narrows the regions it inherits and never widens them, and a resource is placed
// inside the allowance of the chain it sits in. Both are cross-document — the parent is
// another file, and so is the scope a resource is placed under — so neither the schema
// nor load time can see both ends, which is what puts them here.
//
// Both are Broken rather than Check. A widening that compiled silently means a child
// runs where its parent said nothing may, which is the entire point of declaring an
// allowance; and a placement outside the chain is a resource that has nowhere to run.
//
// Where NO scope in a chain declares an allowance there is nothing to check against,
// and that is the zero-config project: it writes nothing and is bounded by nothing. It
// is also why this doubles as the typo-catcher — once a ceiling exists, `eu-wst` is
// simply not within it — and why a project with no ceiling has no typo-catcher at all
// until the deferred validation regionAttributes describes can be carried.
func checkRegions(root map[string]any, all []compiled, environment string) []Finding {
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

		// FAILOVER TAKES TWO REGIONS. That there are exactly two is the declaration's
		// own property — two named fields, both required on the branch — but that they
		// are DIFFERENT is not something the schema can say, and a standby equal to its
		// main is a failover plan that fails to the thing that is already gone.
		// A POLICY DOES NOT CHANGE BETWEEN ENVIRONMENTS, and the branch suffix
		// makes that sayable by accident: `failover:` with a `pinned_preview:`
		// beside it resolves to one policy and the other branch's regions. It is
		// a different resource in each deployment, which is what writing the
		// whole resource for that environment is for.
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

	out = append(out, checkEverythingIsPlaced(root, all)...)
	return append(out, checkPlacedInsideTheEnvironment(root, all, environment)...)
}

// checkEnvironmentRegions: an environment narrows the project's allowance and
// never widens it, exactly as a namespace does.
//
// AN ENVIRONMENT IS ON MANY REGIONS — that is why the field is a list — and the
// regions it keeps its state in are still bounded by what the project allows.
// Nothing checked this, so `regions: [ap]` under a project allowing only `eu`
// compiled clean AND was honoured at runtime: the environment quietly widened
// the one statement the project makes about where its data may ever be.
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

// checkPlacedInsideTheEnvironment: a resource is placed within the regions the
// environment it is deployed to keeps its state in.
//
// IT NEEDS THE ENVIRONMENT'S NAME and says nothing without one — see AuditIn.
// The placement being read is already resolved for whichever environment this
// compile was for, so checking it against a different environment's list would
// report the wrong deployment.
func checkPlacedInsideTheEnvironment(root map[string]any, all []compiled, environment string) []Finding {
	if environment == "" {
		return nil
	}

	var allowed []string
	for _, env := range EnvironmentsIn(root) {
		if env.Name == environment {
			allowed = env.Regions
			break
		}
		// A SHADOW KEEPS NO STATE OF ITS OWN, so it is bounded by the
		// environment it was written inside — which is also why the vocabulary
		// gives it no regions field to disagree with.
		for _, shadow := range env.Shadows {
			if shadow.Name == environment {
				allowed = env.Regions
			}
		}
	}
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

// checkEverythingIsPlaced: a resource with a physical footprint says where it is,
// or the project says it for everything that does not.
//
// WHY HERE AND NOT IN THE SCHEMA. "Required, unless the project declared a
// default" is a rule whose two halves are in different documents, and an
// attribute cannot see the project it sits under — the same reason the ceiling
// is checked here. Required() on the block would be the other rule: every
// resource, every time, with no default possible.
//
// PLACED IS READ OFF THE SCHEMA, never a list kept beside it. A kind that gains
// a footprint tomorrow is covered by this the moment it declares Placed().
func checkEverythingIsPlaced(root map[string]any, all []compiled) []Finding {
	scopes := scopeAllowances(root)
	placed := make(map[string]bool, len(PlacedKinds()))
	for _, kind := range PlacedKinds() {
		placed[kind] = true
	}

	var out []Finding
	for _, res := range all {
		if !placed[res.kind] || res.str("regions/policy") != "" {
			continue
		}
		// THE SCOPE'S OWN `regions` IS THE DEFAULT, which is the same word and
		// the same list a resource writes — one concept narrowing down rather
		// than an allowance and a placement that differ by a letter. A chain
		// that declares one places everything under it that says nothing.
		if len(effectiveAllowance(scopes, res.scope)) > 0 {
			continue
		}
		out = append(out, Finding{Broken, res.kind, res.name, res.scope,
			"is not placed anywhere",
			"Anything with a footprint is placed. Give it regions, or give a scope above it regions for everything that does not say.", res.id})
	}
	return out
}

// placedIn is every region a resource's own block names, across the branch it
// took. `pinned` and `cdn` carry a LIST — a resource is in many regions, the
// same way a scope allows many — and `failover` carries its two by name,
// because which one is main is the statement rather than a position.
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

// checkLoweredLinks: a fault INSIDE something nobody wrote, reported against something
// somebody did.
//
// A workflow becomes queues, and one of those queues invokes the function its step
// named. If that function does not exist, the thing that is broken is a queue the
// author cannot find in their repository — so the finding names the WORKFLOW and the
// step, and the trail says which part of the lowering failed. That is the whole reason
// a plan carries its source: a diagnostic about generated config is worthless if it
// only names generated config.
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
		// A resource a lowering WRITES INTO has to exist. It is the half a reader cannot
		// see: the queue is produced with a name they chose, but the function it says
		// receives from it is one they named in a step — and a name that matches nothing
		// leaves the queue with no consumer at all.
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

		// AND SO DOES THE RESOURCE IT POINTS AT. An access entry is `<kind>/<label>/ops`
		// on the accessor, so the target is in the write's PATH rather than in what it
		// writes into — and when the accessor is itself produced (a driver reaching the
		// function a step named), the path is the ONLY place a misspelling shows. It used
		// to be caught by accident, back when the entry landed on the author's own
		// function and the missing name was the accessor.
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

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// checkAccessDuplicates: two entries in one resource's own access group that resolve to
// the SAME target are two labels for one fact — `accessName` exists precisely so two
// entries CAN share a target's real name across namespaces, but nothing stops the author
// writing two different labels for the same one by mistake, and a map has no order an
// author controls: whichever entry's ops the compiler visits last silently wins, and the
// document reads as if both grants apply.
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

// checkDatabaseOps: AN ENTRY MAY ONLY ASK FOR VERBS THE TARGET DATABASE'S TYPE HAS.
//
// `database` declares the UNION of its four data models' verb sets, because an access
// block is loaded long before anything has resolved which database an entry names — so
// AreVerbsOf can hold `ops` to that union and no further. Left there, the per-kind verb
// rule would be gone in all but name: `ops: [query]` on a kv store, or `[search]` on a
// table, would compile and mean nothing at run time.
//
// So it is asked HERE, where the target has been resolved and its `type` is a field on
// it. That is the same seam CheckLoweredOps already uses and for the same reason, stated
// in AGENTS.md §8b: the generic layer carries values at paths and has no word for a
// verb, so what a verb MEANS is this vocabulary's to check.
//
// It covers what a LOWERING wrote as well as what an author did — a produced database
// and the entries pointing at it are in the compiled object like everything else, so an
// agent's memory index being reached with `search` is checked by the same pass that
// checks a hand-written entry.
//
// An entry that resolves to nothing is checkReachable's business, not this one's.
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

// checkHandlerPorts: only a workload that LISTENS has ports. A route naming a port on
// a website or a function is pointing at something that does not exist — the field is
// on the handler block for every kind, because which kinds have ports is not something
// the schema can condition on.
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

// checkTransformerWorlds: a transformer on a gateway is handed a parsed HTTP request
// and one on a proxy is handed bytes on a stream. They are different host interfaces,
// and a function does not say which it was built for — so the most tcc can do is
// report a function used as BOTH, which cannot be right whichever world it was built
// against.
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
		// worlds is keyed by name alone, with no scope — a real pre-existing gap this
		// finding already had (two functions of this name in different namespaces would
		// be conflated). Not fixed here: an id would have the same ambiguity a scope-blind
		// lookup would, so "" is the honest answer rather than a guess.
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
			// The pull secret is a REFERENCE, so it is `{secret: <id>}` — reading it as
			// a string hands the resolver an empty credential and asks whether the image
			// pulls anonymously, which is a different question.
			if err := r.ImagePullable(image, res.at("source", "image_pull_secret", "name")); err != nil {
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("image %q cannot be pulled", image),
					err.Error(), res.id})
			}
		}
	}
	return out
}

// EVERY OP A LOWERING ASKS FOR IS A VERB THE TARGET DECLARES.
//
// A lowering writes access entries — `Reaches("receive", …)` puts `receive` in a queue
// entry's ops — and whether `receive` is something a queue DOES is this vocabulary's
// question, not the interpreter's. interp.CheckLowerings deliberately cannot ask it: the
// generic layer knows a value goes at a path and has no word for a verb.
//
// So it is asked here, over what the schema declares, with no config in the room. Without
// it a mistyped op reaches the second compile and fails as a load error against a
// document nobody wrote.
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
		// The kind is a fixed segment or it is nothing static to check. It used to be
		// the string "{into}" here, which this had to know by heart; a write that lands
		// on what its own lowering produced arrives with the produced KIND already in
		// it, because that one is known without any config.
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

// `source: {builtin: …}` NAMES A PROGRAM THE PLATFORM SHIPS, and its doc says it is
// not for authors: a lowering sets it, and the set is closed by what exists. Nothing
// performed that. An author could write `builtin: does_not_exist` on an ordinary
// function and compile clean, producing a workload whose code is a name the platform
// has never heard of — a value promising something nothing delivers, which is the
// defect the deleted `auth: oauth` field is remembered for.
//
// The check is the one question that separates the two cases and it is already
// answered: a resource nobody wrote is in the lowering record. So the branch is legal
// exactly where the compiler itself put it.
func checkAuthoredBuiltins(low interp.Lowered, all []compiled) []Finding {
	var out []Finding
	for _, res := range all {
		name := res.at("source", "builtin")
		if name == "" || low.Produced(res.id) {
			continue
		}
		out = append(out, Finding{Broken, res.kind, res.name, res.scope,
			fmt.Sprintf("names the built-in program %q, which is not an author's to name", name),
			"`source: {builtin: …}` is how a lowering says a workload's code is the platform's own — the agent loop, a workflow's step driver. The set is closed by what the runtime ships, so a resource somebody wrote can only name something that is not there. Point `source` at this repository, a component, or a git URL.",
			res.id})
	}
	return out
}
