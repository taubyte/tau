package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// downlevelScript is a tiny Node program that rewrites Unicode-property regex
// escapes (\p{...}) into explicit character classes via Babel's
// transform-unicode-property-regex plugin. Run as: node <script> <in> <out>
// with NODE_PATH pointing at a node_modules that has @babel/core + the plugin.
const downlevelScript = `const babel = require("@babel/core");
const fs = require("fs");
const [, , inF, outF] = process.argv;
const src = fs.readFileSync(inF, "utf8");
const out = babel.transformSync(src, {
  configFile: false, babelrc: false, compact: false, sourceType: "unambiguous",
  plugins: [require.resolve("@babel/plugin-transform-unicode-property-regex")],
}).code;
fs.writeFileSync(outF, out);
`

// downlevelUnicodeRegex rewrites Unicode-property regex escapes (\p{...}, \P{...})
// in the bundle into explicit character classes, so the StarlingMonkey build the
// componentizer ships — built without Unicode property tables — can parse them.
// path-to-regexp v8+ (Express 5, @koa/router) uses \p{ID_Start}/\p{ID_Continue}
// for route-param validation and otherwise traps the engine with "invalid class
// property name in regular expression".
//
// It no-ops unless the bundle actually uses such escapes, so the common case (and
// the validated Hono/Next/Express-4 paths) pays nothing. The transform runs via
// Babel, installed on demand into a reused cache dir. Escape hatches:
// TAUBYTE_SSR_NO_REGEX_DOWNLEVEL skips it; TAUBYTE_BABEL_DIR points at a dir that
// already has @babel/core + the plugin (no auto-install).
func downlevelUnicodeRegex(tmp, bundlePath string) error {
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		return err
	}
	if !bytes.Contains(data, []byte(`\p{`)) && !bytes.Contains(data, []byte(`\P{`)) {
		return nil // no Unicode-property escapes; nothing to downlevel
	}
	if os.Getenv("TAUBYTE_SSR_NO_REGEX_DOWNLEVEL") != "" {
		fmt.Fprintln(os.Stderr, `taubyte-ssr-adapter: WARNING: bundle uses \p{...} regex escapes the shipped engine can't parse, but TAUBYTE_SSR_NO_REGEX_DOWNLEVEL is set — componentize will likely fail`)
		return nil
	}

	node, err := exec.LookPath("node")
	if err != nil {
		return fmt.Errorf(`bundle uses \p{...} regex escapes the StarlingMonkey engine can't parse, but "node" is not on PATH to downlevel them (set TAUBYTE_BABEL_DIR to a dir with @babel/core + @babel/plugin-transform-unicode-property-regex, or TAUBYTE_SSR_NO_REGEX_DOWNLEVEL to skip): %w`, err)
	}

	babelDir, err := ensureBabel()
	if err != nil {
		return err
	}

	script := filepath.Join(tmp, "downlevel.cjs")
	if err := os.WriteFile(script, []byte(downlevelScript), 0o644); err != nil {
		return err
	}
	out := bundlePath + ".dl.js"
	cmd := exec.Command(node, script, bundlePath, out)
	cmd.Env = append(os.Environ(), "NODE_PATH="+filepath.Join(babelDir, "node_modules"))
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(`downleveling \p{...} regex escapes via Babel failed: %w`, err)
	}
	if err := os.Rename(out, bundlePath); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, `taubyte-ssr-adapter: downleveled \p{...} regex escapes for the StarlingMonkey engine`)
	return nil
}

// ensureBabel returns a directory whose node_modules has @babel/core + the
// transform-unicode-property-regex plugin, installing them on first use into a
// reused cache dir. TAUBYTE_BABEL_DIR overrides it (assumed pre-provisioned).
func ensureBabel() (string, error) {
	if dir := os.Getenv("TAUBYTE_BABEL_DIR"); dir != "" {
		return dir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "taubyte-ssr-adapter", "regex-downlevel")
	if fileExists(filepath.Join(dir, "node_modules", "@babel", "core", "package.json")) &&
		fileExists(filepath.Join(dir, "node_modules", "@babel", "plugin-transform-unicode-property-regex", "package.json")) {
		return dir, nil // already installed
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if !fileExists(filepath.Join(dir, "package.json")) {
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"taubyte-ssr-regex-downlevel","private":true}`), 0o644); err != nil {
			return "", err
		}
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		return "", fmt.Errorf(`"npm" not on PATH to install the regex-downlevel toolchain (set TAUBYTE_BABEL_DIR to a dir with @babel/core + @babel/plugin-transform-unicode-property-regex): %w`, err)
	}
	fmt.Fprintf(os.Stderr, "taubyte-ssr-adapter: installing the regex-downlevel toolchain (@babel/core + transform-unicode-property-regex) into %s (first use)\n", dir)
	cmd := exec.Command(npm, "install", "--no-audit", "--no-fund", "@babel/core", "@babel/plugin-transform-unicode-property-regex")
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("installing the regex-downlevel toolchain failed: %w", err)
	}
	return dir, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// fastifyDeferShim wraps Fastify so its async avvio boot is deferred from
// component-init to the first request. Fastify boots at app.listen()/app.ready(),
// but componentize-js's Wizer init snapshot doesn't preserve the pending job
// queue, so an init-time boot never completes. The wrapper intercepts listen()
// (the http server + route handler are already created in Fastify's constructor,
// so they're captured regardless) and registers app.ready() on
// __TAUBYTE_DEFER_READY, which the request bridge drives on the first request —
// in the real event loop, where the boot finishes. %FASTIFY_MAIN% is the app's
// resolved Fastify entry (absolute path, so it bypasses the "fastify" alias).
const fastifyDeferShim = `import RealFastify from %q;
function wrap(app) {
  if (!app || app.__taubyteDeferred) return app;
  app.__taubyteDeferred = true;
  app.listen = function (...args) {
    const cb = args.find((a) => typeof a === "function");
    (globalThis.__TAUBYTE_DEFER_READY = globalThis.__TAUBYTE_DEFER_READY || []).push(() => app.ready());
    if (cb) (typeof queueMicrotask !== "undefined" ? queueMicrotask : (f) => Promise.resolve().then(f))(() => { try { cb(null, "http://127.0.0.1"); } catch (e) {} });
    return Promise.resolve("http://127.0.0.1");
  };
  return app;
}
function Fastify(opts) { return wrap(RealFastify(opts)); }
for (const k of Object.keys(RealFastify)) { try { Fastify[k] = RealFastify[k]; } catch (e) {} }
Fastify.fastify = Fastify;
Fastify.default = Fastify;
export default Fastify;
export { Fastify as fastify };
`

// resolveNodeModule returns the absolute path Node would resolve `name` to from
// fromDir, or "" if not found / node unavailable.
func resolveNodeModule(fromDir, name string) string {
	node, err := exec.LookPath("node")
	if err != nil {
		return ""
	}
	script := fmt.Sprintf("try{process.stdout.write(require.resolve(%q,{paths:[%q]}))}catch(e){}", name, fromDir)
	out, err := exec.Command(node, "-e", script).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
