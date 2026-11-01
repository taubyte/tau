package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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
	v2, err := selectDSL("v2")
	if err != nil {
		t.Fatal(err)
	}
	if err := buildWasm(root, filepath.Join(tmp, "assets"), v2); err != nil {
		t.Fatalf("generate wasm: %v", err)
	}
	if err := writeTS(root, filepath.Join(tmp, "src", "gen"), v2); err != nil {
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
	for _, f := range []string{"src/fs.ts", "src/loader.ts", "src/index.ts", "src/tcc.test.ts", "src/browser.spec.ts"} {
		copyInto(f, filepath.Join(tmp, "src"))
	}
	for _, f := range []string{"package.json", "tsconfig.json", "tsconfig.build.json", "web-test-runner.config.mjs"} {
		copyInto(f, tmp)
	}

	// 3. reuse the client's dev deps (no network).
	if err := os.Symlink(nodeModules, filepath.Join(tmp, "node_modules")); err != nil {
		t.Fatal(err)
	}

	// 4. typecheck then run the tests against the generated code.
	fixture := filepath.Join(root, "pkg", "tcc", "taubyte", "v2", "fixtures", "basic")
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
	npm("test")         // run the Node tests against the generated code

	// run the same wasm in a real headless browser, when Chrome is available.
	if _, err := exec.LookPath("google-chrome-stable"); err == nil {
		npm("run", "test:browser")
	} else {
		t.Log("google-chrome-stable not found; skipping browser test")
	}
}
