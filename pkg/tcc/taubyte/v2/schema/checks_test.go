package schema_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/spf13/afero"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

func auditDocs(t *testing.T, r schema.Resolver, docs map[string]string) []schema.Finding {
	t.Helper()
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
	for path, body := range docs {
		assert.NilError(t, afero.WriteFile(fs, path, []byte(body), 0o644))
	}
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	return schema.Audit(obj.Flat(), r)
}

func has(t *testing.T, found []schema.Finding, sev schema.Severity, substr string) {
	t.Helper()
	for _, f := range found {
		if f.Severity == sev && strings.Contains(f.Title, substr) {
			return
		}
	}
	t.Fatalf("no %s finding containing %q; got %v", sev, substr, found)
}

func hasNo(t *testing.T, found []schema.Finding, substr string) {
	t.Helper()
	for _, f := range found {
		if strings.Contains(f.Title, substr) {
			t.Fatalf("unexpected finding %q", f)
		}
	}
}

const appBody = "id: QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY\nsource:\n    image: img\nexecution:\n    memory: 1MB\n"

// An http workload nothing fronts answers nobody. A consumer in the same position is
// correct, and the difference is the whole reason severity is not a count.
func TestAuditReachability(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{"/application/lonely.yaml": appBody})
	has(t, found, schema.Broken, "nothing reaches it")

	found = auditDocs(t, nil, map[string]string{
		"/sync/work.yaml":     "id: QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o\nqueue:\n    consensus: raft\n    visibility_timeout: 30s\n",
		"/application/w.yaml": appBody + "ports:\n    work:\n        number: 8080\nsync:\n    work:\n        ops: [receive]\n        deliver:\n            port: work\n            path: /w\n",
	})
	has(t, found, schema.Note, "nothing fronts it, and nothing needs to")
	hasNo(t, found, "nothing reaches it")
}

// A container told to consume has to say where each item goes; a WASM workload is
// dispatched and needs no port at all.
func TestAuditDelivery(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{
		"/sync/work.yaml":     "id: QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o\nqueue:\n    consensus: raft\n    visibility_timeout: 30s\n",
		"/application/w.yaml": appBody + "sync:\n    work:\n        ops: [receive]\n",
	})
	has(t, found, schema.Broken, "declares no deliver port")

	found = auditDocs(t, nil, map[string]string{
		"/sync/work.yaml":  "id: QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o\nqueue:\n    consensus: raft\n    visibility_timeout: 30s\n",
		"/function/f.yaml": "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\nexecution: {call: c, timeout: 1s, memory: 1MB}\nsync:\n    work:\n        ops: [receive]\n",
	})
	hasNo(t, found, "deliver port")
}

// The mutual exclusion the DSL cannot state.
func TestAuditVariables(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{
		"/secret/k.yaml":      "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy\n",
		"/application/a.yaml": appBody + "variables:\n    BOTH:\n        value: x\n        secret:\n            name: k\n    NEITHER: {}\n",
	})
	has(t, found, schema.Broken, "has both a value and a secret")
	has(t, found, schema.Broken, "has neither a value nor a secret")
}

// What tcc cannot see, it asks. The stub says yes so an unwired check never blocks a
// compile; a resolver that says no is how the edge case is exercised, with no network.
type refusing struct{}

func (refusing) ZoneDelegated(fqdn string) error { return errors.New("no NS records point here") }
func (refusing) ImagePullable(image, secret string) error {
	if secret == "" {
		return errors.New("private registry and no pull secret")
	}
	return nil
}

func TestAuditAsksTheResolver(t *testing.T) {
	docs := map[string]string{
		"/application/a.yaml": appBody,
		"/zone/z.yaml":        "id: QmRgVLmWTBpgTbCPnHRUvKGnHqHf7Yd7t8ZffKeRXPkRLL\nfqdn: example.com\n",
	}

	// the stub answers yes, so neither is reported
	found := auditDocs(t, nil, docs)
	hasNo(t, found, "does not answer here")
	hasNo(t, found, "cannot be pulled")

	found = auditDocs(t, refusing{}, docs)
	has(t, found, schema.Broken, "does not answer here")
	has(t, found, schema.Broken, "cannot be pulled")
}

// The fixture is the example the tests compile, so it should stay clean of anything
// broken — and its two standing findings are both deliberate.
func TestAuditOfTheExample(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	for _, f := range schema.Audit(obj.Flat(), nil) {
		assert.Assert(t, f.Severity != schema.Broken, "the example must compile to something that works: %v", f)
	}
}

