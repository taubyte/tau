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

const probeFn = "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLi\nexecution:\n    call: c\n    timeout: 1s\n    memory: 1MB\n"

// THE ABSENT BLOCK IS THE ZERO-CONFIG CASE, and it has to cost nothing. Everything
// about the build stanza rests on this: a repository that says nothing is built the way
// it is built today, so adding the vocabulary changes no existing project and the
// question "does the config win or does the file win" never arises — the block's
// PRESENCE is the switch, which is the rule `instances:` already states.
func TestABuildIsAbsentUntilItIsWritten(t *testing.T) {
	flat, err := buildDoc(t, "/function/probe.yaml", probeFn)
	assert.NilError(t, err)
	fn := namedResource(t, resources(t, flat, "function"), "probe")
	_, present := fn["build"]
	assert.Assert(t, !present, "an unwritten build left something in the object: %v", fn)
}

// Every branch, compiled, on the kinds that carry it. These are read off the FIXTURE
// rather than off documents written here, because the fixture is the example somebody
// reads to learn the vocabulary and a branch nothing exercises there is a branch nobody
// has seen work.
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

// A BLOCK THAT IS THERE AND NAMES NO WAY IS A TYPO, NOT A HALF-DECLARED BUILD. There is
// no third state between "the repository decides" and "this says how", which is what
// makes the absent block above mean exactly one thing.
func TestABuildMustSayHow(t *testing.T) {
	_, err := buildDoc(t, "/function/probe.yaml", probeFn+"build: {}\n")
	assert.ErrorContains(t, err, "attribute build/kind has an empty key")
}

// A WRONG PAIR IS UNREPRESENTABLE, and no cross-field check says so — the branch set is
// declared per kind and the fields are declared under their branch, so strict mode
// answers with the keys that actually exist there. This is §4's rule ("a mutual
// exclusion is Either(), never a check") applied twice: once across ways, once across
// kinds.
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

// A BUILD MAY PUBLISH IN PLACE, which is the plainest static site there is and was the
// one shape this block could not carry. THREE STATES, ALL DISTINCT: omitted is the
// output directory the builder hands the build, `.` is the source tree's own root, a
// path is somewhere under it. `output` took the path grammar at first, which refuses the
// root, on the reasoning that omitted already named it — it does not, it names a
// directory outside the source tree entirely. So a build that writes beside its own
// index.html had no spelling, and the build half of any descriptor publishing to the
// root could not be stated.
//
// The compiled object has to keep the two absences apart as well, or the vocabulary can
// say the thing and nothing downstream can read it back.
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

// A CONSTRAINT, NOT A PREFERENCE, so it is held by a test rather than by a sentence.
// An Either() segment is matched LITERALLY, so the branch key cannot carry an
// environment suffix: `commands_preview` is not a spelling of `commands`, it is a key
// the schema does not have. A preview deployment differs in the FIELDS under the
// branch, never in which branch it takes — which is also the right answer, since taking
// a different way of building in preview is building something else.
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

// The axis every other platform spells as a per-context build override, arriving here
// with nothing to invent: `run_preview` beside `run`. One document, one resource, one
// id — what production builds is the unsuffixed line sitting right there.
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

// THE ENGINE SEAM, held as a property rather than as a promise. Three things have to be
// true for a third tier to be a VALUE:
//
//   - every kind that is built says which tiers may run it, ON the kind, so no consumer
//     keeps a table of "a function is wasm" (§8c);
//   - every tier has a way of being produced, so declaring one is never declaring
//     something with no build;
//   - the kind that CHOOSES between tiers offers exactly the tiers it declares, from one
//     slice, so the enum and the annotation cannot drift.
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

	// AND A KIND WITH A CHOICE HAS SOMETHING TO MAKE IT WITH. runsAsContainer reads
	// `runtime` first and falls back to a kind's single engine, so a kind declaring two
	// tiers and no attribute choosing between them would answer "not a container" for
	// both of them — the safe direction, and silent. checks.go says that is unreachable
	// while `worker` is the only kind with two; this is what makes that true rather than
	// observed.
	for _, kind := range built {
		if len(schema.EnginesOf(kind)) > 1 {
			assert.Equal(t, kind, "worker",
				"%s declares more than one engine, so something has to choose between them", kind)
		}
	}

	// the one kind with a choice: the attribute's set IS the kind's set
	for _, g := range schema.V2Resources {
		if key, _ := g.Match.(string); key != "worker" {
			continue
		}
		for _, a := range g.Children[0].Attributes {
			if a.Name != "runtime" {
				continue
			}
			// `enum` is what InSet leaves behind, and it is also what a JSON Schema
			// and a completion read — so this asserts the same list an editor offers.
			in, _ := a.Meta["enum"].([]string)
			assert.DeepEqual(t, in, schema.EnginesOf("worker"))
			return
		}
		t.Fatal("worker declares two engines and no way to choose between them")
	}
}

// THE THREE THINGS ABOUT A BUILD THAT ARE TRUE ACROSS TWO BLOCKS. `source:` and
// `build:` are different attributes at different paths, so §3's switch has no single
// key to hang the exclusion on and the audit owes them — the same seam checkVariables
// sits on, for the same reason.
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

	// and a wasm worker built the way a wasm artifact is made says nothing
	found = auditDocs(t, nil, map[string]string{
		"/worker/w.yaml": "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdJ\nruntime: wasm\nexecution:\n    call: c\n    timeout: 1s\n    memory: 1MB\nbuild:\n    commands:\n        image: golang:1.23\n        run: make\n",
	})
	hasNo(t, found, "is built with")
}

// A CONTAINER BUILT HERE HAS NO IMAGE REFERENCE, so "is this a container" stopped being
// answerable by looking for one. Every check that asked it now asks the vocabulary, and
// this is the case that used to slip through: a queue consumer with no deliver port,
// built from a Dockerfile, reported as though it were a dispatched WASM component.
func TestAContainerIsKnownByItsKindNotByAnImage(t *testing.T) {
	found := auditDocs(t, nil, map[string]string{
		"/queue/work.yaml": "id: QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o\nvisibility_timeout: 30s\n",
		"/application/w.yaml": "id: QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPZ\nexecution:\n    memory: 1MB\n" +
			"source: services/w\nbuild:\n    dockerfile:\n        path: Dockerfile\n" +
			"queue:\n    work:\n        ops: [receive]\n",
	})
	has(t, found, schema.Broken, "declares no deliver port")
}

// WHEN A VALUE IS HANDED OVER, and the legal set is the kind's. A build argument and a
// runtime variable are one entry with two lifetimes — which is what gives a build-time
// credential the `secret:` branch it never had while it lived in a shell script.
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

// A SECTION IS DECLARED ONCE PER KIND, and this is here because the build stanza
// nearly broke it: `secBuild` was passed in six kinds' option lists AND registered by
// attachBuild, so six kinds carried the same heading twice. Nothing failed — a section
// is presentation, so a duplicate compiles, generates and renders as two identical
// panels with the fields split between them.
//
// The rule the trap comes from is attachPlacement's: a block attached to a set of kinds
// registers its own section, because the attach pass is the one place that knows which
// kinds got it. A kind that also declares it is the second copy.
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
