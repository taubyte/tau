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

//go:embed runtime/node-modules/async_hooks.js
var nodeAsyncHooks string

//go:embed runtime/node-modules/events.js
var nodeEvents string

//go:embed runtime/node-modules/buffer.js
var nodeBuffer string

//go:embed runtime/node-modules/cloudflare-workers.js
var cloudflareWorkers string

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
	site := flag.String("site", "", "directory of static/prerendered assets (e.g. SvelteKit's .svelte-kit/cloudflare) to assemble with the handler into a complete website build.zip at --out")
	assetMax := flag.Int64("asset-embed-max", defaultAssetEmbedMax, "max size (bytes) of a text-like --site asset to embed into the bundle for env.ASSETS resolution; larger assets are left to the static layer")
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

	// next-on-pages emits a multi-module worker (per-route *.func.js loaded via
	// dynamic import); fold it into one self-contained entry first.
	if nopEntry, err := prepareNextOnPages(entryAbs, tmp); err != nil {
		return fmt.Errorf("preparing next-on-pages worker failed with: %w", err)
	} else if nopEntry != "" {
		entryAbs = nopEntry
		fmt.Fprintln(os.Stderr, "taubyte-ssr-adapter: detected next-on-pages worker; bundling route modules into one")
	}

	// 1. Materialise the bridge: the shim + a tiny entrypoint importing the app.
	shimPath := filepath.Join(tmp, "shim.js")
	if err := os.WriteFile(shimPath, []byte(shimSource), 0o644); err != nil {
		return err
	}

	// Runtime polyfills are installed (in order) before the app module runs.
	var prelude string
	var aliases []string
	writePolyfill := func(name, src string) (string, error) {
		p := filepath.Join(tmp, name)
		return p, os.WriteFile(p, []byte(src), 0o644)
	}
	if *node {
		// Node-compat globals first (process/Buffer/global), so web.js and the app
		// can rely on them.
		p, err := writePolyfill("node.js", nodePolyfill)
		if err != nil {
			return err
		}
		prelude += fmt.Sprintf("import %q;\n", p)

		// Node builtin-module shims, aliased so `import ... from "node:async_hooks"`
		// (and the bare specifier) resolve during bundling.
		for _, mod := range []struct{ name, src string }{
			{"async_hooks", nodeAsyncHooks},
			{"events", nodeEvents},
			{"buffer", nodeBuffer},
		} {
			mp, err := writePolyfill(mod.name+".mjs", mod.src)
			if err != nil {
				return err
			}
			aliases = append(aliases, "--alias:node:"+mod.name+"="+mp, "--alias:"+mod.name+"="+mp)
		}
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

		// Edge adapters (SvelteKit/Next on Cloudflare) import the Workers runtime
		// virtual module; resolve it to a binding-less shim.
		cf, err := writePolyfill("cloudflare-workers.mjs", cloudflareWorkers)
		if err != nil {
			return err
		}
		aliases = append(aliases, "--alias:cloudflare:workers="+cf)

		// Embed text-like static/prerendered assets so env.ASSETS resolves them
		// in-process — the wasi-stdio bundle can't call back to the host mid-render.
		// Bounded by a per-file cap; larger/binary assets are served by the static
		// layer. Lets a standalone bundle serve its own prerendered pages and gives
		// SvelteKit's read() real bytes.
		if *site != "" {
			mod, n, err := buildAssetModule(*site, *assetMax)
			if err != nil {
				return fmt.Errorf("embedding site assets failed with: %w", err)
			}
			if n > 0 {
				ap, err := writePolyfill("assets.js", mod)
				if err != nil {
					return err
				}
				prelude += fmt.Sprintf("import %q;\n", ap)
				fmt.Fprintf(os.Stderr, "taubyte-ssr-adapter: embedded %d assets for env.ASSETS\n", n)
			}
		}

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
	if err := bundleJS(bridgePath, bundlePath, aliases...); err != nil {
		return fmt.Errorf("bundling failed (is esbuild installed?): %w", err)
	}
	if dst := os.Getenv("TAUBYTE_SSR_KEEP_BUNDLE"); dst != "" {
		if data, rerr := os.ReadFile(bundlePath); rerr == nil {
			_ = os.WriteFile(dst, data, 0o644)
		}
	}

	// 3. Compile JS -> WASM (WASI stdin/stdout) with Javy.
	wasmPath := filepath.Join(tmp, "module.wasm")
	eventLoop, err := javyBuild(bundlePath, wasmPath)
	if err != nil {
		return fmt.Errorf("javy build failed (is javy installed?): %w", err)
	}
	if !eventLoop {
		// The build succeeded but without the event loop, so QuickJS will trap
		// ("Pending jobs in the event queue") the moment a handler awaits. fetch
		// mode is always async (serveFetch resolves a Promise), so refuse it
		// outright; handler mode may be fully synchronous, so warn and proceed.
		const hint = "javy could not enable the event loop, so async handlers (any Promise/await) will trap at runtime. " +
			"Upgrade Javy to >= 5.0 (uses `build -J event-loop=y`) or set TAUBYTE_JAVY_ARGS to an event-loop-enabling invocation"
		if *mode == "fetch" {
			return fmt.Errorf("%s. fetch mode is inherently async and cannot run without it", hint)
		}
		fmt.Fprintln(os.Stderr, "taubyte-ssr-adapter: WARNING: "+hint+". Only fully-synchronous handlers will work.")
	}

	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		return err
	}

	// 4. Package and write outputs. By default --out is the handler.wasm.zip; with
	// --site it becomes a complete, deployable website build.zip (static assets +
	// handler + manifest), so prerendered pages serve from the static layer and
	// dynamic routes hit the bundle.
	handlerZip, err := buildHandlerZip(wasm)
	if err != nil {
		return err
	}
	outBytes, kind := handlerZip, "handler.wasm.zip"
	if *site != "" {
		outBytes, err = buildSiteZip(*site, handlerZip, buildManifest(*framework))
		if err != nil {
			return fmt.Errorf("assembling website from `%s` failed with: %w", *site, err)
		}
		kind = "website build.zip"
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*out, outBytes, 0o644); err != nil {
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

	fmt.Printf("taubyte-ssr-adapter: wrote %s %s (%d bytes)\n", kind, *out, len(outBytes))
	return nil
}