// A site built for / and mounted at /docs 404s every page while the config reads as
// correct. Both halves are in the compiled object, so tcc is what can say so.
func TestAuditMountedWebsite(t *testing.T) {
	site := "id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv\nsource:\n    git: https://github.com/acme/docs\n    branch: main\n"
	gw := func(extra string) string {
		return "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nroute:\n    path: /docs\n" + extra + "    handler:\n        website:\n            name: docs\n"
	}

	found := auditDocs(t, nil, map[string]string{"/website/docs.yaml": site, "/gateway/g.yaml": gw("")})
	has(t, found, schema.Check, "does not strip the prefix")

	found = auditDocs(t, nil, map[string]string{"/website/docs.yaml": site, "/gateway/g.yaml": gw("    strip_prefix: true\n")})
	hasNo(t, found, "strip the prefix")

	// and at the root there is no prefix to strip
	found = auditDocs(t, nil, map[string]string{
		"/website/docs.yaml": site,
		"/gateway/g.yaml":    "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nroute:\n    path: /\n    handler:\n        website:\n            name: docs\n",
	})
	hasNo(t, found, "strip the prefix")
}

// An SPA and a 404 page are the same document with opposite status codes.
func TestWebsiteServingMode(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/website/app.yaml", []byte(
		"id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv\nsource:\n    git: https://github.com/acme/app\n    branch: main\nserve:\n    mode: spa\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	site := only(t, resources(t, obj.Flat(), "website"))
	assert.Equal(t, nested(site, "serve", "mode"), "spa")
	assert.Equal(t, nested(site, "serve", "index"), "index.html", "the default a directory request returns")
}

// Every USE of a function is a place tcc can see, and the function says which world it
// was built against — so "compiles clean, fails on live traffic" becomes an error at
// compile.
func TestAuditHostWorlds(t *testing.T) {
	fn := func(world string) string {
		body := "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\nexecution: {call: c, timeout: 1s, memory: 1MB}\n"
		if world != "" {
			body += "implements: " + world + "\n"
		}
		return body
	}
	gw := "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nroute:\n    path: /\n    handler:\n        application:\n            name: a\n            port: p\ntransformers:\n    - name: t\n"
	app := appBody + "ports:\n    p:\n        number: 80\n"

	// built for a gateway, used by a gateway
	found := auditDocs(t, nil, map[string]string{
		"/function/t.yaml": fn("gateway-transformer"), "/gateway/g.yaml": gw, "/application/a.yaml": app,
	})
	hasNo(t, found, "is used as a")

	// built as a plain handler, used as a transformer
	found = auditDocs(t, nil, map[string]string{
		"/function/t.yaml": fn(""), "/gateway/g.yaml": gw, "/application/a.yaml": app,
	})
	has(t, found, schema.Broken, "is used as a gateway-transformer")

	// built for a gateway, used by a proxy
	found = auditDocs(t, nil, map[string]string{
		"/function/t.yaml":    fn("gateway-transformer"),
		"/application/a.yaml": app,
		"/proxy/p.yaml":       "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nhost:\n    generated:\n        name: db\ntransformers:\n    - name: t\nforward:\n    application:\n        name: a\n        port: p\n",
	})
	has(t, found, schema.Broken, "is used as a proxy-transformer")
}

// EVERY USE OF A FUNCTION IS A PLACE THE AUDIT CAN SEE — and two of them were not
// being looked at. A per-route router is a second router site beside the gateway-wide
// one, and an admission validator is the only use of `instance-validator`, the world
// that exists so a validator need not be a kind. Both compiled clean naming a function
// built against something else, which is the failure this check exists to prevent.
func TestAuditHostWorldsReachesEveryUse(t *testing.T) {
	fn := func(world string) string {
		b := "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nexecution:\n    call: c\n    timeout: 1s\n    memory: 1MB\n"
		if world != "" {
			b += "implements: " + world + "\n"
		}
		return b
	}
	cacheWith := func(validator string) string {
		return "id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nsize: 32MB\nttl: 24h\nconsensus: crdt\ninstances:\n    limit: 0\n    validator:\n        name: " + validator + "\n"
	}
	routeRouter := func(router string) string {
		return "id: QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o\nroute:\n    path: /\n    router:\n        name: " + router + "\n"
	}

	// An admission validator built as a plain handler.
	found := auditDocs(t, nil, map[string]string{"/function/v.yaml": fn(""), "/cache/c.yaml": cacheWith("v")})
	has(t, found, schema.Broken, "is used as a instance-validator")

	// Built for the job: no finding.
	found = auditDocs(t, nil, map[string]string{"/function/v.yaml": fn("instance-validator"), "/cache/c.yaml": cacheWith("v")})
	hasNo(t, found, "instance-validator")

	// A PER-ROUTE router, which is a different key from the gateway-wide one.
	found = auditDocs(t, nil, map[string]string{"/function/r.yaml": fn("authorizer"), "/gateway/g.yaml": routeRouter("r")})
	has(t, found, schema.Broken, "is used as a gateway-router")

	found = auditDocs(t, nil, map[string]string{"/function/r.yaml": fn("gateway-router"), "/gateway/g.yaml": routeRouter("r")})
	hasNo(t, found, "is used as a")

	// A NAME THAT RESOLVES TO NOTHING COMPILES CLEAN. Ref() drops it rather than
	// refusing it, on both branches, which is why the audit has to say so.
	found = auditDocs(t, nil, map[string]string{"/cache/c.yaml": cacheWith("ghost")})
	has(t, found, schema.Broken, "does not exist")

	found = auditDocs(t, nil, map[string]string{"/gateway/g.yaml": routeRouter("ghost")})
	has(t, found, schema.Broken, "does not exist")
}

