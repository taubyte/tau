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
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
	for path, body := range standingPackages {
		assert.NilError(t, afero.WriteFile(fs, path, []byte(body), 0o644))
	}
	for path, body := range docs {
		assert.NilError(t, afero.WriteFile(fs, path, []byte(body), 0o644))
	}
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	return schema.Audit(obj.Flat(), r)
}

// standingPackages are one package per tier, so a probe workload names what it runs
// without each test declaring it.
var standingPackages = map[string]string{
	"/package/img.yaml":  "id: " + cidFor("img") + "\ntype: oci\nsource:\n    image: img\n",
	"/package/wasm.yaml": "id: " + cidFor("wasm") + "\ntype: wasm\n",
	"/package/warp.yaml": "id: " + cidFor("warp") + "\ntype: warp\n",
	"/package/site.yaml": "id: " + cidFor("site") + "\ntype: static\n",
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

const appBody = "id: QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY\npackage:\n    name: img\nexecution:\n    memory: 1MB\n"

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

func TestAuditDelivery(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{
		"/sync/work.yaml":     "id: QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o\nqueue:\n    consensus: raft\n    visibility_timeout: 30s\n",
		"/application/w.yaml": appBody + "sync:\n    work:\n        ops: [receive]\n",
	})
	has(t, found, schema.Broken, "declares no deliver port")

	found = auditDocs(t, nil, map[string]string{
		"/sync/work.yaml":  "id: QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o\nqueue:\n    consensus: raft\n    visibility_timeout: 30s\n",
		"/function/f.yaml": "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\npackage:\n    name: wasm\nexecution: {call: c, timeout: 1s, memory: 1MB}\nsync:\n    work:\n        ops: [receive]\n",
	})
	hasNo(t, found, "deliver port")
}

func TestAuditVariables(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{
		"/application/a.yaml": appBody + "variables:\n    X:\n        value: x\n",
	})
	hasNo(t, found, "variable")
}

// refusing says no to every external question, so the edge cases run with no
// network.
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

func TestAuditOfTheExample(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	// The example names more regions than one cloud has, to exercise the syntax; it
	// must not be broken.
	flat := obj.Flat()
	for _, f := range schema.Audit(flat, nil, schema.WithRegions(schema.Preview(schema.RegionsNamed(flat)...))) {
		assert.Assert(t, f.Severity != schema.Broken, "the example must compile to something that works: %v", f)
	}
}

