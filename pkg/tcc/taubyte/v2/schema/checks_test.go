package schema_test

import (
	"context"
	"errors"
	"strings"
	"testing"

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

const appBody = "id: QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY\nimage: img\nexecution:\n    memory: 1MB\n"

// An http workload nothing fronts answers nobody. A consumer in the same position is
// correct, and the difference is the whole reason severity is not a count.
func TestAuditReachability(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{"/application/lonely.yaml": appBody})
	has(t, found, schema.Broken, "nothing reaches it")

	found = auditDocs(t, nil, map[string]string{
		"/queue/work.yaml":    "id: QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o\nvisibility-timeout: 30s\n",
		"/application/w.yaml": appBody + "ports:\n    work:\n        number: 8080\nqueue:\n    work:\n        ops: [receive]\n        deliver:\n            port: work\n            path: /w\n",
	})
	has(t, found, schema.Note, "nothing fronts it, and nothing needs to")
	hasNo(t, found, "nothing reaches it")
}

// A container told to consume has to say where each item goes; a WASM workload is
// dispatched and needs no port at all.
func TestAuditDelivery(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{
		"/queue/work.yaml":    "id: QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o\nvisibility-timeout: 30s\n",
		"/application/w.yaml": appBody + "queue:\n    work:\n        ops: [receive]\n",
	})
	has(t, found, schema.Broken, "declares no deliver port")

	found = auditDocs(t, nil, map[string]string{
		"/queue/work.yaml": "id: QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o\nvisibility-timeout: 30s\n",
		"/function/f.yaml": "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\nsource: \".\"\nexecution: {call: c, timeout: 1s, memory: 1MB}\nqueue:\n    work:\n        ops: [receive]\n",
	})
	hasNo(t, found, "deliver port")
}

// The mutual exclusion the DSL cannot state.
func TestAuditVariables(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{
		"/secret/k.yaml":      "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy\n",
		"/application/a.yaml": appBody + "variables:\n    BOTH:\n        value: x\n        secret: secret/k\n    NEITHER: {}\n",
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
	site := "id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv\nsource:\n    branch: main\n    github:\n        id: \"1\"\n        fullname: acme/docs\n"
	gw := func(extra string) string {
		return "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nroute:\n    path: /docs\n" + extra + "    handler:\n        website:\n            name: docs\n"
	}

	found := auditDocs(t, nil, map[string]string{"/website/docs.yaml": site, "/gateway/g.yaml": gw("")})
	has(t, found, schema.Check, "does not strip the prefix")

	found = auditDocs(t, nil, map[string]string{"/website/docs.yaml": site, "/gateway/g.yaml": gw("    strip-prefix: true\n")})
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
		"id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv\nsource:\n    branch: main\n    github:\n        id: \"1\"\n        fullname: acme/app\nserve:\n    mode: spa\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	site := only(t, resources(t, obj.Flat(), "website"))
	assert.Equal(t, site["serve/mode"], "spa")
	assert.Equal(t, site["serve/index"], "index.html", "the default a directory request returns")
}

// Every USE of a function is a place tcc can see, and the function says which world it
// was built against — so "compiles clean, fails on live traffic" becomes an error at
// compile.
func TestAuditHostWorlds(t *testing.T) {
	fn := func(world string) string {
		body := "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\nsource: \".\"\nexecution: {call: c, timeout: 1s, memory: 1MB}\n"
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
		"/proxy/p.yaml":       "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nprotocol: tcp\nhost:\n    generated:\n        name: db\ntransformers:\n    - name: t\nforward:\n    application:\n        name: a\n        port: p\n",
	})
	has(t, found, schema.Broken, "is used as a proxy-transformer")
}

// Only a container listens on ports. A route naming one on a website points at
// something that does not exist — the field is on the handler block for every kind
// because which kinds have ports is not something the schema can condition on.
func TestAuditHandlerPorts(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{
		"/website/w.yaml": "id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv\nsource:\n    branch: main\n    github:\n        id: \"1\"\n        fullname: acme/w\n",
		"/gateway/g.yaml": "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nroute:\n    path: /\n    handler:\n        website:\n            name: w\n            port: http\n",
	})
	has(t, found, schema.Broken, "names port \"http\" on a website")
}

// A file has to be mounted somewhere the workload can open it.
func TestAuditVariableAsFile(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{
		"/secret/k.yaml":      "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy\n",
		"/application/a.yaml": appBody + "variables:\n    CERT:\n        secret: secret/k\n        as: file\n",
	})
	has(t, found, schema.Broken, "names no path")

	found = auditDocs(t, nil, map[string]string{
		"/secret/k.yaml":      "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy\n",
		"/application/a.yaml": appBody + "variables:\n    CERT:\n        secret: secret/k\n        as: file\n        path: /etc/tls/cert.pem\n",
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
		"/cache/c.yaml":       "id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nmatch: c\nsize: 1MB\nauthorizer:\n    name: alsoghost\n",
	})
	has(t, found, schema.Broken, "is named as a gateway-transformer by gateway/g transformers, and does not exist")
	has(t, found, schema.Broken, "is named as a authorizer by cache/c authorizer, and does not exist")
}

// A volume is leased to ONE writer, and nothing in the declaration says so: access is
// declared by the accessor, so two workloads can each ask to write and both compile.
func TestAuditVolumeWriters(t *testing.T) {
	const vol = "id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nquota: 1GB\n"
	mount := func(op string) string {
		return appBody + "volume:\n    data:\n        ops: [" + op + "]\n        path: /data\n"
	}

	found := auditDocs(t, nil, map[string]string{
		"/volume/data.yaml":   vol,
		"/application/a.yaml": mount("mount-rw"),
		"/application/b.yaml": strings.Replace(mount("mount-rw"), "QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY", "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG", 1),
	})
	has(t, found, schema.Broken, "is mounted for writing by")

	// one writer and one reader is the shape that works
	found = auditDocs(t, nil, map[string]string{
		"/volume/data.yaml":   vol,
		"/application/a.yaml": mount("mount-rw"),
		"/application/b.yaml": strings.Replace(mount("mount-ro"), "QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY", "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG", 1),
	})
	hasNo(t, found, "is mounted for writing by")

	// and the same contradiction inside one resource: ten instances, one lease
	found = auditDocs(t, nil, map[string]string{
		"/volume/data.yaml":   vol,
		"/application/a.yaml": mount("mount-rw") + "limits:\n    max-instances: 10\n",
	})
	has(t, found, schema.Broken, "may run 10 instances")

	found = auditDocs(t, nil, map[string]string{
		"/volume/data.yaml":   vol,
		"/application/a.yaml": mount("mount-rw") + "limits:\n    max-instances: 1\n",
	})
	hasNo(t, found, "instances")
}