// Only a container listens on ports. A route naming one on a website points at
// something that does not exist — the field is on the handler block for every kind
// because which kinds have ports is not something the schema can condition on.
func TestAuditHandlerPorts(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{
		"/website/w.yaml": "id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv\nsource:\n    git: https://github.com/acme/w\n    branch: main\n",
		"/gateway/g.yaml": "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nroute:\n    path: /\n    handler:\n        website:\n            name: w\n            port: http\n",
	})
	has(t, found, schema.Broken, "names port \"http\" on a website")
}

// A file has to be mounted somewhere the workload can open it.
func TestAuditVariableAsFile(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{
		"/secret/k.yaml":      "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy\n",
		"/application/a.yaml": appBody + "variables:\n    CERT:\n        secret:\n            name: k\n        as: file\n",
	})
	has(t, found, schema.Broken, "names no path")

	found = auditDocs(t, nil, map[string]string{
		"/secret/k.yaml":      "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy\n",
		"/application/a.yaml": appBody + "variables:\n    CERT:\n        secret:\n            name: k\n        as: file\n        path: /etc/tls/cert.pem\n",
	})
	hasNo(t, found, "names no path")
}

// The entry's KEY is the function's name, and a key is not a value — nothing resolves
// it — so a typo compiles clean and the step never runs. In the authorizer's case, it
// never runs and the resource is silently unguarded.
func TestAuditGhostFunctions(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{
		"/application/a.yaml": appBody + "ports:\n    p:\n        number: 80\n",
		"/gateway/g.yaml":     "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nroute:\n    path: /\n    handler:\n        application:\n            name: a\n            port: p\ntransformers:\n    - name: ghost\n",
		"/cache/c.yaml":       "id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nsize: 1MB\nconsensus: crdt\nauthorizer:\n    name: alsoghost\n",
	})
	has(t, found, schema.Broken, "is named as a gateway-transformer by gateway/g transformers, and does not exist")
	has(t, found, schema.Broken, "is named as a authorizer by cache/c authorizer, and does not exist")
}

// A VOLUME CAN BE SHARED, so nothing here has anything to say about two workloads
// mounting one read-write, or about one workload the platform may run ten instances of.
// This is the audit that used to refuse both, kept as the case with its expectation
// flipped: POSIX answers concurrent access — flock, fcntl, atomic rename — and a lease
// above the filesystem would be a second, weaker answer to a question the layer below
// already answers.
func TestAVolumeMayBeSharedByManyWriters(t *testing.T) {
	const vol = "id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nquota: 1GB\n"
	mount := func(op string) string {
		return appBody + "volume:\n    data:\n        ops: [" + op + "]\n        path: /data\n"
	}

	found := auditDocs(t, nil, map[string]string{
		"/volume/data.yaml":   vol,
		"/application/a.yaml": mount("mount-rw"),
		"/application/b.yaml": strings.Replace(mount("mount-rw"), "QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY", "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG", 1),
	})
	hasNo(t, found, "mounted for writing")
	hasNo(t, found, "may run")

	found = auditDocs(t, nil, map[string]string{
		"/volume/data.yaml":   vol,
		"/application/a.yaml": mount("mount-rw") + "limits:\n    max_instances: 10\n",
	})
	hasNo(t, found, "instances")

	// The two verbs still mean different things, which is why they stay two verbs.
	ro, rw := false, false
	for _, v := range schema.OpsOf("volume") {
		switch v.Name {
		case "mount-ro":
			ro = v.Mounted && v.Effect == schema.Read
		case "mount-rw":
			rw = v.Mounted && v.Effect == schema.Write
		}
	}
	assert.Assert(t, ro && rw, "a volume declares a read mount and a write mount: %v", schema.OpsOf("volume"))
}

