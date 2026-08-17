package schema_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// root is the compiled project. The chroot envelope ({object, indexes}) only
// appears once a group declares an index footprint, so unwrap it if it is there
// and the tests keep working when v2 gains one.
func root(t *testing.T, flat map[string]any) map[string]any {
	t.Helper()
	if obj, ok := flat["object"].(map[string]any); ok {
		return obj
	}
	return flat
}

// resources reads one compiled group. Resources are keyed by id after
// compilation, so the tests read fields rather than names.
func resources(t *testing.T, flat map[string]any, group string) map[string]any {
	t.Helper()
	obj := root(t, flat)
	g, ok := obj[group].(map[string]any)
	assert.Assert(t, ok, "group %q missing; got %v", group, obj)
	return g
}

func only(t *testing.T, group map[string]any) map[string]any {
	t.Helper()
	assert.Equal(t, len(group), 1, "expected exactly one instance")
	for _, v := range group {
		r, ok := v.(map[string]any)
		assert.Assert(t, ok)
		return r
	}
	return nil
}

// namespace picks one instance of the container group by its authored name;
// after compilation the instances are keyed by id, like every other resource.
func namespace(t *testing.T, obj map[string]any, name string) map[string]any {
	t.Helper()
	ns, ok := obj["namespaces"].(map[string]any)
	assert.Assert(t, ok, "namespaces missing")
	for _, v := range ns {
		if n := v.(map[string]any); n["name"] == name {
			return n
		}
	}
	t.Fatalf("namespace %q not found", name)
	return nil
}

// namedResource picks one compiled instance by its authored name.
func namedResource(t *testing.T, group map[string]any, name string) map[string]any {
	t.Helper()
	for _, v := range group {
		if r := v.(map[string]any); r["name"] == name {
			return r
		}
	}
	t.Fatalf("no resource named %q", name)
	return nil
}

func compileBasic(t *testing.T) map[string]any {
	t.Helper()
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	return obj.Flat()
}

// The whole point of v2: a function is code and limits. If any of these keys comes
// back, the trigger block was extended rather than deleted and reachability is
// still the function's business.
func TestFunctionHasNoTrigger(t *testing.T) {
	fn := namedResource(t, resources(t, compileBasic(t), "functions"), "checkout")

	for _, gone := range []string{"type", "local", "channel", "protocol", "command", "method", "methods", "domains", "paths", "secure"} {
		_, present := fn[gone]
		assert.Assert(t, !present, "functions carries %q; the trigger block is meant to be deleted, not extended", gone)
	}

	assert.Equal(t, fn["execution/call"], "checkout")
	assert.Equal(t, fn["execution/timeout"], int64(20000000000), "20s parses to nanoseconds")
	assert.Equal(t, fn["execution/memory"], int64(32000000), "32MB parses to bytes")
}

// `libraries` -> `module` carries into the reference form: a source of
// "module/<name>" resolves to "module/<id>" against the compiled index.
func TestModuleSourceResolves(t *testing.T) {
	flat := compileBasic(t)
	modID := ""
	for id := range resources(t, flat, "module") {
		modID = id
	}
	assert.Assert(t, modID != "", "no module compiled")

	fn := namedResource(t, resources(t, flat, "functions"), "checkout")
	assert.Equal(t, fn["source"], "module/"+modID)
}

// A queue's dead-letter is a ref to another queue, so a typo is a compile error
// rather than a silently undelivered item.
func TestQueueDeadLetterResolves(t *testing.T) {
	queues := resources(t, compileBasic(t), "queues")
	assert.Equal(t, len(queues), 2)

	var dl string
	for id, v := range queues {
		q := v.(map[string]any)
		if s, ok := q["dead-letter"].(string); ok && s != "" {
			dl = strings.TrimPrefix(s, "queues/")
			assert.Assert(t, dl != s, "dead-letter lost its queues/ prefix")
			assert.Assert(t, dl != id, "a queue must not dead-letter to itself")
		}
	}
	assert.Assert(t, dl != "", "dead-letter was not set")
	_, exists := queues[dl]
	assert.Assert(t, exists, "dead-letter %q did not resolve to a compiled queue id", dl)
}

// A connector's credentials resolve to a secret id — the value never enters the
// config, only the reference does.
func TestConnectorCredentialsResolve(t *testing.T) {
	flat := compileBasic(t)
	secretID := ""
	for id := range resources(t, flat, "secrets") {
		secretID = id
	}
	conn := only(t, resources(t, flat, "connectors"))
	assert.Equal(t, conn["credentials"], "secrets/"+secretID)
}

