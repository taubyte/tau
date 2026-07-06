package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestGeneratedClientE2E is an end-to-end check of the whole
// DSL -> tcc-gen -> generated TypeScript -> wasm pipeline: it generates the wasm
// module and the TS schema FRESH into a temp package, drops the hand-written
// runtime and tests alongside, then typechecks (tsc) and runs the tests against
// that generated code — so the generation is validated, not just the committed
// src/gen/schema.ts.
//
// Everything is driven from Go (buildWasm/writeTS are called directly; tsc/npm
// via exec). It needs node + the client's dev deps, so it skips gracefully when
// those aren't present or under -short.
func TestGeneratedClientE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping generated-client e2e under -short")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not found; skipping generated-client e2e")
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := findRepoRoot(cwd)
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(root, "pkg", "tcc", "clients", "js")

	nodeModules := filepath.Join(pkg, "node_modules")
	if _, err := os.Stat(nodeModules); err != nil {
		t.Skipf("client deps missing; run `npm install` in %s", pkg)
	}

	tmp := t.TempDir()

	// 1. generate the wasm module + TS schema fresh into the temp package.
	if err := buildWasm(root, filepath.Join(tmp, "assets")); err != nil {
		t.Fatalf("generate wasm: %v", err)
	}
	if err := writeTS(root, filepath.Join(tmp, "src", "gen")); err != nil {
		t.Fatalf("generate ts: %v", err)
	}

	// 2. drop the hand-written runtime, tests, and config alongside.
	if err := os.MkdirAll(filepath.Join(tmp, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyInto := func(rel string, dstDir string) {
		t.Helper()
		if err := copyFile(filepath.Join(pkg, rel), filepath.Join(dstDir, filepath.Base(rel))); err != nil {
			t.Fatalf("copy %s: %v", rel, err)
		}
	}
	for _, f := range []string{"src/fs.ts", "src/loader.ts", "src/index.ts", "src/tcc.test.ts"} {
		copyInto(f, filepath.Join(tmp, "src"))
	}
	for _, f := range []string{"package.json", "tsconfig.json", "tsconfig.build.json"} {
		copyInto(f, tmp)
	}

	// 3. reuse the client's dev deps (no network).
	if err := os.Symlink(nodeModules, filepath.Join(tmp, "node_modules")); err != nil {
		t.Fatal(err)
	}

	// 4. typecheck then run the tests against the generated code.
	fixture := filepath.Join(root, "pkg", "tcc", "taubyte", "v1", "fixtures", "config")
	npm := func(args ...string) {
		t.Helper()
		cmd := exec.Command("npm", args...)
		cmd.Dir = tmp
		cmd.Env = append(os.Environ(), "TCC_FIXTURE="+fixture)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("npm %v failed: %v\n%s", args, err, out)
		}
		t.Logf("npm %v:\n%s", args, out)
	}
	npm("run", "build") // tsc typecheck of the generated code
	npm("test")         // run the tests against the generated code
}