// A fault INSIDE a lowering is reported against what the AUTHOR wrote. The broken
// thing is a queue nobody wrote and cannot find; the finding has to name the workflow,
// the step, and which part of the lowering failed — or a generated-config diagnostic is
// worth nothing.
func TestAuditReportsALoweringFaultAgainstItsSource(t *testing.T) {
	const wf = "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nstart: reserve\nsteps:\n    - name: reserve\n      call:\n          function: %s\n      attempts: 2\n"

	found := auditDocs(t, nil, map[string]string{
		"/function/reserve.yaml":  "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\nexecution: {call: c, timeout: 1s, memory: 1MB}\n",
		"/workflow/checkout.yaml": strings.Replace(wf, "%s", "reserve", 1),
	})
	hasNo(t, found, "does not exist")

	found = auditDocs(t, nil, map[string]string{
		"/function/reserve.yaml":  "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\nexecution: {call: c, timeout: 1s, memory: 1MB}\n",
		"/workflow/checkout.yaml": strings.Replace(wf, "%s", "resrve", 1),
	})
	f := findOne(t, found, "does not exist")
	assert.Equal(t, f.Kind, "workflow", "the finding names what the author wrote")
	assert.Equal(t, f.Name, "checkout")
	assert.Assert(t, strings.Contains(f.Detail, "steps/0"), "and which step: %s", f.Detail)
	assert.Assert(t, strings.Contains(f.Detail, "function/checkout_reserve"), "and which part of the lowering: %s", f.Detail)
}

func findOne(t *testing.T, found []schema.Finding, contains string) schema.Finding {
	t.Helper()
	for _, f := range found {
		if strings.Contains(f.Title, contains) {
			return f
		}
	}
	t.Fatalf("no finding containing %q; got %v", contains, found)
	return schema.Finding{}
}

// The verb check AGENTS.md promised and nothing implemented: a lowering that asks for an
// op the target kind does not declare. It cannot live in interp — whether "receive" is
// something a queue does is this vocabulary's question — so it is asked here, and the
// vocabulary as shipped has to pass it.
func TestLoweredOpsAreVerbsOfTheirTarget(t *testing.T) {
	assert.DeepEqual(t, schema.CheckLoweredOps(schema.CompileRoot()), []string(nil))

	// and it has to actually catch one, or passing means nothing
	bad := schema.Root(nil, schema.DefineGroup("workflow",
		schema.DefineIter(nil, schema.Lowers(
			schema.Into("sync",
				schema.Named(schema.Text("x")),
				schema.Reaches("consume", schema.From("function", schema.Text("f"))),
			),
		))))
	problems := schema.CheckLoweredOps(bad)
	assert.Equal(t, len(problems), 1, "got %v", problems)
	assert.Assert(t, strings.Contains(problems[0], `"consume"`), problems[0])
	assert.Assert(t, strings.Contains(problems[0], "receive"), "it names what a queue does: %s", problems[0])
}

// recorder remembers what the audit actually handed the resolver.
type recorder struct{ pulls map[string]string }

func (r *recorder) ZoneDelegated(string) error { return nil }
func (r *recorder) ImagePullable(image, pullSecret string) error {
	if r.pulls == nil {
		r.pulls = map[string]string{}
	}
	r.pulls[image] = pullSecret
	return nil
}

// A PULL SECRET REACHES THE RESOLVER. It is a reference, so what compiles out is the
// secret's ID; if the audit reads the wrong place it hands the resolver "" and asks
// whether the image pulls ANONYMOUSLY — a different question, answered yes by every stub.
func TestPullSecretReachesTheResolver(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	r := &recorder{}
	schema.Audit(obj.Flat(), r)
	assert.Assert(t, len(r.pulls) > 0, "no image was checked at all")

	// exactly one image in the fixture is pulled with a credential
	withSecret := 0
	for image, secret := range r.pulls {
		if secret != "" {
			withSecret++
			assert.Assert(t, strings.HasPrefix(secret, "Qm"),
				"%s was checked with %q, which is not a resolved secret id", image, secret)
		}
	}
	assert.Equal(t, withSecret, 1, "the audit asked about every image anonymously: %v", r.pulls)
}