func TestAuditMountedWebsite(t *testing.T) {
	site := "id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv\npackage:\n    name: site\n"
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

func TestWebsiteServingMode(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/package/site.yaml", []byte(standingPackages["/package/site.yaml"]), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/website/app.yaml", []byte(
		"id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv\npackage:\n    name: site\nserve:\n    mode: spa\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	site := only(t, resources(t, obj.Flat(), "website"))
	assert.Equal(t, nested(site, "serve", "mode"), "spa")
	assert.Equal(t, nested(site, "serve", "index"), "index.html", "the default a directory request returns")
}

func TestAuditHostWorlds(t *testing.T) {
	fn := func(world string) string {
		body := "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\npackage:\n    name: wasm\nexecution: {call: c, timeout: 1s, memory: 1MB}\n"
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

func TestAuditHostWorldsReachesEveryUse(t *testing.T) {
	fn := func(world string) string {
		b := "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\npackage:\n    name: wasm\nexecution:\n    call: c\n    timeout: 1s\n    memory: 1MB\n"
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

func TestAuditHandlerPorts(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{
		"/website/w.yaml": "id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv\npackage:\n    name: site\n",
		"/gateway/g.yaml": "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nroute:\n    path: /\n    handler:\n        website:\n            name: w\n            port: http\n",
	})
	has(t, found, schema.Broken, "names port \"http\" on a website")
}

func TestAuditVariableAsFile(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{
		"/application/a.yaml": appBody + "variables:\n    CERT:\n        value: pem\n        as: file\n",
	})
	has(t, found, schema.Broken, "names no path")

	found = auditDocs(t, nil, map[string]string{
		"/application/a.yaml": appBody + "variables:\n    CERT:\n        value: pem\n        as: file\n        path: /etc/tls/cert.pem\n",
	})
	hasNo(t, found, "names no path")
}

func TestAuditGhostFunctions(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{
		"/application/a.yaml": appBody + "ports:\n    p:\n        number: 80\n",
		"/gateway/g.yaml":     "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nroute:\n    path: /\n    handler:\n        application:\n            name: a\n            port: p\ntransformers:\n    - name: ghost\n",
		"/cache/c.yaml":       "id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nsize: 1MB\nconsensus: crdt\nauthorizer:\n    name: alsoghost\n",
	})
	has(t, found, schema.Broken, "is named as a gateway-transformer by gateway/g transformers, and does not exist")
	has(t, found, schema.Broken, "is named as a authorizer by cache/c authorizer, and does not exist")
}

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

func TestAuditReportsALoweringFaultAgainstItsSource(t *testing.T) {
	const wf = "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nstart: reserve\nsteps:\n    - name: reserve\n      call:\n          function: %s\n      attempts: 2\n"

	found := auditDocs(t, nil, map[string]string{
		"/function/reserve.yaml":  "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\npackage:\n    name: wasm\nexecution: {call: c, timeout: 1s, memory: 1MB}\n",
		"/workflow/checkout.yaml": strings.Replace(wf, "%s", "reserve", 1),
	})
	hasNo(t, found, "does not exist")

	found = auditDocs(t, nil, map[string]string{
		"/function/reserve.yaml":  "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\npackage:\n    name: wasm\nexecution: {call: c, timeout: 1s, memory: 1MB}\n",
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

type recorder struct{ pulls map[string]string }

func (r *recorder) ZoneDelegated(string) error { return nil }
func (r *recorder) ImagePullable(image, pullSecret string) error {
	if r.pulls == nil {
		r.pulls = map[string]string{}
	}
	r.pulls[image] = pullSecret
	return nil
}

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

func TestAuditAuthoredBuiltin(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/package/loop.yaml", []byte(
		"id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n"+
			"type: wasm\n"+
			"source:\n    builtin: does_not_exist\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err, "it compiles - which is the point")

	var found *schema.Finding
	for _, f := range schema.Audit(obj.Flat(), nil) {
		if f.Kind == "package" && f.Name == "loop" {
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

func TestATargetWithNoNameIsRefused(t *testing.T) {
	type testCase struct{ name, path, doc, contains string }
	for _, tc := range []testCase{
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

func TestDatabaseOpsAreItsTypesNotTheUnion(t *testing.T) {
	const fn = "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\npackage:\n    name: wasm\nexecution: {call: c, timeout: 1s, memory: 1MB}\n"
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

	// A target in another scope resolves like every other reference: a written
	// namespace, and the outward walk of a bare name.
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
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/database/tokens.yaml", []byte(kv), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/function/f.yaml",
		[]byte(fn+"database:\n    tokens:\n        ops: [fetch]\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, `"fetch" is not something a database does`)
}

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
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
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

func TestAuditRegions(t *testing.T) {
	const proj = "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\nregions: [region-1, region-2]\n"
	const shopID = "id: QmQPeNsJPyVWPFDVHb77w8G42Fvo15z4bmmjRoBHfW1YZv\n"
	cache := func(region string) string {
		return "id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nsize: 1MB\nconsensus: crdt\n" + region
	}

	// A scope may narrow what it inherits, and only narrow it.
	found := auditDocs(t, nil, map[string]string{
		"/project.yaml":             proj,
		"/namespace/shop/meta.yaml": shopID + "regions: [region-1, region-3]\n",
	})
	has(t, found, schema.Broken, `allows region "region-3", which the scope above it does not`)

	found = auditDocs(t, nil, map[string]string{
		"/project.yaml":             proj,
		"/namespace/shop/meta.yaml": shopID + "regions: [region-1]\n",
	})
	hasNo(t, found, "which the scope above it does not")

	// A placement is bounded by the WHOLE chain, not only by the scope it sits in:
	// `region-2` is in the project's list and out of shop's, and shop is what applies.
	found = auditDocs(t, nil, map[string]string{
		"/project.yaml":                       proj,
		"/namespace/shop/meta.yaml":           shopID + "regions: [region-1]\n",
		"/namespace/shop/cache/sessions.yaml": cache("regions:\n    pinned: [region-2]\n"),
	})
	has(t, found, schema.Broken, `is placed in region "region-2", which its scope does not allow`)

	found = auditDocs(t, nil, map[string]string{
		"/project.yaml":                       proj,
		"/namespace/shop/meta.yaml":           shopID + "regions: [region-1]\n",
		"/namespace/shop/cache/sessions.yaml": cache("regions:\n    pinned: [region-1]\n"),
	})
	hasNo(t, found, "which its scope does not allow")

	// A FAILOVER'S TWO REGIONS ARE CHECKED SEPARATELY, because either half can escape.
	found = auditDocs(t, nil, map[string]string{
		"/project.yaml": proj,
		"/cache/c.yaml": cache("regions:\n    failover:\n        main: region-1\n        standby: region-3\n"),
	})
	has(t, found, schema.Broken, `is placed in region "region-3", which its scope does not allow`)

	// …and failing over to the region you are already in is a plan that fails to what
	// is already gone. The schema says there are two; only this can say they differ.
	found = auditDocs(t, nil, map[string]string{
		"/project.yaml": proj,
		"/cache/c.yaml": cache("regions:\n    failover:\n        main: region-1\n        standby: region-1\n"),
	})
	has(t, found, schema.Broken, "fails over from region-1 to itself")

	// NO CEILING ANYWHERE IS THE ZERO-CONFIG PROJECT: nothing was declared, so there is
	// nothing to check a label against and the audit says nothing. This is also where
	// the typo-catcher stops — `zz` is only wrong against a list somebody wrote.
	found = auditDocs(t, nil, map[string]string{
		"/project.yaml": "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n",
		"/cache/c.yaml": cache("regions:\n    pinned: [zz]\n"),
	})
	hasNo(t, found, "which its scope does not allow")

	// The chain is walked to whichever level last declared one, so a namespace that
	// declares nothing inherits rather than resetting to the cloud's whole world.
	found = auditDocs(t, nil, map[string]string{
		"/project.yaml":                                       proj,
		"/namespace/shop/meta.yaml":                           shopID + "regions: [region-1]\n",
		"/namespace/shop/namespace/local/meta.yaml":           "id: QmQt1CwQTMMBWhPDHmNUzd81oCd21PuxkrxJzSh2SnoUQM\n",
		"/namespace/shop/namespace/local/cache/sessions.yaml": cache("regions:\n    pinned: [region-2]\n"),
	})
	has(t, found, schema.Broken, `is placed in region "region-2", which its scope does not allow`)
}

// auditDocsInEnv audits a compile for one environment, the only way a
// per-environment placement is visible.
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

func TestAPerEnvironmentPlacementIsBoundedToo(t *testing.T) {
	const shopID = "id: QmQPeNsJPyVWPFDVHb77w8G42Fvo15z4bmmjRoBHfW1YZv\n"
	docs := map[string]string{
		"/project.yaml": "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nregions: [region-1]\n" +
			"environments:\n    production:\n        branches: [main]\n    preview:\n        branches: [\"*\"]\n",
		"/namespace/shop/meta.yaml": shopID + "regions: [region-1]\nregions_preview: [region-2]\n",
	}

	// Production reads `regions`, which the project allows.
	hasNo(t, auditDocsInEnv(t, "production", docs), "which the scope above it does not")

	// Preview reads `regions_preview`, which it does not.
	has(t, auditDocsInEnv(t, "preview", docs), schema.Broken,
		`allows region "region-2", which the scope above it does not`)
}

func TestAnythingWithAFootprintIsPlaced(t *testing.T) {
	const bare = "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"
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
		"/cache/c.yaml": cache("regions:\n    pinned: [region-1]\n"),
	}), "is not placed anywhere")

	// A KIND WITH NO FOOTPRINT IS NOT ASKED UNTIL IT SAYS WHERE IT GOES, and the
	// set is read off the schema rather than listed here.
	require.NotContains(t, schema.FootprintKinds(), "secret")
	secret := "id: QmVs4sQjPFqJZ7Ug5MPpFxPTNiZ1CT8xY4XPKUbnDNbNAY\nsource: value\nrotation: 720h\n"
	hasNo(t, auditWith(t, onlyNamed{}, map[string]string{
		"/project.yaml":  bare,
		"/secret/s.yaml": secret,
	}), "is not placed anywhere")

	// …and once it does, it is placed like anything else.
	has(t, auditWith(t, onlyNamed{}, map[string]string{
		"/project.yaml":  bare,
		"/secret/s.yaml": secret + "regions:\n    cdn: []\n",
	}), schema.Broken, "is not placed anywhere")
	hasNo(t, auditWith(t, onlyNamed{}, map[string]string{
		"/project.yaml":  bare,
		"/secret/s.yaml": secret + "regions:\n    pinned: [region-1]\n",
	}), "is not placed anywhere")
}

func TestAnEnvironmentCannotWidenTheProject(t *testing.T) {
	proj := func(envRegions string) string {
		return "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nregions: [region-1, region-2]\n" +
			"environments:\n    production:\n        branches: [main]\n" + envRegions
	}

	has(t, auditDocs(t, nil, map[string]string{
		"/project.yaml": proj("        regions: [region-3]\n"),
	}), schema.Broken, `keeps state in region "region-3", which the project does not allow`)

	hasNo(t, auditDocs(t, nil, map[string]string{
		"/project.yaml": proj("        regions: [region-1, region-2]\n"),
	}), "which the project does not allow")
}

func TestAResourceIsPlacedWhereItsDeploymentKeepsState(t *testing.T) {
	docs := map[string]string{
		"/project.yaml": "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nregions: [region-1, region-2]\n" +
			"environments:\n    production:\n        branches: [main]\n        regions: [region-1]\n" +
			"    preview:\n        branches: [\"*\"]\n        regions: [region-2]\n",
		"/cache/c.yaml": "id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nsize: 1MB\nconsensus: crdt\n" +
			"regions:\n    pinned: [region-1]\n",
	}

	// production keeps state in region-1 and the cache is pinned to region-1.
	hasNo(t, auditDocsInEnv(t, "production", docs), "does not keep state in")

	// preview keeps state in region-2, and the same pin is now outside it — a resource
	// pins itself in EVERY environment, which is what a residency line does.
	has(t, auditDocsInEnv(t, "preview", docs), schema.Broken,
		`is placed in region "region-1", which preview does not keep state in`)

	// TOLD NOTHING, IT SAYS NOTHING rather than guessing a deployment.
	hasNo(t, auditDocsInEnv(t, "", docs), "does not keep state in")
}
