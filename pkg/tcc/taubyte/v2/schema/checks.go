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
// workload, does the port something targets exist, does the connector this database
// names actually provide a database — and an attribute Validator sees one value at a
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

// Audit runs every cross-resource check over a compiled object.
func Audit(obj map[string]any, r Resolver) []Finding {
	if r == nil {
		r = StubResolver()
	}
	root := obj
	if inner, ok := obj["object"].(map[string]any); ok {
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
	out = append(out, checkTransformerWorlds(all)...)
	out = append(out, checkMountedSites(all)...)
	out = append(out, checkHostWorlds(all)...)
	out = append(out, checkHandlerPorts(all)...)
	out = append(out, checkVolumeWriters(all)...)
	out = append(out, checkWorkflows(all)...)
	// The record of what the compile produced sits BESIDE the project, so it is read
	// from the object rather than from the project inside it.
	out = append(out, checkLoweringCollisions(root, interp.LoweredOf(obj), all)...)
	out = append(out, checkLoweredLinks(root, all)...)
	out = append(out, checkExternal(all, r)...)

	sort.SliceStable(out, func(i, j int) bool {
		rank := map[Severity]int{Broken: 0, Check: 1, Note: 2}
		return rank[out[i].Severity] < rank[out[j].Severity]
	})
	return out
}

// collect walks the compiled object into a flat list, carrying each resource's scope.
// Namespaces nest, so this recurses on the scope container rather than assuming depth.
func collect(scope map[string]any, path string) []compiled {
	var out []compiled
	for group, val := range scope {
		instances, ok := val.(map[string]any)
		if !ok {
			continue
		}
		for id, v := range instances {
			res, ok := v.(map[string]any)
			if !ok {
				continue
			}
			name, _ := res["name"].(string)
			if group == scopeContainer {
				child := path
				if name != "" {
					child = strings.TrimPrefix(path+"/"+name, "/")
				}
				out = append(out, collect(res, child)...)
				continue
			}
			out = append(out, compiled{kind: group, name: name, scope: path, id: id, fields: res})
		}
	}
	return out
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
func (r compiled) list(key string) []string {
	switch v := fieldValue(r.fields, key).(type) {
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
				"It is started by work arriving rather than by a request. Listed so the absence is a decision you can see rather than one you have to remember."})
			continue
		}
		out = append(out, Finding{Broken, res.kind, res.name, res.scope,
			"nothing reaches it",
			"No gateway route and no proxy names it, and it consumes nothing. It runs and answers no one."})
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
				"No ingress names it, no probe polls it and nothing is delivered to it. Either something should target this port by name, or the workload is not listening on what it says it is."})
		}
	}
	return out
}

// checkDelivery: a container has no host ABI, so an item has to be posted to a port
// it declared. A WASM workload is dispatched and needs none.
func checkDelivery(all []compiled) []Finding {
	var out []Finding
	for _, res := range all {
		if res.str("image") == "" {
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
					"A container has no host ABI, so each item has to be posted to a port it declared. Without deliver.port nothing arrives and the workload idles."})
			}
		}
	}
	return out
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
						"A file has to be mounted somewhere the workload can open it."})
				}
			}
			_, literal := entry["value"]
			_, secret := entry["secret"]
			switch {
			case literal && secret:
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("variable %q has both a value and a secret", name),
					"One or the other: a literal is in git, a secret reference is not. With both, which one the workload gets is whatever the runtime happens to prefer."})
			case !literal && !secret:
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("variable %q has neither a value nor a secret", name),
					"The name is declared and nothing is behind it, so the workload reads an empty string."})
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
			fmt.Sprintf("The site receives %s/... and was almost certainly built for /, so every page is a file it does not have. Set route.strip-prefix, or build the site with %s as its base.", path, path)})
	}
	return out
}