// A lowering may write `source: {builtin: …}`; an author may not. The set is closed by
// what the runtime ships, so a name an author invents is a workload whose code does
// not exist — and the compiler cannot tell one from the other by looking at the value,
// only by asking who wrote the resource.
func TestAuditAuthoredBuiltin(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/function/checkout.yaml", []byte(
		"id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n"+
			"source:\n    builtin: does_not_exist\n"+
			"execution: {call: c, timeout: 1s, memory: 1MB}\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err, "it compiles - which is the point")

	var found *schema.Finding
	for _, f := range schema.Audit(obj.Flat(), nil) {
		if f.Kind == "function" && f.Name == "checkout" {
			found = &f
			break
		}
	}
	assert.Assert(t, found != nil, "an authored builtin was not reported")
	assert.Equal(t, found.Severity, schema.Broken)
	assert.Assert(t, strings.Contains(found.Title, "does_not_exist"), "%q", found.Title)

	// and the agents and workflow steps the fixture lowers are NOT reported, because
	// a lowering is exactly who may write it
	for _, f := range schema.Audit(obj.Flat(), nil) {
		assert.Assert(t, !strings.Contains(f.Title, "built-in program \"agent\""), "%v", f)
		assert.Assert(t, !strings.Contains(f.Title, "built-in program \"step\""), "%v", f)
	}
}

// A ROUTE OR A PROXY POINTING AT NOTHING. `handler: {agent: {}}` names a kind and no
// resource: the branch key is there, so the switch captures, and every field inside it
// was optional. A router-only gateway or proxy has no such block at all, which is why
// the name is required by the branch rather than outright.
func TestATargetWithNoNameIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, path, doc, contains string }{
		{"a gateway's handler", "/gateway/ask.yaml",
			"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zg\nhost:\n    generated:\n        name: ask\nroute:\n    path: /\n    handler:\n        agent: {}\n",
			"route/handler/agent/name"},
		{"a proxy's forward", "/proxy/postgres.yaml",
			"id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nservice: postgres\nhost:\n    generated:\n        name: db\nforward:\n    application: {}\n",
			"forward/application/name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
			assert.NilError(t, afero.WriteFile(fs, tc.path, []byte(tc.doc), 0o644))
			c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
			assert.NilError(t, err)
			_, _, err = c.Compile(context.Background())
			assert.ErrorContains(t, err, tc.contains)
		})
	}
}

