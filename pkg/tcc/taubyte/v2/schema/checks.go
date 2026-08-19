package schema

import (
	"fmt"
	"sort"
	"strings"
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
	fields            map[string]any
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
		for _, v := range instances {
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
			out = append(out, compiled{kind: group, name: name, scope: path, fields: res})
		}
	}
	return out
}

const scopeContainer = "namespace"

func (r compiled) str(key string) string {
	s, _ := r.fields[key].(string)
	return s
}

// list reads a StringSlice off the compiled object, which arrives as []string or, when
// it came through JSON, as []any.
func (r compiled) list(key string) []string {
	switch v := r.fields[key].(type) {
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
	items, ok := r.fields[key].([]any)
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

// addr reads a string field off a list element.
func addr(el map[string]any, key string) string {
	s, _ := el[key].(string)
	return s
}

// splitAddress reads "redact", "eu/stamp" or "/shop/tier_header" into the scope to look
// in and the name to look for — the same three forms a namespace path takes anywhere.
func splitAddress(addr, own string) (scope, name string) {
	cut := strings.LastIndex(addr, "/")
	if cut < 0 {
		return own, addr
	}
	name = addr[cut+1:]
	prefix := addr[:cut]
	if strings.HasPrefix(addr, "/") {
		return strings.TrimPrefix(prefix, "/"), name
	}
	if own == "" {
		return prefix, name
	}
	return own + "/" + prefix, name
}

func (r compiled) block(key string) map[string]any {
	m, _ := r.fields[key].(map[string]any)
	return m
}

// checkReachable: an ingress names a workload, or something delivers work to it, or
// nothing ever starts it.
func checkReachable(all []compiled, byKind map[string][]compiled) []Finding {
	fronted := map[string]bool{}
	for _, res := range all {
		for _, prefix := range []string{"route/handler", "forward"} {
			kind, name := res.str(prefix+"/kind"), res.str(prefix+"/name")
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
		for _, probe := range []string{"readiness/port", "liveness/port"} {
			if p := res.str(probe); p != "" {
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
					if p, _ := entry["deliver/port"].(string); p != "" {
						used[p] = true
					}
				}
			}
		}
		for _, other := range all {
			for _, prefix := range []string{"route/handler", "forward"} {
				if other.str(prefix+"/name") == res.name {
					if p := other.str(prefix + "/port"); p != "" {
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
				if p, _ := entry["deliver/port"].(string); p != "" {
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
		if res.str("route/handler/kind") != "website" {
			continue
		}
		path := res.str("route/path")
		if path == "" || path == "/" {
			continue
		}
		if strip, _ := res.fields["route/strip-prefix"].(bool); strip {
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
	type use struct{ name, scope, wants, where string }
	var uses []use
	for _, res := range all {
		if n := res.str("authorizer/name"); n != "" {
			uses = append(uses, use{n, res.str("authorizer/namespace"), "authorizer", res.kind + "/" + res.name + " authorizer"})
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
				scope, name := splitAddress(addr(el, "function"), res.scope)
				uses = append(uses, use{name, scope, wants, res.kind + "/" + res.name + " transformers"})
			}
		}
		if res.str("route/handler/kind") == "function" {
			uses = append(uses, use{res.str("route/handler/name"), res.str("route/handler/namespace"), "handler", res.kind + "/" + res.name + " route"})
		}
	}

	var out []Finding
	for _, u := range uses {
		got, known := lookupWorld(world, u.name, u.scope)
		if !known {
			// The entry's KEY is the function's name, and a key is not a value — no
			// Ref() resolves it, so a typo here compiles clean and the transformer or
			// authorizer simply never runs. Silent, and in the authorizer's case
			// silently unguarded.
			out = append(out, Finding{Broken, "function", u.name, u.scope,
				fmt.Sprintf("is named as a %s by %s, and does not exist", u.wants, u.where),
				"The name is the key of the entry, and a key is not a reference the compiler resolves — so this one names nothing and the step never runs."})
			continue
		}
		if got == u.wants {
			continue
		}
		out = append(out, Finding{Broken, "function", u.name, u.scope,
			fmt.Sprintf("is used as a %s by %s, and implements %s", u.wants, u.where, got),
			"Those are different host interfaces. The function will be handed a world it was not built to read, which compiles clean and fails on live traffic."})
	}
	return out
}

// lookupWorld resolves a named function the way the config does: an absolute path, a
// path relative to the user's scope, or own-scope-then-parents when it names none.
func lookupWorld(world map[string]string, name, scope string) (string, bool) {
	scope = strings.TrimPrefix(scope, "/")
	for {
		if w, ok := world[scope+"\x00"+name]; ok {
			return w, true
		}
		if scope == "" {
			return "", false
		}
		cut := strings.LastIndex(scope, "/")
		if cut < 0 {
			scope = ""
			continue
		}
		scope = scope[:cut]
	}
}

// checkHandlerPorts: only a workload that LISTENS has ports. A route naming a port on
// a website or a function is pointing at something that does not exist — the field is
// on the handler block for every kind, because which kinds have ports is not something
// the schema can condition on.
func checkHandlerPorts(all []compiled) []Finding {
	var out []Finding
	for _, res := range all {
		for _, prefix := range []string{"route/handler", "forward"} {
			port := res.str(prefix + "/port")
			kind := res.str(prefix + "/kind")
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
			_, name := splitAddress(addr(el, "function"), res.scope)
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
			if err := r.ImagePullable(image, res.str("image-pull-secret")); err != nil {
				out = append(out, Finding{Broken, res.kind, res.name, res.scope,
					fmt.Sprintf("image %q cannot be pulled", image),
					err.Error()})
			}
		}
	}
	return out
}