// checkHostWorlds: every USE of a function is a place tcc can see, and the function
// says which world it was built against. Comparing the two is what turns "compiles
// clean, fails on live traffic" into an error at compile.
func checkHostWorlds(all []compiled) []Finding {
	world := map[string]string{} // "scope\x00name" -> declared world
	for _, res := range all {
		if res.kind != "function" {
			continue
		}
		w := res.str("implements")
		if w == "" {
			w = "handler"
		}
		world[res.scope+"\x00"+res.name] = w
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
	}

	var out []Finding
	for _, u := range uses {
		got, known := lookupWorld(world, u.name, u.own, u.ns)
		if !known {
			// The entry's KEY is the function's name, and a key is not a value — no
			// Ref() resolves it, so a typo here compiles clean and the transformer or
			// authorizer simply never runs. Silent, and in the authorizer's case
			// silently unguarded.
			out = append(out, Finding{Broken, "function", u.name, u.ns,
				fmt.Sprintf("is named as a %s by %s, and does not exist", u.wants, u.where),
				"The name is the key of the entry, and a key is not a reference the compiler resolves — so this one names nothing and the step never runs."})
			continue
		}
		if got == u.wants {
			continue
		}
		out = append(out, Finding{Broken, "function", u.name, u.ns,
			fmt.Sprintf("is used as a %s by %s, and implements %s", u.wants, u.where, got),
			"Those are different host interfaces. The function will be handed a world it was not built to read, which compiles clean and fails on live traffic."})
	}
	return out
}

// lookupWorld resolves a named function the way every name is resolved.
func lookupWorld(world map[string]string, name, own, ns string) (string, bool) {
	for _, scope := range scopeChain(own, ns) {
		if w, ok := world[scope+"\x00"+name]; ok {
			return w, true
		}
	}
	return "", false
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
				"A workflow does not run itself. Something has to declare `start` on it — a cron, a function, or a route — the same way anything else in this vocabulary is reached."})
		}

		steps := res.elements("steps")
		if len(steps) == 0 {
			out = append(out, Finding{Broken, res.kind, res.name, res.scope,
				"has no steps, so it lowers to nothing and starting it does nothing",
				"A workflow IS its steps: with none, the queues and the entity it would become are not created and a start has nowhere to go."})
			continue
		}

		named := map[string]bool{}
		for _, step := range steps {
			named[field(step, "name")] = true
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
					"It would run when the step before it succeeded and when it failed, which is not what a compensation step is for. A line has one order and no branch: give the handler its own workflow, or let failure land in the dead-letter queue."})
			}
		}

		for _, step := range steps {
			name := field(step, "name")
			if target := field(step, "on_error"); target != "" && !named[target] {
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("step %q sends failure to %q, which is not a step of this workflow", name, target),
					"On error the run would jump to a step that does not exist, which is the same as having no handler at all — except it reads like it has one."})
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
		for name, e := range res.block(target.kind) {
			if name != target.name {
				continue
			}
			entry, ok := e.(map[string]any)
			if !ok {
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
			if _, taken := authored[d.Kind+"\x00"+res.scope+"\x00"+d.Name]; taken {
				out = append(out, Finding{Broken, d.Kind, d.Name, res.scope,
					fmt.Sprintf("is authored here AND is what %s becomes", d.Of),
					"A transpiled resource takes the name too, and both would be the same key. Rename one — the authored one is the one you can see."})
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
					". The name is carried through the lowering unchanged, so fixing it where it is written fixes this."})
		}
	}
	return out
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