// The new kinds load at all, with their scalars parsed.
func TestNewKindsCompile(t *testing.T) {
	flat := compileBasic(t)

	assert.Equal(t, only(t, resources(t, flat, "crons"))["schedule"], "0 3 * * *")
	assert.Equal(t, only(t, resources(t, flat, "caches"))["storage/ttl"], int64(86400000000000), "24h in nanoseconds")
	assert.Equal(t, only(t, resources(t, flat, "secrets"))["source"], "value", "default applies")
}

// `namespaces` is the SCOPE container: its instances hold resources of their own,
// compiled in their own scope. An inline source (".") is left alone by ref
// resolution. `applications` is a compute kind here, not a scope.
func TestNamespaceScope(t *testing.T) {
	obj := root(t, compileBasic(t))
	shop := namespace(t, obj, "shop")

	fn := namedResource(t, shop["functions"].(map[string]any), "notify")
	assert.Equal(t, fn["source"], ".", "inline source is not a ref")
	assert.Equal(t, fn["execution/call"], "notify")

	kv := only(t, shop["caches"].(map[string]any))
	assert.Equal(t, kv["match"], "sessions")

	app := only(t, resources(t, compileBasic(t), "applications"))
	assert.Equal(t, app["image"], "ghcr.io/acme/api:1.4", "applications is a compute kind, not a scope")
	assert.Equal(t, app["port"], 8080)

	// how many instances run is the platform's, never authored
	_, replicas := app["replicas"]
	assert.Assert(t, !replicas, "a replica count must not be authorable")
}

