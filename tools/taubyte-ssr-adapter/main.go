// Command taubyte-ssr-adapter compiles a JavaScript request handler (a module
// default-exporting `handle(req) -> res` over plain JSON objects) into a Taubyte
// server bundle: it bundles the handler together with a small bridge shim,
// compiles the result to WebAssembly with Javy (QuickJS), and packages it as
// the website handler zip (+ optional SSR manifest).
//
// Bare Javy has no Web APIs, so the base contract is polyfill-free JSON; Hono /
// Next.js need a Web-API polyfill bundled in (see README.md).
//
// Pipeline: entry.js + shim  --esbuild-->  bundle.js  --javy-->  module.wasm
//
//	--> handler.wasm.zip
//
// PROTOTYPE STATUS: the packaging and manifest emission are covered by tests.
// The esbuild/javy steps shell out to those tools (not bundled here), and the
// produced bundle uses the WASI-stdio ABI, which the substrate must support to
// execute it (see README.md "Runtime support"). Validate the JS pipeline with
// the toolchain installed.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed shim/shim.js
var shimSource string

//go:embed runtime/web.js
var webPolyfill string

//go:embed runtime/node.js
var nodePolyfill string

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "taubyte-ssr-adapter:", err)
		os.Exit(1)
	}
}

func run() error {
	entry := flag.String("entry", "", "path to the app entry module")
	out := flag.String("out", "handler.wasm.zip", "output path for the server bundle zip")
	manifestOut := flag.String("manifest", "", "optional path to also write the SSR manifest (ssr.json)")
	framework := flag.String("framework", "js", "framework name recorded in the manifest")
	mode := flag.String("mode", "handler", "entry shape: `handler` (default-export handle(req)->res) or `fetch` (Web-standard app.fetch(Request), e.g. Hono)")
	node := flag.Bool("node", false, "inject Node-compat shims (process/Buffer/global/timers) — needed by Next.js edge handlers")
	flag.Parse()

	if *entry == "" {
		return fmt.Errorf("--entry is required")
	}
	if *mode != "handler" && *mode != "fetch" {
		return fmt.Errorf("--mode must be `handler` or `fetch`")
	}
	entryAbs, err := filepath.Abs(*entry)
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "tb-ssr-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	// 1. Materialise the bridge: the shim + a tiny entrypoint importing the app.
	shimPath := filepath.Join(tmp, "shim.js")
	if err := os.WriteFile(shimPath, []byte(shimSource), 0o644); err != nil {
		return err
	}

	// Runtime polyfills are installed (in order) before the app module runs.
	var prelude string
	writePolyfill := func(name, src string) (string, error) {
		p := filepath.Join(tmp, name)
		return p, os.WriteFile(p, []byte(src), 0o644)
	}
	if *node {
		// Node-compat shims first (process/Buffer/global), so web.js and the app
		// can rely on them.
		p, err := writePolyfill("node.js", nodePolyfill)
		if err != nil {
			return err
		}
		prelude += fmt.Sprintf("import %q;\n", p)
	}

	bridge := fmt.Sprintf("import app from %q;\n", entryAbs)
	if *mode == "fetch" {
		// Install the Web API polyfill (Request/Response/Headers/URL) before the
		// app runs, then dispatch through the Web-standard fetch handler.
		p, err := writePolyfill("web.js", webPolyfill)
		if err != nil {
			return err
		}
		prelude += fmt.Sprintf("import %q;\n", p)
		bridge = prelude + bridge + fmt.Sprintf("import { serveFetch } from %q;\nserveFetch(app);\n", shimPath)
	} else {
		bridge = prelude + bridge + fmt.Sprintf("import { serve } from %q;\nserve(app);\n", shimPath)
	}

	bridgePath := filepath.Join(tmp, "bridge.js")
	if err := os.WriteFile(bridgePath, []byte(bridge), 0o644); err != nil {
		return err
	}

	// 2. Bundle to a single module.
	bundlePath := filepath.Join(tmp, "bundle.js")
	if err := bundleJS(bridgePath, bundlePath); err != nil {
		return fmt.Errorf("bundling failed (is esbuild installed?): %w", err)
	}

	// 3. Compile JS -> WASM (WASI stdin/stdout) with Javy.
	wasmPath := filepath.Join(tmp, "module.wasm")
	if err := javyBuild(bundlePath, wasmPath); err != nil {
		return fmt.Errorf("javy build failed (is javy installed?): %w", err)
	}

	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		return err
	}

	// 4. Package and write outputs.
	zipBytes, err := buildHandlerZip(wasm)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*out, zipBytes, 0o644); err != nil {
		return err
	}

	if *manifestOut != "" {
		data, err := buildManifest(*framework).Marshal()
		if err != nil {
			return err
		}
		if err := os.WriteFile(*manifestOut, data, 0o644); err != nil {
			return err
		}
	}

	fmt.Printf("taubyte-ssr-adapter: wrote %s (%d bytes)\n", *out, len(zipBytes))
	return nil
}

// bundleJS bundles entry into a single ES module using esbuild (direct binary
// or via npx).
func bundleJS(entry, out string) error {
	args := []string{"--bundle", entry, "--format=esm", "--platform=neutral", "--outfile=" + out}
	if path, err := exec.LookPath("esbuild"); err == nil {
		return runCmd(path, args...)
	}
	return runCmd("npx", append([]string{"--yes", "esbuild"}, args...)...)
}

// javyBuild compiles a JS module to a WASI WASM module with Javy, with the
// event loop enabled so async handlers (Hono's app.fetch, etc.) — whose
// promises sit on QuickJS's job queue — are drained before the module exits.
//
// The subcommand (`build` vs `compile`) and the event-loop flag differ across
// Javy versions, so the enabled forms are tried first (preferred) and a plain
// build only as a last resort. Override the whole invocation with
// TAUBYTE_JAVY_ARGS (space-separated; %IN and %OUT are substituted).
func javyBuild(in, out string) error {
	javy, err := exec.LookPath("javy")
	if err != nil {
		return fmt.Errorf("javy not found on PATH: %w", err)
	}

	var attempts [][]string
	if override := os.Getenv("TAUBYTE_JAVY_ARGS"); strings.TrimSpace(override) != "" {
		fields := strings.Fields(override)
		for i, f := range fields {
			switch f {
			case "%IN":
				fields[i] = in
			case "%OUT":
				fields[i] = out
			}
		}
		attempts = append(attempts, fields)
	}
	attempts = append(attempts,
		// event-loop enabled (preferred) — modern `build`, then legacy `compile`
		[]string{"build", "-J", "event-loop=y", in, "-o", out},
		[]string{"build", "--enable-experimental-event-loop", in, "-o", out},
		[]string{"compile", "--enable-experimental-event-loop", in, "-o", out},
		// plain (async handlers will trap at runtime) — last resort
		[]string{"build", in, "-o", out},
		[]string{"compile", in, "-o", out},
	)

	var last string
	for _, args := range attempts {
		combined, err := exec.Command(javy, args...).CombinedOutput()
		if err == nil {
			return nil
		}
		last = fmt.Sprintf("javy %s: %v\n%s", strings.Join(args, " "), err, combined)
	}
	return fmt.Errorf("javy build failed (tried event-loop variants):\n%s", last)
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