// THE VERB SET IS THE TYPE'S, AND THE UNION IS ONLY WHAT THE LOADER CAN HOLD IT TO.
//
// `database` declares nine verbs because it holds four data models, and an access block
// is loaded before anything knows which database an entry names. That is not a hole in
// the per-kind verb rule; it moves the question to where the target is known. This is
// the test that says so: the same document that COMPILES is the one the audit refuses,
// so it pins both halves at once — take the check out and the first assertion still
// passes, which is exactly the failure this is here to catch.
func TestDatabaseOpsAreItsTypesNotTheUnion(t *testing.T) {
	const fn = "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\nexecution: {call: c, timeout: 1s, memory: 1MB}\n"
	kv := "id: QmVLDAzEjEbfBPuoyLPWnPMhKp8ceDXwGdmMKY5W3nZWfF\nkv:\n    distributed:\n        size: 1MB\n        consensus: crdt\n"
	vec := "id: QmRZ1J1ZmVsGKvBFZoScNoGCzMwrDGHqxYAgqK7QsWpZbb\nvector:\n    dimensions: 8\n    distributed:\n        size: 1MB\n        consensus: crdt\n"

	// A kv store asked to run SQL. Both words are real verbs of the KIND, so nothing
	// at load has anything to say about it.
	found := auditDocs(t, nil, map[string]string{
		"/database/tokens.yaml": kv,
		"/function/f.yaml":      fn + "database:\n    tokens:\n        ops: [get, query]\n",
	})
	has(t, found, schema.Broken, "asks to query database/tokens, which is a kv database and has get, list, watch, put, delete")

	// …and the ops that ARE the type's raise nothing.
	found = auditDocs(t, nil, map[string]string{
		"/database/tokens.yaml": kv,
		"/function/f.yaml":      fn + "database:\n    tokens:\n        ops: [get, list, watch, put, delete]\n",
	})
	hasNo(t, found, "which is a kv database")

	// Every type answers for itself, so the check is not a kv special case.
	found = auditDocs(t, nil, map[string]string{
		"/database/mem.yaml": vec,
		"/function/f.yaml":   fn + "database:\n    mem:\n        ops: [search, exec]\n",
	})
	has(t, found, schema.Broken, "asks to exec database/mem, which is a vector database and has search, upsert, delete")
	hasNo(t, found, "asks to search")

	// A TARGET IN ANOTHER SCOPE resolves the way every other reference does, and both
	// directions matter: a written namespace, and the outward walk a bare name takes.
	// Without them the check quietly stops applying the moment a project has
	// namespaces — which is also every project a lowering has produced into, since a
	// lowered resource lives in a scope of its own.
	const meta = "id: QmPChzsSKcAiSFqNqrH7LM4t9GhqfHmi3ihn7C9hNc8Vg1\nname: billing\n"
	found = auditDocs(t, nil, map[string]string{
		"/namespace/billing/meta.yaml":           meta,
		"/namespace/billing/database/books.yaml": kv,
		"/function/f.yaml":                       fn + "database:\n    books:\n        namespace: /billing\n        ops: [search]\n",
	})
	has(t, found, schema.Broken, "asks to search database/books, which is a kv database")

	found = auditDocs(t, nil, map[string]string{
		"/namespace/billing/meta.yaml":       meta,
		"/namespace/billing/function/g.yaml": fn + "database:\n    tokens:\n        ops: [exec]\n",
		"/database/tokens.yaml":              kv,
	})
	has(t, found, schema.Broken, "asks to exec database/tokens, which is a kv database")

	// A verb NO type declares is refused a level earlier, at load, by the kind's own
	// enum — so the two halves cover different mistakes rather than overlapping.
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/database/tokens.yaml", []byte(kv), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/function/f.yaml",
		[]byte(fn+"database:\n    tokens:\n        ops: [fetch]\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, `"fetch" is not something a database does`)
}

// AND IT COVERS WHAT A LOWERING WROTE. An agent's memory is a vector database nobody
// authored, reached by a function nobody authored, with an entry nobody authored — and
// all three are in the compiled object, so the same pass checks them. A lowering that
// asked for the wrong verb would be a fault in something an author cannot edit, which
// is the case worth having a test for.
func TestALoweredDatabaseEntryIsCheckedToo(t *testing.T) {
	docs := map[string]string{
		"/secret/k.yaml":   "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy\n",
		"/model/opus.yaml": "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zc\nidentifier: x\nformat: openai\ninference:\n    remote:\n        endpoint: https://api.example.com\n        credentials:\n            name: k\n",
		"/agent/a.yaml": "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Za\n" +
			"model:\n    name: opus\nlimits:\n    steps: 4\n" +
			"memory:\n    embedder:\n        name: opus\n    dimensions: 8\n    size: 1MB\n",
	}

	// There IS a lowered entry to check: without this the assertion below passes for
	// the wrong reason, on a project where nothing produced a database at all.
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
	for path, body := range docs {
		assert.NilError(t, afero.WriteFile(fs, path, []byte(body), 0o644))
	}
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	memory := resource(t, obj.Flat(), "database", "a_memory")
	assert.Equal(t, memory["type"], "vector", "the lowering picked the block, not a `type:` line")
	assert.Equal(t, nested(memory, "vector", "dimensions"), 8)
	assert.Equal(t, nested(memory, "vector", "size"), int64(1000000))
	loop := resource(t, obj.Flat(), "function", "a_infer")
	assert.DeepEqual(t, loop["database"].(map[string]any)["a_memory"].(map[string]any)["ops"], []string{"search"})

	hasNo(t, schema.Audit(obj.Flat(), nil), "which is a vector database")
}

// THE CEILING, WHICH ONLY THE AUDIT CAN HOLD. A scope's allowance and a resource's
// placement are in different documents, so neither the schema nor load time sees both
// ends — and a widening that compiled silently would mean a child running where its
// parent said nothing may.
func TestAuditRegions(t *testing.T) {
	const proj = "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nregions: [eu, us]\n"
	const shopID = "id: QmQPeNsJPyVWPFDVHb77w8G42Fvo15z4bmmjRoBHfW1YZv\n"
	cache := func(region string) string {
		return "id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nsize: 1MB\nconsensus: crdt\n" + region
	}

	// A scope may narrow what it inherits, and only narrow it.
	found := auditDocs(t, nil, map[string]string{
		"/project.yaml":             proj,
		"/namespace/shop/meta.yaml": shopID + "regions: [eu, ap]\n",
	})
	has(t, found, schema.Broken, `allows region "ap", which the scope above it does not`)

	found = auditDocs(t, nil, map[string]string{
		"/project.yaml":             proj,
		"/namespace/shop/meta.yaml": shopID + "regions: [eu]\n",
	})
	hasNo(t, found, "which the scope above it does not")

	// A placement is bounded by the WHOLE chain, not only by the scope it sits in:
	// `us` is in the project's list and out of shop's, and shop is what applies.
	found = auditDocs(t, nil, map[string]string{
		"/project.yaml":                       proj,
		"/namespace/shop/meta.yaml":           shopID + "regions: [eu]\n",
		"/namespace/shop/cache/sessions.yaml": cache("regions:\n    pinned: [us]\n"),
	})
	has(t, found, schema.Broken, `is placed in region "us", which its scope does not allow`)

	found = auditDocs(t, nil, map[string]string{
		"/project.yaml":                       proj,
		"/namespace/shop/meta.yaml":           shopID + "regions: [eu]\n",
		"/namespace/shop/cache/sessions.yaml": cache("regions:\n    pinned: [eu]\n"),
	})
	hasNo(t, found, "which its scope does not allow")

	// A FAILOVER'S TWO REGIONS ARE CHECKED SEPARATELY, because either half can escape.
	found = auditDocs(t, nil, map[string]string{
		"/project.yaml": proj,
		"/cache/c.yaml": cache("regions:\n    failover:\n        main: eu\n        standby: ap\n"),
	})
	has(t, found, schema.Broken, `is placed in region "ap", which its scope does not allow`)

	// …and failing over to the region you are already in is a plan that fails to what
	// is already gone. The schema says there are two; only this can say they differ.
	found = auditDocs(t, nil, map[string]string{
		"/project.yaml": proj,
		"/cache/c.yaml": cache("regions:\n    failover:\n        main: eu\n        standby: eu\n"),
	})
	has(t, found, schema.Broken, "fails over from eu to itself")

	// NO CEILING ANYWHERE IS THE ZERO-CONFIG PROJECT: nothing was declared, so there is
	// nothing to check a label against and the audit says nothing. This is also where
	// the typo-catcher stops — `zz` is only wrong against a list somebody wrote.
	found = auditDocs(t, nil, map[string]string{
		"/project.yaml": "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n",
		"/cache/c.yaml": cache("regions:\n    pinned: [zz]\n"),
	})
	hasNo(t, found, "which its scope does not allow")

	// The chain is walked to whichever level last declared one, so a namespace that
	// declares nothing inherits rather than resetting to the cloud's whole world.
	found = auditDocs(t, nil, map[string]string{
		"/project.yaml":                                    proj,
		"/namespace/shop/meta.yaml":                        shopID + "regions: [eu]\n",
		"/namespace/shop/namespace/eu/meta.yaml":           "id: QmVs4sQjPFqJZ7Ug5MPpFxPTNiZ1CT8xY4XPKUbnDNbNAY\n",
		"/namespace/shop/namespace/eu/cache/sessions.yaml": cache("regions:\n    pinned: [us]\n"),
	})
	has(t, found, schema.Broken, `is placed in region "us", which its scope does not allow`)
}

// auditDocsInEnv is auditDocs for ONE environment, which is the only way a
// per-environment placement is visible to the audit: it reads a COMPILED resource, and
// a compile is for one environment at a time.
func auditDocsInEnv(t *testing.T, env string, docs map[string]string) []schema.Finding {
	t.Helper()
	fs := afero.NewMemMapFs()
	for path, body := range docs {
		assert.NilError(t, afero.WriteFile(fs, path, []byte(body), 0o644))
	}
	opts := []schema.Option{schema.WithVirtual(fs, "/"), schema.WithBranch("main"),
		schema.WithEnvironments("production", "preview")}
	if env != "" {
		opts = append(opts, schema.WithEnvironment(env))
	}
	c, err := schema.New(opts...)
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	return schema.Audit(obj.Flat(), nil, schema.AuditIn(env))
}

// TestTheSuffixIsNotAWayAroundTheCeiling.
//
// PER-ENVIRONMENT PLACEMENT IS A SCOPE'S `regions`, a plain list, so it takes a
// suffix — and a suffixed list is a placement like any other, bounded by the
// scope above it. Otherwise `regions_preview` would be the spelling that walks
// regulated data out of the region a project was written to keep it in.
func TestAPerEnvironmentPlacementIsBoundedToo(t *testing.T) {
	const shopID = "id: QmQPeNsJPyVWPFDVHb77w8G42Fvo15z4bmmjRoBHfW1YZv\n"
	docs := map[string]string{
		"/project.yaml": "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nregions: [eu]\n" +
			"environments:\n    production:\n        branches: [main]\n    preview:\n        branches: [\"*\"]\n",
		"/namespace/shop/meta.yaml": shopID + "regions: [eu]\nregions_preview: [us]\n",
	}

	// Production reads `regions`, which the project allows.
	hasNo(t, auditDocsInEnv(t, "production", docs), "which the scope above it does not")

	// Preview reads `regions_preview`, which it does not.
	has(t, auditDocsInEnv(t, "preview", docs), schema.Broken,
		`allows region "us", which the scope above it does not`)
}

// TestAnythingWithAFootprintIsPlaced.
//
// WHERE SOMETHING RUNS IS THE REGIONS' ANSWER, not an authored line: the default
// places everything in one region, so a project that writes no `regions` anywhere is
// placed, and only a Regions that places a resource nowhere makes it Broken.
func TestAnythingWithAFootprintIsPlaced(t *testing.T) {
	const bare = "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"
	cache := func(region string) string {
		return "id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nsize: 1MB\nconsensus: crdt\n" + region
	}
	docs := map[string]string{"/project.yaml": bare, "/cache/c.yaml": cache("")}

	// Neither says where it goes, and the default places it.
	hasNo(t, auditDocs(t, nil, docs), "is not placed anywhere")

	// A Regions that serves nothing named places it nowhere.
	has(t, auditWith(t, onlyNamed{}, docs), schema.Broken, "is not placed anywhere")

	// …and the same Regions places what says where it goes.
	hasNo(t, auditWith(t, onlyNamed{}, map[string]string{
		"/project.yaml": bare,
		"/cache/c.yaml": cache("regions:\n    pinned: [eu]\n"),
	}), "is not placed anywhere")

	// A KIND WITH NO FOOTPRINT IS NEVER ASKED, and the set is read off the
	// schema rather than listed here — a `component` is built, not placed, and
	// a kind that gains a footprint tomorrow is covered the moment it declares
	// Placed().
	require.NotContains(t, schema.PlacedKinds(), "secret")
	hasNo(t, auditWith(t, onlyNamed{}, map[string]string{
		"/project.yaml":  bare,
		"/secret/s.yaml": "id: QmVs4sQjPFqJZ7Ug5MPpFxPTNiZ1CT8xY4XPKUbnDNbNAY\nsource: value\nrotation: 720h\n",
	}), "is not placed anywhere")
}

// TestAnEnvironmentNarrowsTheProjectAndNeverWidensIt.
//
// AN ENVIRONMENT IS ON MANY REGIONS — that is why the field is a list — and the
// regions it keeps its state in are still bounded by what the project allows.
// Nothing checked this, so an environment could quietly widen the one statement
// a project makes about where its data may ever be, and the runtime honoured it.
func TestAnEnvironmentCannotWidenTheProject(t *testing.T) {
	proj := func(envRegions string) string {
		return "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nregions: [eu, us]\n" +
			"environments:\n    production:\n        branches: [main]\n" + envRegions
	}

	has(t, auditDocs(t, nil, map[string]string{
		"/project.yaml": proj("        regions: [ap]\n"),
	}), schema.Broken, `keeps state in region "ap", which the project does not allow`)

	hasNo(t, auditDocs(t, nil, map[string]string{
		"/project.yaml": proj("        regions: [eu, us]\n"),
	}), "which the project does not allow")
}

// TestAResourceIsPlacedWhereItsDeploymentKeepsState.
//
// THE AUDIT HAS TO BE TOLD WHICH DEPLOYMENT IT IS READING. A compile is for one
// environment and the object does not say which — `name_preview` and `name` are
// indistinguishable once resolved — so without the name there is no way to know
// whose region list to check against.
func TestAResourceIsPlacedWhereItsDeploymentKeepsState(t *testing.T) {
	docs := map[string]string{
		"/project.yaml": "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nregions: [eu, us]\n" +
			"environments:\n    production:\n        branches: [main]\n        regions: [eu]\n" +
			"    preview:\n        branches: [\"*\"]\n        regions: [us]\n",
		"/cache/c.yaml": "id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nsize: 1MB\nconsensus: crdt\n" +
			"regions:\n    pinned: [eu]\n",
	}

	// production keeps state in eu and the cache is pinned to eu.
	hasNo(t, auditDocsInEnv(t, "production", docs), "does not keep state in")

	// preview keeps state in us, and the same pin is now outside it — a resource
	// pins itself in EVERY environment, which is what a residency line does.
	has(t, auditDocsInEnv(t, "preview", docs), schema.Broken,
		`is placed in region "eu", which preview does not keep state in`)

	// TOLD NOTHING, IT SAYS NOTHING rather than guessing a deployment.
	hasNo(t, auditDocsInEnv(t, "", docs), "does not keep state in")
}