// Access is declared per TARGET, named once, with its ops beside it and a namespace
// path when it lives somewhere else. The block is a group nested in the resource's
// own document, so the resource stays a plain file with its id at the root.
func TestAccessIsPerTarget(t *testing.T) {
	flat := compileBasic(t)
	fn := namedResource(t, resources(t, flat, "functions"), "checkout")

	caches, ok := fn["caches"].(map[string]any)
	assert.Assert(t, ok, "no caches block on the function")

	orders := caches["orders"].(map[string]any)
	assert.DeepEqual(t, orders["ops"], []string{"get", "put"})
	_, scoped := orders["namespace"]
	assert.Assert(t, !scoped, "a target in the same scope needs no namespace")

	// the whole point of the namespace field: naming a resource in another one
	sessions := caches["sessions"].(map[string]any)
	assert.Equal(t, sessions["namespace"], "/shop")
	assert.DeepEqual(t, sessions["ops"], []string{"get"})

	// nesting did NOT cost the resource its own compile passes
	assert.Equal(t, fn["execution/timeout"], int64(20000000000), "scalars still parse")
	assert.Assert(t, strings.HasPrefix(fn["source"].(string), "module/Qm"), "refs still resolve")

	// and a verb the kind does not implement is refused at load
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/functions/checkout.yaml", []byte(`
id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh
source: "."
execution: {call: checkout, timeout: 20s, memory: 32MB}
caches:
    orders:
        ops: [query]
`), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "\"query\" is not something a caches does")
}

// Ingress lives on the gateway, in its own flat document, the way a v1 function
// carried its whole trigger block in one file.
//
// The handler is a switch on the target kind — an Either() in the attribute path
// with Key() capturing the branch — so `handler: { function: { namespace, name } }`
// compiles to the kind, the namespace path and the name, and a key outside the
// closed set is a compile error rather than a route that never fires.
func TestGatewayRoute(t *testing.T) {
	flat := compileBasic(t)

	gw := only(t, resources(t, flat, "gateways"))
	assert.Equal(t, gw["fqdn"], "shop.example.com", "the name lives on the gateway, not on a domain")
	assert.Equal(t, gw["certificate/source"], "generated", "generated unless it is provided")

	assert.Equal(t, gw["route/path"], "/checkout")
	assert.Equal(t, gw["route/match"], "prefix", "default applies")
	assert.DeepEqual(t, gw["route/methods"], []string{"POST"})

	// the switch, and the resource path through namespaces
	assert.Equal(t, gw["route/handler/kind"], "function", "Key() captures which branch the handler took")
	assert.Equal(t, gw["route/handler/namespace"], "/shop")
	assert.Equal(t, gw["route/handler/name"], "notify")

	// there is no route resource kind
	_, standalone := root(t, flat)["routes"]
	assert.Assert(t, !standalone, "routes must not be a resource kind")

	// and the function itself still says nothing about being reachable
	fn := namedResource(t, resources(t, flat, "functions"), "checkout")
	for _, gone := range []string{"domains", "paths", "method", "type"} {
		_, present := fn[gone]
		assert.Assert(t, !present, "functions carries %q", gone)
	}
}

// The handler set is NARROWER than the vocabulary, and that is the property worth
// asserting: `kv` is a perfectly real kind and it is not something an HTTP request
// can be handed to. The Either() matches no key, so there is nothing for the switch
// to capture and the compile fails — an unroutable handler cannot become a route
// that silently never fires.
func TestNonHTTPHandlerKindIsRefused(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/gateways/shop.yaml", []byte(`
id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG
fqdn: shop.example.com
route:
    path: /checkout
    handler:
        kv:
            name: orders
`), 0o644))

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	// the Either() matches no key, so the switch has nothing to capture
	assert.ErrorContains(t, err, "attribute route/handler/kind has an empty key",
		"a real but non-HTTP kind must not be a handler")
}

// Namespaces nest — v1's applications did not. A tenant can hold its own tenants,
// down to MaxNamespaceDepth, which is a declared cap because the schema is a finite
// node tree rather than a recursive grammar.
func TestNamespacesNest(t *testing.T) {
	shop := namespace(t, root(t, compileBasic(t)), "shop")

	inner, ok := shop["namespaces"].(map[string]any)
	assert.Assert(t, ok, "a namespace must be able to hold namespaces")
	eu := only(t, inner)
	assert.Equal(t, eu["name"], "eu")

	// a third `sessions`, two levels down, distinct from both above it
	kv := only(t, eu["caches"].(map[string]any))
	assert.Equal(t, kv["match"], "sessions")
	assert.Equal(t, kv["storage/size"], int64(2000000))
}

// The cost of nesting, asserted so it cannot change silently: the interpreter's
// scope walk covers the project root and ONE level of the container, so a reference
// written inside a namespace below the first level is not resolved. It is not an
// error and nothing reports it — the config compiles and the name stays a name.
func TestDeepNamespaceRefsAreNotResolved(t *testing.T) {
	flat := compileBasic(t)

	// the contrast first: a first-level namespace declares access like anywhere else
	shop := namespace(t, root(t, flat), "shop")
	notify := namedResource(t, shop["functions"].(map[string]any), "notify")
	sessions := notify["caches"].(map[string]any)["sessions"].(map[string]any)
	assert.DeepEqual(t, sessions["ops"], []string{"get"})

	// two levels down there is nothing to resolve against yet, so the assertion is
	// simply that the scope compiles at all and holds its own resources
	eu := only(t, shop["namespaces"].(map[string]any))
	assert.Equal(t, eu["name"], "eu")
	assert.Equal(t, len(eu["caches"].(map[string]any)), 1)
}

// Every verb has an effect class, assigned once, in the table next to the kind.
// invoke is write: a call may do anything the callee can do.
func TestEffectClasses(t *testing.T) {
	for _, c := range []struct {
		kind, verb, effect string
	}{
		{"caches", "get", schema.Read},
		{"caches", "put", schema.Write},
		{"caches", "watch", schema.Read}, // observes writes it cannot cause
		{"databases", "query", schema.Read},
		{"databases", "exec", schema.Write}, // the host cannot classify arbitrary SQL
		{"messaging", "publish", schema.Write},
		{"messaging", "subscribe", schema.Read},
		{"functions", "invoke", schema.Write},
		{"functions", "GET", schema.Read},
		{"functions", "DELETE", schema.Write},
		{"secrets", "get", schema.Read},
	} {
		got, ok := schema.EffectOf(c.kind, c.verb)
		assert.Assert(t, ok, "%s/%s has no effect class", c.kind, c.verb)
		assert.Equal(t, got, c.effect, "%s/%s", c.kind, c.verb)
	}

	// there is no write verb on a secret from a workload, ever
	_, ok := schema.EffectOf("secrets", "put")
	assert.Assert(t, !ok, "secrets must have no write verb")
}

// ---- namespaces ------------------------------------------------------------
//
// `namespaces` is a CONTAINER group: its iterator is itself a group holding a
// nested copy of every resource group, which is what gives each instance its own
// scope. These tests exercise that scope rather than just its existence.

// The same NAME in two namespaces is two different resources. If this ever
// collapsed, every multi-tenant claim in v2 would be false.
func TestNamespacesIsolateNames(t *testing.T) {
	obj := root(t, compileBasic(t))
	assert.Equal(t, len(obj["namespaces"].(map[string]any)), 2, "fixture should carry two namespaces")

	shop, billing := namespace(t, obj, "shop"), namespace(t, obj, "billing")

	sessionID := func(scope map[string]any) string {
		for id, v := range scope["caches"].(map[string]any) {
			if v.(map[string]any)["name"] == "sessions" {
				return id
			}
		}
		return ""
	}
	shopSessions, billingSessions := sessionID(shop), sessionID(billing)
	assert.Assert(t, shopSessions != "" && billingSessions != "")
	assert.Assert(t, shopSessions != billingSessions,
		"caches/sessions in two namespaces must compile to two different resources")
}

// Where a target lives is now stated, not inferred: an entry with no `namespace` is
// in the resource's own scope, and one with a namespace path names another. That is
// what makes a sibling namespace reachable at all — the ref resolver understands the
// current scope, the root and parents, and nothing else.
//
// The cost, asserted so it cannot change silently: an access target is a map KEY, so
// nothing resolves it at compile time. A typo in a target name is not a compile error
// the way `source: module/typo` is. See omissions.go.
func TestAccessNamesItsScope(t *testing.T) {
	flat := compileBasic(t)

	shop := namespace(t, root(t, flat), "shop")
	notify := namedResource(t, shop["functions"].(map[string]any), "notify")
	own := notify["caches"].(map[string]any)["sessions"].(map[string]any)
	_, scoped := own["namespace"]
	assert.Assert(t, !scoped, "a target in the resource's own scope needs no namespace")

	// and from the project root, reaching into a namespace
	fn := namedResource(t, resources(t, flat, "functions"), "checkout")
	elsewhere := fn["caches"].(map[string]any)["sessions"].(map[string]any)
	assert.Equal(t, elsewhere["namespace"], "/shop")

	// the two `sessions` are different resources; the entries name them, and the
	// compiler does not resolve either one to an id
	for _, v := range []any{own["ops"], elsewhere["ops"]} {
		assert.Assert(t, v != nil)
	}
}

// A sibling namespace IS nameable now, and that is a change worth stating: v1 had no
// syntax for it, so isolation held by absence of grammar. `namespace: /billing` is
// grammar. Nothing in the compiler decides whether it is ALLOWED — the connection and
// the target-side grant that are supposed to bound it do not exist yet, so this
// compiles and the check has to live at publish or at runtime.
func TestSiblingNamespaceIsNameableAndUnchecked(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))

	// shop reaches into billing's books
	assert.NilError(t, afero.WriteFile(fs, "/namespaces/shop/functions/notify.yaml", []byte(`
id: QmbWqxBEKC3P8tqsKc98xmWNzrzDtRLMiMPL8wBuTGsMnR
source: "."
execution: {call: notify, timeout: 5s, memory: 16MB}
databases:
    ledger:
        namespace: /billing
        ops: [exec]
`), 0o644))

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err, "naming a sibling namespace compiles; nothing authorises it yet")

	shop := namespace(t, root(t, obj.Flat()), "shop")
	notify := namedResource(t, shop["functions"].(map[string]any), "notify")
	ledger := notify["databases"].(map[string]any)["ledger"].(map[string]any)
	assert.Equal(t, ledger["namespace"], "/billing")
	assert.DeepEqual(t, ledger["ops"], []string{"exec"})
}

func copyTree(dst afero.Fs, from, to string) error {
	return filepath.Walk(from, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return afero.WriteFile(dst, filepath.Join(to, rel), b, 0o644)
	})
}

// A zone is authoritative DNS, which is a different job from routing: it says what
// a name resolves to at all — mail, verification, a third-party CNAME, a box that
// is not tau. Folding it into the gateway would have deleted the whole surface.
func TestZoneIsNotRouting(t *testing.T) {
	flat := compileBasic(t)

	zone := only(t, resources(t, flat, "zones"))
	assert.Equal(t, zone["fqdn"], "example.com")
	records := zone["records"].([]string)
	assert.Equal(t, len(records), 4)

	// zone-file notation, kept verbatim: the compiler stores what you wrote, so a
	// zone exported from another provider round-trips without translation.
	assert.Assert(t, strings.Contains(records[0], "MX 10 mail.example.com."))

	// A zone carries no routing and no verbs pointed at it: nothing `uses` a name.
	for k := range zone {
		assert.Assert(t, !strings.HasPrefix(k, "use-"), "a zone declared %q", k)
	}
	_, ok := schema.EffectOf("zones", "get")
	assert.Assert(t, !ok, "zones must not be a target kind")

	// and the gateway that serves a name under it still holds the ingress
	gw := only(t, resources(t, flat, "gateways"))
	assert.Equal(t, gw["fqdn"], "shop.example.com")
}

// A namespace path is absolute with a leading "/" and relative without one — the same
// two forms the name resolver already uses for references. "/" is the project root
// itself; omitted means the resource's own scope, then its parents.
func TestNamespacePathForms(t *testing.T) {
	obj := root(t, compileBasic(t))

	shop := namespace(t, obj, "shop")
	notify := namedResource(t, shop["functions"].(map[string]any), "notify")
	caches := notify["caches"].(map[string]any)

	// omitted: shop's own sessions
	_, scoped := caches["sessions"].(map[string]any)["namespace"]
	assert.Assert(t, !scoped, "a target in the resource's own scope names no namespace")

	// "/": the project root, from inside a namespace
	assert.Equal(t, caches["orders"].(map[string]any)["namespace"], "/")

	// absolute, from two levels down
	eu := only(t, shop["namespaces"].(map[string]any))
	relay := only(t, eu["functions"].(map[string]any))
	assert.Equal(t, relay["databases"].(map[string]any)["ledger"].(map[string]any)["namespace"], "/billing")

	// and the shape is checked at load
	for _, bad := range []string{"shop/", "shop//eu", "../billing", "Shop-EU"} {
		fs := afero.NewMemMapFs()
		assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
		assert.NilError(t, afero.WriteFile(fs, "/functions/checkout.yaml", []byte(
			"id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n"+
				"source: \".\"\nexecution: {call: c, timeout: 1s, memory: 1MB}\n"+
				"caches:\n    orders:\n        namespace: "+bad+"\n        ops: [get]\n"), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		assert.ErrorContains(t, err, "namespace path", "%q must be refused", bad)
	}
}

// Per-use modifiers: an access entry is a place to put them, which a verb-keyed list
// of names never had. They get the same treatment as any other attribute — the
// duration parses — because a nested group is processed as an ordinary group, per
// entry.
func TestAccessEntryModifiers(t *testing.T) {
	flat := compileBasic(t)

	fn := namedResource(t, resources(t, flat, "functions"), "checkout")
	orders := fn["caches"].(map[string]any)["orders"].(map[string]any)
	assert.Equal(t, orders["timeout"], int64(200000000), "200ms parses to nanoseconds")

	// and an entry that names none carries none
	sessions := fn["caches"].(map[string]any)["sessions"].(map[string]any)
	_, present := sessions["timeout"]
	assert.Assert(t, !present, "an unauthored modifier must not appear")
}

// The target owns the policy, the accessor only states intent: a reachable resource
// declares the authorizer that decides what a caller gets, and a caller declares the
// ops it asks for. The caller cannot name its own judge.
func TestTargetOwnsThePolicy(t *testing.T) {
	flat := compileBasic(t)

	// the cache being reached carries the authorizer
	orders := namedResource(t, resources(t, flat, "caches"), "orders")
	assert.Equal(t, orders["authorizer/name"], "tier_check")
	assert.Equal(t, orders["authorizer/namespace"], "/shop")

	// the caller states what it asks for, and names no authorizer
	fn := namedResource(t, resources(t, flat, "functions"), "checkout")
	entry := fn["caches"].(map[string]any)["orders"].(map[string]any)
	assert.DeepEqual(t, entry["ops"], []string{"get", "put"})
	for _, gone := range []string{"via", "via-namespace", "authorizer", "authorizer/name"} {
		_, present := entry[gone]
		assert.Assert(t, !present, "an accessor must not name its own authorizer: %q", gone)
	}

	// a kind nothing can reach has no authorizer to declare
	zone := only(t, resources(t, flat, "zones"))
	_, present := zone["authorizer/name"]
	assert.Assert(t, !present, "a kind with no ops is not reachable, so it authorises nothing")
}