// checkVolumeWriters: a volume is a SINGLE-WRITER leased directory, and nothing in the
// declaration says so. Access is declared by the ACCESSOR and the target only bounds it
// — which is right for every other kind, and for a volume means two workloads can each
// write `ops: [mount-rw]` and both compile. The second one does not get a degraded
// mount; it gets a directory another process already holds the lease on.
//
// The same statement fails a second way inside ONE resource: a workload with
// `max-instances: 10` and a rw mount is asking for ten writers to a one-writer
// filesystem. Nine of them cannot start, and which nine is a race.
//
// Both need a second resource to answer — the volume's own kind for one, the workload's
// ceiling for the other — which is why neither is an attribute validator.
func checkVolumeWriters(all []compiled) []Finding {
	var out []Finding
	writers := map[string][]compiled{}

	for _, res := range all {
		for target, e := range res.block("volume") {
			entry, ok := e.(map[string]any)
			if !ok || !mountsForWriting(entry) {
				continue
			}
			writers[volumeKey(all, target, field(entry, "namespace"), res.scope)] = append(
				writers[volumeKey(all, target, field(entry, "namespace"), res.scope)], res)

			if n := res.int("limits/max_instances"); n > 1 {
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("mounts volume %q for writing and may run %d instances", target, n),
					"A volume is leased to one writer. Either the ceiling comes down to 1, or the volume is mounted read-only and something else owns the writes."})
			}
		}
	}

	for key, ws := range writers {
		if len(ws) < 2 {
			continue
		}
		sort.Slice(ws, func(i, j int) bool { return ws[i].kind+ws[i].name < ws[j].kind+ws[j].name })
		names := make([]string, 0, len(ws))
		for _, w := range ws {
			names = append(names, w.kind+"/"+w.name)
		}
		name := key
		if cut := strings.LastIndex(key, "\x00"); cut >= 0 {
			name = key[cut+1:]
		}
		out = append(out, Finding{Broken, "volume", name, "",
			"is mounted for writing by " + strings.Join(names, " and "),
			"A volume has one writer. The others are asking for a lease that is already held, and which of them starts is a race."})
	}
	return out
}

// mountsForWriting reports whether an access entry asks for a mount that writes,
// read off the volume kind's own verbs rather than by matching the name "mount-rw".
func mountsForWriting(entry map[string]any) bool {
	for _, op := range ops(entry) {
		for _, v := range OpsOf("volume") {
			if v.Name == op && v.Mounted && v.Effect == Write {
				return true
			}
		}
	}
	return false
}

// volumeKey resolves an access target to the volume it actually names, so two writers
// that spell the same volume differently still collide. Falls back to the written
// namespace when no volume of that name exists — an unresolvable target is
// checkReachable's business, not this one's.
func volumeKey(all []compiled, name, ns, own string) string {
	scope := strings.TrimPrefix(ns, "/")
	if ns == "" {
		scope = own
	} else if !strings.HasPrefix(ns, "/") && own != "" {
		scope = own + "/" + ns
	}
	for {
		for _, res := range all {
			if res.kind == "volume" && res.name == name && res.scope == scope {
				return scope + "\x00" + name
			}
		}
		if scope == "" || ns != "" {
			break
		}
		cut := strings.LastIndex(scope, "/")
		if cut < 0 {
			scope = ""
			continue
		}
		scope = scope[:cut]
	}
	return scope + "\x00" + name
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
				fmt.Sprintf("A %s does not listen on ports — only a container workload does — so there is nothing for this name to refer to.", kind)})
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
		out = append(out, Finding{Broken, "function", name, "",
			"is a transformer on both a gateway and a proxy",
			"Those are different host interfaces — one is handed a parsed HTTP request, the other bytes on a stream — so one of the two uses is being handed a world the function cannot read."})
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
					err.Error()})
			}
		}
		if image := res.str("image"); image != "" {
			// The pull secret is a REFERENCE, so it is `{secret: <id>}` — reading it as
			// a string hands the resolver an empty credential and asks whether the image
			// pulls anonymously, which is a different question.
			if err := r.ImagePullable(image, res.at("image_pull_secret", "name")); err != nil {
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("image %q cannot be pulled", image),
					err.Error()})
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
		if len(w.Path) != 3 || w.Path[2] != ops {
			continue
		}
		kind := w.Path[0]
		if kind == "{into}" {
			kind = w.Into // the resource this lowering produced
		}
		if strings.ContainsAny(kind, "{}") {
			continue // a kind the plan fills in; nothing static to check
		}
		if _, ok := EffectOf(kind, w.Value); !ok {
			problems = append(problems, fmt.Sprintf(
				"%s lowers into %s and asks for %q on a %s, which does not do that; it has %s",
				w.From, w.Into, w.Value, kind, strings.Join(opNames(kind), ", ")))
		}
	}
	sort.Strings(problems)
	return slices.Compact(problems)
}
