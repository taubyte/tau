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

//go:embed runtime/node-modules/node-http.js
var nodeHTTP string

// Node builtin-module shims for the Node HTTP-server surface (--mode node).
//
//go:embed runtime/node-modules/path.js
var nodePath string

//go:embed runtime/node-modules/querystring.js
var nodeQuerystring string

//go:embed runtime/node-modules/string_decoder.js
var nodeStringDecoder string

//go:embed runtime/node-modules/url.js
var nodeURL string

//go:embed runtime/node-modules/util.js
var nodeUtil string

//go:embed runtime/node-modules/stream.js
var nodeStream string

//go:embed runtime/node-modules/crypto.js
var nodeCrypto string

//go:embed runtime/node-modules/fs.js
var nodeFS string

//go:embed runtime/node-modules/net.js
var nodeNet string

//go:embed runtime/node-modules/zlib.js
var nodeZlib string

//go:embed runtime/node-modules/assert.js
var nodeAssert string

//go:embed runtime/node-modules/v8.js
var nodeV8 string

//go:embed runtime/node-modules/os.js
var nodeOS string

//go:embed runtime/node-modules/diagnostics_channel.js
var nodeDiagnosticsChannel string

//go:embed runtime/node-modules/dns.js
var nodeDNS string

//go:embed runtime/node-modules/http2.js
var nodeHTTP2 string

//go:embed runtime/node-modules/perf_hooks.js
var nodePerfHooks string

//go:embed runtime/node-modules/repl.js
var nodeREPL string

//go:embed runtime/node-modules/bun.js
var bunRuntime string

//go:embed runtime/node-modules/deno.js
var denoRuntime string

//go:embed runtime/node-modules/cloudflare-workers.js
var cloudflareWorkers string

