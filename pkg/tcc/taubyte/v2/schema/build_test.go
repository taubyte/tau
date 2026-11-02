package schema_test

import (
	"context"
	"slices"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// buildDoc writes one document over the basic fixture and compiles the lot, so a probe
// is a real project rather than a resource on its own — references resolve, the audit
// has something to walk, and a branch that only works in isolation fails here.
func buildDoc(t *testing.T, path, body string) (map[string]any, error) {
	t.Helper()
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, path, []byte(body), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	if err != nil {
		return nil, err
	}
	return obj.Flat(), nil
}

const probePkg = "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLi\ntype: wasm\n"

const probeSteps = "output: out.wasm\nbuild:\n    steps:\n        - name: b\n          image: g\n          commands: [make]\n"

func TestABuildIsAbsentUntilItIsWritten(t *testing.T) {
	flat, err := buildDoc(t, "/package/probe.yaml", probePkg)
	assert.NilError(t, err)
	pkg := namedResource(t, resources(t, flat, "package"), "probe")
	_, present := pkg["build"]
	assert.Assert(t, !present, "an unwritten build left something in the object: %v", pkg)
}

func TestEveryWayOfBuildingCompiles(t *testing.T) {
	flat := compileBasic(t)
	pkgs := resources(t, flat, "package")

	// steps: a toolchain, its commands in order, a cache, and where the artifact landed
	site := namedResource(t, pkgs, "shop_site")
	assert.Equal(t, nested(site, "build", "kind"), "steps")
	step := nested(site, "build", "steps").([]any)[0].(map[string]any)
	assert.Equal(t, step["name"], "site")
	assert.Equal(t, step["image"], "node:22")
	assert.DeepEqual(t, step["commands"], []string{"export NPM_TOKEN=$(/.taubyte/secret stripe)", "pnpm install --frozen-lockfile", "cp .inputs/pay/paylib.wasm public/", "pnpm build"})
	paylib := ""
	for id, p := range pkgs {
		if p.(map[string]any)["name"] == "paylib" {
			paylib = id
		}
	}
	assert.Equal(t, nested(site, "build", "inputs", "pay", "name"), paylib, "an input resolves to the package it names")
	assert.DeepEqual(t, step["cache"], []string{"~/.pnpm-store"})
	assert.Equal(t, site["output"], "dist")
	assert.Equal(t, site["type"], "static")

	// dockerfile: an oci package built HERE rather than pulled
	pool := namedResource(t, pkgs, "worker_pool_code")
	assert.Equal(t, nested(pool, "build", "kind"), "dockerfile")
	assert.Equal(t, nested(pool, "build", "dockerfile", "path"), "Dockerfile", "the path defaults")
	assert.Equal(t, nested(pool, "build", "dockerfile", "target"), "runtime")
	assert.Equal(t, pool["source"], "services/pool")

	// and the other answer to the same question: somebody else built it
	api := namedResource(t, pkgs, "api_image")
	assert.Equal(t, nested(api, "source", "kind"), "image")
	assert.Equal(t, nested(api, "source", "image"), "ghcr.io/acme/api:1.4")
	_, built := api["build"]
	assert.Assert(t, !built, "a pulled image has no build")

	// a steps build has no dockerfile defaults in it
	_, dockerfile := nestedOK(site, "build", "dockerfile")
	assert.Assert(t, !dockerfile, "a steps build carries a dockerfile path: %v", site["build"])
}

func TestABuildMustSayHow(t *testing.T) {
	_, err := buildDoc(t, "/package/probe.yaml", probePkg+"build: {}\n")
	assert.ErrorContains(t, err, "empty key")
}

func TestAWrongPairIsRefusedByShape(t *testing.T) {
	type testCase struct{ name, body, want string }
	for _, tc := range []testCase{
		{"a step has no install of its own", probePkg + "build:\n    steps:\n        - name: b\n          image: g\n          commands: [make]\n          install: npm ci\n",
			"unknown key 'install'"},
		{"two ways at once", probePkg + "build:\n    dockerfile:\n        path: Dockerfile\n    steps:\n        - name: b\n          image: g\n          commands: [make]\n",
			"only one may be set"},
		{"a step with nothing to run it in", probePkg + "build:\n    steps:\n        - name: b\n          commands: [make]\n",
			"image"},
		{"a step with nothing to run", probePkg + "build:\n    steps:\n        - name: b\n          image: g\n",
			"commands"},
		{"a step with no name", probePkg + "build:\n    steps:\n        - image: g\n          commands: [make]\n",
			"name"},
		{"the output is a path in the repository", probePkg + "output: \"../dist\"\n",
			"climbs out of the repository"},
		{"and the root has exactly one spelling", probePkg + "output: \"./\"\n",
			"must not end in /"},
		{"an empty output is not one of the three states", probePkg + "output: \"\"\n",
			"empty: write a path, or \".\" for the repository root"},
		{"a type nothing runs", "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLi\ntype: nodejs\n",
			`invalid value "nodejs"; valid values here: wasm, warp, oci, static`},
		{"a package says what it is", "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLi\n",
			"required attribute 'type'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildDoc(t, "/package/probe.yaml", tc.body)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestABuildMayPublishToTheRoot(t *testing.T) {
	const head = "id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVt\ntype: static\n"
	const steps = "build:\n    steps:\n        - name: b\n          image: node:22\n          commands: [npm ci, npm run build]\n"

	flat, err := buildDoc(t, "/package/publishroot.yaml", head+steps+"output: \".\"\n")
	assert.NilError(t, err)
	pkg := namedResource(t, resources(t, flat, "package"), "publishroot")
	assert.Equal(t, pkg["output"], ".",
		"the root has to be IN the object: a build that published in place is not a build that said nothing")

	flat, err = buildDoc(t, "/package/publishroot.yaml", head)
	assert.NilError(t, err)
	pkg = namedResource(t, resources(t, flat, "package"), "publishroot")
	_, present := pkg["output"]
	assert.Assert(t, !present, "an unwritten output left something in the object: %v", pkg)
}

func TestABuildBranchCannotBeEnvironmentSuffixed(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/package/probe.yaml",
		[]byte(probePkg+"build:\n    steps_preview:\n        - name: b\n          image: g\n          commands: [x]\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"),
		schema.WithEnvironments("production", "preview"), schema.WithEnvironment("preview"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "unknown key 'steps_preview'")
}

// What differs per deployment is a value the recipe reads and where the artifact
// lands; the recipe itself is one list, the same everywhere.
func TestABuildDiffersPerEnvironment(t *testing.T) {
	at := func(env string) map[string]any {
		t.Helper()
		c, err := schema.New(schema.WithLocal(envFixture), schema.WithBranch("main"),
			schema.WithProjectEnvironments(afero.NewOsFs(), envFixture), schema.WithEnvironment(env))
		assert.NilError(t, err)
		obj, _, err := c.Compile(context.Background())
		assert.NilError(t, err)
		return namedResource(t, resources(t, obj.Flat(), "package"), "checkout_code")
	}

	prod := at("production")
	assert.Equal(t, prod["output"], "target/release/checkout.wasm")
	assert.Equal(t, nested(prod, "variables", "PROFILE", "value"), "release")

	preview := at("preview")
	assert.Equal(t, preview["output"], "target/debug/checkout.wasm")
	assert.Equal(t, nested(preview, "variables", "PROFILE", "value"), "dev")

	assert.DeepEqual(t, nested(prod, "build", "steps"), nested(preview, "build", "steps"))

	// staging declares none of its own and reads the base
	assert.Equal(t, at("staging")["output"], "target/release/checkout.wasm")
}

func TestTheEngineSeamIsAValue(t *testing.T) {
	// every tier a kind runs on is a package type, so a workload can always name what
	// it runs
	for _, g := range schema.V2Resources {
		kind, _ := g.Match.(string)
		for _, e := range schema.EnginesOf(kind) {
			assert.Assert(t, slices.Contains(schema.PackageTypes(), e), "%s runs on %q, which no package is", kind, e)
		}
	}

	// a kind that runs nothing declares nothing, and the absence is the statement
	for _, kind := range []string{"package", "cache", "database", "cron", "zone", "model", "agent", "workflow"} {
		assert.Assert(t, len(schema.EnginesOf(kind)) == 0, "%s declares an engine", kind)
	}

	// and the tier is said once, on the package: no kind carries `runtime`, and only a
	// package carries `build`
	for _, g := range schema.V2Resources {
		kind, _ := g.Match.(string)
		if len(g.Children) == 0 {
			continue
		}
		for _, a := range g.Children[0].Attributes {
			assert.Assert(t, a.Name != "runtime", "%s still carries runtime", kind)
		}
		if kind == "package" {
			continue
		}
		for _, c := range g.Children[0].Children {
			assert.Assert(t, c.Match != "build", "%s still carries a build", kind)
		}
	}
}

func TestAuditPackages(t *testing.T) {
	audit := func(doc string) []schema.Finding {
		t.Helper()
		return auditDocs(t, nil, map[string]string{"/package/p.yaml": doc})
	}
	const id = "id: QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPZ\n"

	has(t, audit(id+"type: oci\nsource:\n    image: img\nbuild:\n    dockerfile:\n        path: Dockerfile\n"),
		schema.Broken, "takes its artifact from an image and also declares a build")
	has(t, audit(id+"type: wasm\nsource:\n    image: img\n"), schema.Broken, "names an image and is of type wasm")
	has(t, audit(id+"type: static\nsource: s\nbuild:\n    dockerfile:\n        path: Dockerfile\n"),
		schema.Broken, "is built from a Dockerfile and is of type static")
	has(t, audit(id+"type: oci\nsource: s\n"), schema.Broken, "is of type oci and names no image and no build")
	has(t, audit(id+"type: wasm\nbuild:\n    steps:\n        - name: b\n          image: g\n          commands: [make]\n"),
		schema.Broken, "is built with steps and names no output")
	has(t, audit(id+"type: oci\noutput: dist\nbuild:\n    dockerfile:\n        path: Dockerfile\n"),
		schema.Broken, "is built from a Dockerfile and names an output")

	// each alone is fine
	for _, ok := range []string{
		id + "type: oci\nsource:\n    image: img\n",
		id + "type: oci\nsource: api\nbuild:\n    dockerfile:\n        path: Dockerfile\n",
		id + "type: wasm\n" + probeSteps,
		id + "type: static\nsource: site\n",
	} {
		found := audit(ok)
		for _, f := range found {
			assert.Assert(t, f.Kind != "package" || f.Name != "p" || f.Severity != schema.Broken, "%q: %v", ok, f)
		}
	}
}

func TestAWorkloadRunsWhatItsKindCanRun(t *testing.T) {
	const fn = "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdJ\npackage:\n    name: p\nexecution:\n    timeout: 1s\n    memory: 1MB\n"
	const pkg = "id: QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPZ\n"

	found := auditDocs(t, nil, map[string]string{"/function/f.yaml": fn, "/package/p.yaml": pkg + "type: oci\nsource:\n    image: img\n"})
	has(t, found, schema.Broken, "runs package p, which is of type oci")

	found = auditDocs(t, nil, map[string]string{"/function/f.yaml": fn, "/package/p.yaml": pkg + "type: wasm\n"})
	has(t, found, schema.Broken, "runs the wasm package p and names no entrypoint")

	// a warp module has a default export, so it needs none
	found = auditDocs(t, nil, map[string]string{"/function/f.yaml": fn, "/package/p.yaml": pkg + "type: warp\n"})
	hasNo(t, found, "names no entrypoint")
	hasNo(t, found, "which is of type")
}

func TestAContainerIsKnownByItsPackageNotByAnImage(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{
		"/sync/work.yaml": "id: QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o\nqueue:\n    consensus: raft\n    visibility_timeout: 30s\n",
		"/package/w.yaml": "id: QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPZ\ntype: oci\nsource: services/w\nbuild:\n    dockerfile:\n        path: Dockerfile\n",
		"/worker/w.yaml": "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdJ\npackage:\n    name: w\nexecution:\n    timeout: 1s\n    memory: 1MB\n" +
			"sync:\n    work:\n        ops: [receive]\n",
	})
	has(t, found, schema.Broken, "declares no deliver port")
}

func TestAVariableIsALiteral(t *testing.T) {
	flat := compileBasic(t)

	lib := namedResource(t, resources(t, flat, "package"), "paylib")
	assert.Equal(t, nested(lib["variables"].(map[string]any), "GOPRIVATE", "value"), "github.com/acme/*")

	api := namedResource(t, resources(t, flat, "application"), "api")
	assert.Equal(t, nested(api["variables"].(map[string]any), "LOG_LEVEL", "value"), "info")

	// a secret is read through the vault, never handed over, and there is no second
	// lifetime to choose: a package's values are its build's, a workload's its own
	for path, body := range map[string]string{
		"/package/probe.yaml":  probePkg + "variables:\n    X:\n        secret:\n            name: stripe\n",
		"/function/probe.yaml": "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdJ\npackage:\n    name: redact_code\nexecution:\n    timeout: 1s\n    memory: 1MB\nvariables:\n    X:\n        at: build\n        value: y\n",
	} {
		_, err := buildDoc(t, path, body)
		assert.ErrorContains(t, err, "unknown key", path)
	}
}

func TestNoKindDeclaresASectionTwice(t *testing.T) {
	for _, g := range schema.V2Resources {
		if len(g.Children) == 0 {
			continue
		}
		kind, _ := g.Match.(string)
		seen := map[string]bool{}
		secs, _ := g.Children[0].Meta["sections"].([]engine.SectionSpec)
		for _, s := range secs {
			assert.Assert(t, !seen[s.ID], "%s declares the section %q twice", kind, s.ID)
			seen[s.ID] = true
		}
	}
}