// bundleJS bundles entry into a single ES module using esbuild (direct binary
// or via npx). extra holds additional esbuild flags (e.g. --alias: for node
// builtin-module shims).
func bundleJS(entry, out string, extra ...string) error {
	// Target es2020 so esbuild transpiles newer syntax (class static blocks,
	// logical-assignment, .at(), top-level await spots, etc.) that Javy/QuickJS's
	// bytecode compiler doesn't fully support — heavy framework/webpack output
	// (Next.js) otherwise crashes javy with a bytecode "stack underflow".
	args := []string{"--bundle", entry, "--format=esm", "--platform=neutral", "--target=es2020", "--outfile=" + out}
	args = append(args, extra...)
	if path, err := exec.LookPath("esbuild"); err == nil {
		return runCmd(path, args...)
	}
	return runCmd("npx", append([]string{"--yes", "esbuild"}, args...)...)
}

// javyBuild compiles a JS module to a WASI WASM module with Javy and reports
// whether it could enable the event loop. The event loop drains QuickJS's job
// queue before the module exits, so async handlers (Hono's app.fetch, a fetch
// worker, anything returning a Promise) resolve instead of trapping with
// "Pending jobs in the event queue".
//
// The flag for it differs sharply across Javy versions (build -J event-loop=y
// on v5+, --enable-experimental-event-loop on older lines), and the default
// plugin in v3/v4 dropped it entirely. So the event-loop forms are tried first;
// only if every one fails do we fall back to a plain build — and we tell the
// caller (via the returned bool) so it can warn or refuse, rather than silently
// shipping a module that traps on the first await. Override the whole
// invocation with TAUBYTE_JAVY_ARGS (space-separated; %IN and %OUT substituted).
func javyBuild(in, out string) (eventLoop bool, err error) {
	javy, err := exec.LookPath("javy")
	if err != nil {
		return false, fmt.Errorf("javy not found on PATH: %w", err)
	}

	var override [][]string
	if v := os.Getenv("TAUBYTE_JAVY_ARGS"); strings.TrimSpace(v) != "" {
		fields := strings.Fields(v)
		for i, f := range fields {
			switch f {
			case "%IN":
				fields[i] = in
			case "%OUT":
				fields[i] = out
			}
		}
		override = append(override, fields)
	}

	// Event-loop forms, preferred: modern `build`, then the older flag on both
	// `build` and the legacy `compile`. A user override is trusted to enable it.
	eventLoopForms := append(override,
		[]string{"build", "-J", "event-loop=y", in, "-o", out},
		[]string{"build", "--enable-experimental-event-loop", in, "-o", out},
		[]string{"compile", "--enable-experimental-event-loop", in, "-o", out},
	)
	// Plain forms, last resort: async handlers will trap at runtime.
	plainForms := [][]string{
		{"build", in, "-o", out},
		{"compile", in, "-o", out},
	}

	var last string
	try := func(forms [][]string) bool {
		for _, args := range forms {
			combined, runErr := exec.Command(javy, args...).CombinedOutput()
			if runErr == nil {
				return true
			}
			last = fmt.Sprintf("javy %s: %v\n%s", strings.Join(args, " "), runErr, combined)
		}
		return false
	}

	if try(eventLoopForms) {
		return true, nil
	}
	if try(plainForms) {
		return false, nil
	}
	return false, fmt.Errorf("javy build failed (tried event-loop and plain variants):\n%s", last)
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