//go:embed shim/component.js
var componentShim string

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
	mode := flag.String("mode", "handler", "entry shape: `handler` (default-export handle(req)->res), `fetch` (Web-standard app.fetch(Request), e.g. Hono), `node` (a Node HTTP-server app: http.createServer/app.listen, e.g. Express/Koa), `bun` (a Bun.serve({fetch}) app), or `deno` (a Deno.serve(handler) app)")
	node := flag.Bool("node", false, "inject Node-compat shims (process/Buffer/global/timers) — needed by Next.js edge handlers")
	site := flag.String("site", "", "directory of static/prerendered assets (e.g. SvelteKit's .svelte-kit/cloudflare) to assemble with the handler into a complete website build.zip at --out")
	assetMax := flag.Int64("asset-embed-max", defaultAssetEmbedMax, "max size (bytes) of a text-like --site asset to embed into the bundle for env.ASSETS resolution; larger assets are left to the static layer")
	engine := flag.String("engine", "javy", "JS engine: `javy` (QuickJS, wasi-stdio — small, no Web APIs) or `starlingmonkey` (SpiderMonkey wasi:http component via jco — full Web APIs + heavy React SSR)")
	flag.Parse()

	if *entry == "" {
		return fmt.Errorf("--entry is required")
	}
	if *mode != "handler" && *mode != "fetch" && *mode != "node" && *mode != "bun" && *mode != "deno" {
		return fmt.Errorf("--mode must be `handler`, `fetch`, `node`, `bun`, or `deno`")
	}
	if *engine != "javy" && *engine != "starlingmonkey" {
		return fmt.Errorf("--engine must be `javy` or `starlingmonkey`")
	}
	// node/bun/deno modes drive their handler from the wasi:http fetch event, which
	// only the component tier provides; and StarlingMonkey only runs event-driven
	// entries (fetch/node/bun/deno), never the wasi-stdio `handler` shape.
	if (*mode == "node" || *mode == "bun" || *mode == "deno") && *engine != "starlingmonkey" {
		return fmt.Errorf("--mode %s requires --engine starlingmonkey (the component tier provides the fetch/Web-API host the bridge runs on)", *mode)
	}
	if *engine == "starlingmonkey" && *mode != "fetch" && *mode != "node" && *mode != "bun" && *mode != "deno" {
		return fmt.Errorf("--engine starlingmonkey requires --mode fetch, node, bun, or deno")
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
	// node/bun modes are Node-compatible apps, which always want the Node-compat
	// surface (process/Buffer/events), so enable it implicitly there.
	nodeCompat := *node || *mode == "node" || *mode == "bun" || *mode == "deno"
	if nodeCompat {
		// Node-compat globals first (process/Buffer/global), so web.js and the app
		// can rely on them.
		p, err := writePolyfill("node.js", nodePolyfill)
		if err != nil {
			return err
		}
		prelude += fmt.Sprintf("import %q;\n", p)

		// Node builtin-module shims, aliased so `import ... from "node:async_hooks"`
		// (and the bare specifier) resolve during bundling. The extension picks the
		// module format esbuild infers: `.cjs` for CJS-authored shims (events,
		// whose module object must be the EventEmitter class) and `.mjs` otherwise.
		for _, mod := range []struct{ name, src, ext string }{
			{"async_hooks", nodeAsyncHooks, ".mjs"},
			{"events", nodeEvents, ".cjs"},
			{"buffer", nodeBuffer, ".mjs"},
		} {
			mp, err := writePolyfill(mod.name+mod.ext, mod.src)
			if err != nil {
				return err
			}
			aliases = append(aliases, "--alias:node:"+mod.name+"="+mp, "--alias:"+mod.name+"="+mp)
		}
	}

	bridge := fmt.Sprintf("import app from %q;\n", entryAbs)
	if *mode == "node" || *mode == "bun" || *mode == "deno" {
		// Node-compatible app (a Node HTTP server, or a Bun.serve/Deno.serve app —
		// both are node-compatible). Alias node:http/node:https (and the bare
		// specifiers) to the bridge module so the app's `http.createServer` is ours,
		// alias the rest of the node builtins to shims, and run the entry:
		// http.createServer + listen() (Express's app.listen()) or Bun.serve()/
		// Deno.serve() captures the handler as a side effect, which the wasi:http
		// fetch event then drives (serveNode / serveBun / serveDeno). A namespace
		// import tolerates an entry with no default export.
		nhp, err := writePolyfill("node-http.mjs", nodeHTTP)
		if err != nil {
			return err
		}

		// Run packages on their *browser* code path consistently. --platform=browser
		// selects browser builds, but some libs still gate browser-disabled, node-only
		// code on process.versions.node (e.g. iconv-lite loads its stream extensions,
		// which the browser build stubs out -> "not a function"). Clearing it — after
		// the node-compat globals load, before the app — keeps the bundle edge-shaped.
		// Imported ahead of the app so it runs first (ESM evaluates in import order).
		ep, err := writePolyfill("node-edge.mjs", "if (typeof process !== \"undefined\" && process.versions) { try { delete process.versions.node; } catch (e) {} }\n")
		if err != nil {
			return err
		}
		prelude += fmt.Sprintf("import %q;\n", ep)

		// Node builtin-module shims for the HTTP-server surface. http/https are the
		// bridge itself; the rest are pure-JS / Web-API-backed (path/stream/crypto/
		// …) with fs/net/zlib as loud stubs (no filesystem or raw sockets here).
		// async_hooks/events/buffer are already aliased above (nodeCompat).
		// `.cjs` where the module object must be a function/class Node consumers
		// require directly (stream -> the Stream class); `.mjs` for the rest.
		nodeBuiltins := []struct{ name, src, ext string }{
			{"http", nodeHTTP, ".mjs"}, {"https", nodeHTTP, ".mjs"},
			{"path", nodePath, ".mjs"}, {"querystring", nodeQuerystring, ".mjs"},
			{"string_decoder", nodeStringDecoder, ".mjs"}, {"url", nodeURL, ".mjs"},
			{"util", nodeUtil, ".mjs"}, {"stream", nodeStream, ".cjs"}, {"crypto", nodeCrypto, ".mjs"},
			{"fs", nodeFS, ".mjs"}, {"net", nodeNet, ".mjs"}, {"zlib", nodeZlib, ".mjs"},
			{"assert", nodeAssert, ".cjs"}, {"v8", nodeV8, ".mjs"}, {"os", nodeOS, ".mjs"},
			{"diagnostics_channel", nodeDiagnosticsChannel, ".mjs"}, {"dns", nodeDNS, ".mjs"},
			{"http2", nodeHTTP2, ".mjs"}, {"perf_hooks", nodePerfHooks, ".mjs"}, {"repl", nodeREPL, ".mjs"},
		}
		for _, b := range nodeBuiltins {
			p := nhp // http/https reuse the already-written bridge module
			if b.name != "http" && b.name != "https" {
				if p, err = writePolyfill(b.name+b.ext, b.src); err != nil {
					return err
				}
			}
			aliases = append(aliases, "--alias:node:"+b.name+"="+p, "--alias:"+b.name+"="+p)
		}

		// Resolve npm deps under the browser platform: it selects packages' browser
		// builds (which avoid node builtins) and honors main/module fields that
		// --platform=neutral skips. Appended last so it overrides the neutral
		// default in bundleJS.
		aliases = append(aliases, "--platform=browser")

		// If the app uses Fastify, alias it to a wrapper that defers its async boot
		// to the first request (it can't complete during the Wizer init snapshot —
		// see docs/framework-support.md). The wrapper imports the real Fastify by
		// absolute path so it isn't caught by this alias.
		if fastifyMain := resolveNodeModule(filepath.Dir(entryAbs), "fastify"); fastifyMain != "" {
			fp, err := writePolyfill("fastify-defer.mjs", fmt.Sprintf(fastifyDeferShim, fastifyMain))
			if err != nil {
				return err
			}
			aliases = append(aliases, "--alias:fastify="+fp)
			fmt.Fprintln(os.Stderr, "taubyte-ssr-adapter: detected Fastify; deferring its async boot to first request")
		}

		if *mode == "bun" || *mode == "deno" {
			// Bun/Deno app: Bun.serve({fetch}) / Deno.serve(handler) is a Web-standard
			// fetch handler. The runtime shim installs the global (`Bun`/`Deno`) whose
			// serve() captures the handler; serveBun/serveDeno drives it. The component
			// shim is imported only for its Web-API polyfills (Request clone-with-body
			// etc.) — serveComponent is unused here.
			runtimeSrc, runtimeName, serveFn := bunRuntime, "bun", "serveBun"
			if *mode == "deno" {
				runtimeSrc, runtimeName, serveFn = denoRuntime, "deno", "serveDeno"
			}
			csp, err := writePolyfill("component-shim.mjs", componentShim)
			if err != nil {
				return err
			}
			rsp, err := writePolyfill(runtimeName+".mjs", runtimeSrc)
			if err != nil {
				return err
			}
			// Bun apps can `import … from "bun"`; resolve it to the shim (harmless
			// for Deno, which only uses the global).
			aliases = append(aliases, "--alias:"+runtimeName+"="+rsp)
			prelude += fmt.Sprintf("import %q;\nimport %q;\n", csp, rsp)
			bridge = prelude +
				fmt.Sprintf("import %q;\n", entryAbs) +
				fmt.Sprintf("import { %s } from %q;\n%s();\n", serveFn, rsp, serveFn)
		} else {
			bridge = prelude +
				fmt.Sprintf("import * as __app from %q;\n", entryAbs) +
				fmt.Sprintf("import { serveNode } from %q;\nserveNode(__app);\n", nhp)
		}
	} else if *mode == "fetch" {
		// The Javy tier polyfills Web APIs (web.js); StarlingMonkey provides them
		// natively, so install web.js only for Javy.
		if *engine == "javy" {
			p, err := writePolyfill("web.js", webPolyfill)
			if err != nil {
				return err
			}
			prelude += fmt.Sprintf("import %q;\n", p)
		}

		// Edge adapters (SvelteKit/Next on Cloudflare) import the Workers runtime
		// virtual module; resolve it to a binding-less shim (both engines).
		cf, err := writePolyfill("cloudflare-workers.mjs", cloudflareWorkers)
		if err != nil {
			return err
		}
		aliases = append(aliases, "--alias:cloudflare:workers="+cf)

		// Embed text-like static/prerendered assets so env.ASSETS resolves them
		// in-process (the host bundle can't call back mid-render). Bounded by a
		// per-file cap; larger/binary assets are served by the static layer.
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

		if *engine == "starlingmonkey" {
			// Dispatch through the fetch-event bridge (wasi:http/proxy).
			csp, err := writePolyfill("component-shim.mjs", componentShim)
			if err != nil {
				return err
			}
			bridge = prelude + bridge + fmt.Sprintf("import { serveComponent } from %q;\nserveComponent(app);\n", csp)
		} else {
			bridge = prelude + bridge + fmt.Sprintf("import { serveFetch } from %q;\nserveFetch(app);\n", shimPath)
		}
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
	// StarlingMonkey (the shipped componentize-js build) parses regexes without
	// Unicode property tables, so rewrite any \p{...} escapes the bundle uses
	// (e.g. path-to-regexp v8 in Express 5 / @koa/router) into explicit classes.
	// No-op unless such escapes are present.
	if *engine == "starlingmonkey" {
		if err := downlevelUnicodeRegex(tmp, bundlePath); err != nil {
			return err
		}
	}
	if dst := os.Getenv("TAUBYTE_SSR_KEEP_BUNDLE"); dst != "" {
		if data, rerr := os.ReadFile(bundlePath); rerr == nil {
			_ = os.WriteFile(dst, data, 0o644)
		}
	}

	// 3. Compile the bundle to the engine's handler artifact.
	var handler []byte // the handler asset bytes (a wasm zip for Javy; a raw component for StarlingMonkey)
	manifest := buildManifest(*framework)
	if *engine == "starlingmonkey" {
		// StarlingMonkey (SpiderMonkey) -> a wasi:http/proxy component via jco.
		witDir, err := writeWIT(tmp)
		if err != nil {
			return err
		}
		compPath := filepath.Join(tmp, "handler.component.wasm")
		if err := componentizeJS(bundlePath, compPath, witDir); err != nil {
			return fmt.Errorf("componentize failed (is jco installed?): %w", err)
		}
		handler, err = os.ReadFile(compPath)
		if err != nil {
			return err
		}
		manifest = buildComponentManifest(*framework)
	} else {
		// Javy (QuickJS) -> a WASI-stdio module, packaged as main.wasm in a zip.
		wasmPath := filepath.Join(tmp, "module.wasm")
		eventLoop, err := javyBuild(bundlePath, wasmPath)
		if err != nil {
			return fmt.Errorf("javy build failed (is javy installed?): %w", err)
		}
		if !eventLoop {
			// The build succeeded but without the event loop, so QuickJS will trap
			// ("Pending jobs in the event queue") the moment a handler awaits. fetch
			// mode is always async, so refuse it; handler mode may be synchronous.
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
		if handler, err = buildHandlerZip(wasm); err != nil {
			return err
		}
	}

	// 4. Package and write outputs. By default --out is the handler artifact; with
	// --site it becomes a complete, deployable website build.zip (static assets +
	// handler + manifest), so prerendered pages serve from the static layer and
	// dynamic routes hit the bundle.
	outBytes, kind := handler, "handler ("+string(manifest.ABI)+")"
	if *site != "" {
		outBytes, err = buildSiteZip(*site, handler, manifest)
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
		data, err := manifest.Marshal()
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
	// Per-app esbuild escape hatch — e.g. `--external:<pkg>` for optional peer
	// dependencies a framework lazy-requires in a try/catch (Nest's
	// @nestjs/microservices, class-transformer, …), so the bundle resolves and the
	// framework treats them as absent at runtime.
	if v := strings.TrimSpace(os.Getenv("TAUBYTE_ESBUILD_ARGS")); v != "" {
		args = append(args, strings.Fields(v)...)
	}
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
