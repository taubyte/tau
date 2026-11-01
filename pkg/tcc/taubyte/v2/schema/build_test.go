package schema_test

import (
	"context"
	"sort"
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

const probeFn = "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLi\nruntime: wasm\nexecution:\n    call: c\n    timeout: 1s\n    memory: 1MB\n"

func TestABuildIsAbsentUntilItIsWritten(t *testing.T) {
	flat, err := buildDoc(t, "/function/probe.yaml", probeFn)
	assert.NilError(t, err)
	fn := namedResource(t, resources(t, flat, "function"), "probe")
	_, present := fn["build"]
	assert.Assert(t, !present, "an unwritten build left something in the object: %v", fn)
}

func TestEveryWayOfBuildingCompiles(t *testing.T) {
	flat := compileBasic(t)

	// commands: a toolchain, two steps and where the product landed
	site := namedResource(t, resources(t, flat, "website"), "shop")
	assert.Equal(t, nested(site, "build", "kind"), "commands")
	assert.Equal(t, nested(site, "build", "image"), "node:22")
	assert.Equal(t, nested(site, "build", "install"), "pnpm install --frozen-lockfile")
	assert.Equal(t, nested(site, "build", "run"), "pnpm build")
	assert.Equal(t, nested(site, "build", "output"), "dist")

	// script: the escape hatch, named rather than found
	lib := namedResource(t, resources(t, flat, "component"), "paylib")
	assert.Equal(t, nested(lib, "build", "kind"), "script")
	assert.Equal(t, nested(lib, "build", "script"), ".taubyte/build.sh")
	assert.Equal(t, nested(lib, "build", "image"), "taubyte/go-wasi:v2")

	// dockerfile: the container tier, built HERE rather than pulled
	pool := namedResource(t, resources(t, flat, "application"), "worker_pool")
	assert.Equal(t, nested(pool, "build", "kind"), "dockerfile")
	assert.Equal(t, nested(pool, "build", "dockerfile"), "Dockerfile")
	assert.Equal(t, nested(pool, "build", "target"), "runtime")
	assert.Equal(t, pool["source"], "services/pool")

	// and the other answer to the same question: somebody else built it
	api := namedResource(t, resources(t, flat, "application"), "api")
	assert.Equal(t, nested(api, "source", "kind"), "image")
	assert.Equal(t, nested(api, "source", "image"), "ghcr.io/acme/api:1.4")
	_, built := api["build"]
	assert.Assert(t, !built, "a pulled image has no build")
}

func TestABuildMustSayHow(t *testing.T) {
	_, err := buildDoc(t, "/function/probe.yaml", probeFn+"build: {}\n")
	assert.ErrorContains(t, err, "attribute build/kind has an empty key")
}

func TestAWrongPairIsRefusedByShape(t *testing.T) {
	for _, tc := range []struct{ name, path, body, want string }{
		{"a script has no install step", "/function/probe.yaml",
			probeFn + "build:\n    script:\n        image: g\n        path: b.sh\n        install: npm ci\n",
			"unknown key 'install'"},
		{"a website cannot be built into an image", "/website/probe.yaml",
			"id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVt\nbuild:\n    dockerfile:\n        path: Dockerfile\n",
			"unknown key 'dockerfile'; valid keys here: commands, script"},
		{"a container is not built by a shell command", "/application/probe.yaml",
			"id: QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPZ\nexecution:\n    memory: 1MB\nbuild:\n    commands:\n        image: node:22\n        run: x\n",
			"unknown key 'commands'; valid keys here: dockerfile"},
		{"two ways at once", "/function/probe.yaml",
			probeFn + "build:\n    script:\n        image: g\n        path: b.sh\n    commands:\n        image: g\n        run: x\n",
			"ambiguous path: commands and script each match, and only one may be set"},
		{"a build with nothing to run it in", "/function/probe.yaml",
			probeFn + "build:\n    commands:\n        run: make\n",
			"required attribute 'build/commands/image'"},
		{"a command build with no command", "/function/probe.yaml",
			probeFn + "build:\n    commands:\n        image: node:22\n",
			"required attribute 'build/commands/run'"},
		{"a script branch with no script", "/function/probe.yaml",
			probeFn + "build:\n    script:\n        image: node:22\n",
			"required attribute 'build/script/path'"},
		{"a dockerfile branch with no dockerfile", "/application/probe.yaml",
			"id: QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPZ\nexecution:\n    memory: 1MB\nbuild:\n    dockerfile:\n        target: prod\n",
			"required attribute 'build/dockerfile/path'"},
		{"the output is a path in the repository", "/function/probe.yaml",
			probeFn + "build:\n    commands:\n        image: node:22\n        run: make\n        output: \"../dist\"\n",
			"climbs out of the repository"},
		{"and the root has exactly one spelling", "/function/probe.yaml",
			probeFn + "build:\n    commands:\n        image: node:22\n        run: make\n        output: \"./\"\n",
			"must not end in /"},
		{"an empty output is not one of the three states", "/function/probe.yaml",
			probeFn + "build:\n    commands:\n        image: node:22\n        run: make\n        output: \"\"\n",
			"empty: write a path, or \".\" for the repository root"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildDoc(t, tc.path, tc.body)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestABuildMayPublishToTheRoot(t *testing.T) {
	const head = "id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVt\nserve:\n    mode: spa\n"
	const cmds = "build:\n    commands:\n        image: node:22\n        install: npm ci\n        run: npm run build\n"

	flat, err := buildDoc(t, "/website/publishroot.yaml", head+cmds+"        output: \".\"\n")
	assert.NilError(t, err)
	site := namedResource(t, resources(t, flat, "website"), "publishroot")
	assert.Equal(t, nested(site, "build", "output"), ".",
		"the root has to be IN the object: a build that published in place is not a build that said nothing")

	// and the state it is not
	flat, err = buildDoc(t, "/website/publishroot.yaml", head+cmds)
	assert.NilError(t, err)
	site = namedResource(t, resources(t, flat, "website"), "publishroot")
	_, present := nestedOK(site, "build", "output")
	assert.Assert(t, !present, "an unwritten output left something in the object: %v", site["build"])
}

func TestABuildBranchCannotBeEnvironmentSuffixed(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/function/probe.yaml",
		[]byte(probeFn+"build:\n    commands_preview:\n        image: node:22\n        run: x\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"),
		schema.WithEnvironments("production", "preview"), schema.WithEnvironment("preview"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "unknown key 'commands_preview'")
}

func TestABuildDiffersPerEnvironment(t *testing.T) {
	at := func(env string) map[string]any {
		t.Helper()
		c, err := schema.New(schema.WithLocal(envFixture), schema.WithBranch("main"),
			schema.WithProjectEnvironments(afero.NewOsFs(), envFixture), schema.WithEnvironment(env))
		assert.NilError(t, err)
		obj, _, err := c.Compile(context.Background())
		assert.NilError(t, err)
		return nested(namedResource(t, resources(t, obj.Flat(), "function"), "checkout"), "build").(map[string]any)
	}

	prod := at("production")
	assert.Equal(t, prod["image"], "rust:1.83")
	assert.Equal(t, prod["run"], "cargo build --release")
	assert.Equal(t, prod["output"], "target/release/checkout.wasm")

	preview := at("preview")
	assert.Equal(t, preview["image"], "rust:1.83-slim")
	assert.Equal(t, preview["run"], "cargo build")
	assert.Equal(t, preview["output"], "target/debug/checkout.wasm")

	// the branch is the same one in both: what differs is how, never what kind of
	assert.Equal(t, prod["kind"], "commands")
	assert.Equal(t, preview["kind"], "commands")

	// staging declares none of its own and reads the base, which is the whole reason
	// nothing is duplicated
	assert.Equal(t, at("staging")["run"], "cargo build --release")
}

func TestTheEngineSeamIsAValue(t *testing.T) {
	built := schema.BuiltKinds()
	sort.Strings(built)
	assert.DeepEqual(t, built, []string{"application", "component", "function", "sandbox", "website", "worker"})

	for _, kind := range built {
		engines := schema.EnginesOf(kind)
		assert.Assert(t, len(engines) > 0, "%s is built and names no engine", kind)
		for _, e := range engines {
			assert.Assert(t, len(schema.BuildWaysOf(e)) > 0,
				"%s runs on %q and nothing here says how a %q artifact is produced", kind, e, e)
		}
	}

	// a kind that runs nothing declares nothing, and the absence is the statement
	for _, kind := range []string{"cache", "database", "cron", "zone", "model", "agent", "workflow"} {
		assert.Assert(t, len(schema.EnginesOf(kind)) == 0, "%s declares an engine and is not built", kind)
	}

	// A kind with a choice of tier has `runtime` to choose with, and its set is the
	// kind's: runsAsContainer falls back to a kind's single engine.
	choosers := map[string][]string{}
	for _, g := range schema.V2Resources {
		kind, _ := g.Match.(string)
		if len(g.Children) == 0 || len(schema.EnginesOf(kind)) < 2 {
			continue
		}
		var in []string
		for _, a := range g.Children[0].Attributes {
			if a.Name == "runtime" {
				in, _ = a.Meta["enum"].([]string)
			}
		}
		assert.Assert(t, in != nil, "%s declares more than one engine and no way to choose between them", kind)
		assert.DeepEqual(t, in, schema.EnginesOf(kind))
		choosers[kind] = in
	}
	assert.DeepEqual(t, choosers, map[string][]string{
		"function": {"wasm", "js"},
		"worker":   {"wasm", "js", "container"},
	})

	// js is produced the ways a component is: a toolchain image runs, and what it left
	// in its output directory is the artifact.
	assert.DeepEqual(t, schema.BuildWaysOf("js"), schema.BuildWaysOf("wasm"))
	assert.DeepEqual(t, schema.BuildWaysOf("js"), []string{"commands", "script"})
}

func TestAuditBuild(t *testing.T) {
	const appHead = "id: QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPZ\nexecution:\n    memory: 1MB\nports:\n    http:\n        number: 80\n"
	route := "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdH\nhost:\n    generated:\n        name: probe\nroute:\n    path: /\n    handler:\n        application:\n            name: a\n            port: http\n"

	found := auditDocs(t, nil, map[string]string{
		"/gateway/g.yaml":     route,
		"/application/a.yaml": appHead + "source:\n    image: img\nbuild:\n    dockerfile:\n        path: Dockerfile\n",
	})
	has(t, found, schema.Broken, "takes its content from an image and also declares a build")

	found = auditDocs(t, nil, map[string]string{
		"/gateway/g.yaml":     route,
		"/application/a.yaml": appHead,
	})
	has(t, found, schema.Broken, "names no image and declares no build")

	// either one alone is fine
	for _, ok := range []string{"source:\n    image: img\n", "source: api\nbuild:\n    dockerfile:\n        path: Dockerfile\n"} {
		found = auditDocs(t, nil, map[string]string{"/gateway/g.yaml": route, "/application/a.yaml": appHead + ok})
		hasNo(t, found, "names no image and declares no build")
		hasNo(t, found, "also declares a build")
	}

	// the way of building and the tier that runs the product, where a kind chooses
	found = auditDocs(t, nil, map[string]string{
		"/worker/w.yaml": "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdJ\nruntime: wasm\nexecution:\n    call: c\n    timeout: 1s\n    memory: 1MB\nbuild:\n    dockerfile:\n        path: Dockerfile\n",
	})
	has(t, found, schema.Broken, "runs on the wasm tier and is built with \"dockerfile\"")

	// and a module is made the ways a component is, so a Dockerfile does not make one
	found = auditDocs(t, nil, map[string]string{
		"/worker/w.yaml": "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdJ\nruntime: js\nexecution:\n    timeout: 1s\n    memory: 1MB\nbuild:\n    dockerfile:\n        path: Dockerfile\n",
	})
	has(t, found, schema.Broken, "runs on the js tier and is built with \"dockerfile\"")

	// and a wasm worker built the way a wasm artifact is made says nothing
	found = auditDocs(t, nil, map[string]string{
		"/worker/w.yaml": "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdJ\nruntime: wasm\nexecution:\n    call: c\n    timeout: 1s\n    memory: 1MB\nbuild:\n    commands:\n        image: golang:1.23\n        run: make\n",
	})
	hasNo(t, found, "is built with")
}

func TestAContainerIsKnownByItsKindNotByAnImage(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{
		"/sync/work.yaml": "id: QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o\nqueue:\n    consensus: raft\n    visibility_timeout: 30s\n",
		"/application/w.yaml": "id: QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPZ\nexecution:\n    memory: 1MB\n" +
			"source: services/w\nbuild:\n    dockerfile:\n        path: Dockerfile\n" +
			"sync:\n    work:\n        ops: [receive]\n",
	})
	has(t, found, schema.Broken, "declares no deliver port")
}

func TestAVariableSaysWhenItIsHandedOver(t *testing.T) {
	flat := compileBasic(t)

	// a component has no running process, so `build` is the only lifetime and the
	// default says so without anybody writing it
	lib := namedResource(t, resources(t, flat, "component"), "paylib")
	vars := lib["variables"].(map[string]any)
	assert.Equal(t, nested(vars, "GOPRIVATE", "at"), "build")
	assert.Equal(t, nested(vars, "GITHUB_TOKEN", "at"), "build")
	_, secret := vars["GITHUB_TOKEN"].(map[string]any)["secret"]
	assert.Assert(t, secret, "a build-time credential is a reference, not a literal")

	// a workload runs, so its default is the lifetime it had before this field existed
	api := namedResource(t, resources(t, flat, "application"), "api")
	assert.Equal(t, nested(api["variables"].(map[string]any), "LOG_LEVEL", "at"), "run")

	// and writing a lifetime a kind does not have is a compile error naming the ones it does
	_, err := buildDoc(t, "/website/probe.yaml",
		"id: QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVt\nvariables:\n    X:\n        at: run\n        value: y\n")
	assert.ErrorContains(t, err, "invalid value")
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
