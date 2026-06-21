package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// nopDynamicImport matches a dynamic `import(` whose argument is NOT a string
// literal — i.e. `import(someVar)` / `import(expr)` but not `import('node:fs')`.
// next-on-pages loads route and cache modules via `import(runtimeStringPath)`,
// which Javy's single-module model can't resolve; these are redirected to a
// static registry, while literal imports (node:buffer/async_hooks) are left for
// esbuild to bundle through the adapter's aliases. Go's RE2 has no lookahead, so
// the first non-quote char is captured and re-emitted via ${1}.
var nopDynamicImport = regexp.MustCompile("\\bimport\\(\\s*([^'\"`\\s)])")

// prepareNextOnPages detects a @cloudflare/next-on-pages worker — an index.js
// inside a `_worker.js` directory with a sibling `__next-on-pages-dist__` — and
// rewrites it into a single self-contained entry module: every per-route
// (`*.func.js`) and cache module the worker pulls in via dynamic import is
// statically imported into a registry, and the worker's dynamic imports are
// replaced with a registry lookup. It returns the path to the new entry to
// bundle in place of index.js, or ("", nil) when entryAbs is not such a worker.
func prepareNextOnPages(entryAbs, tmp string) (string, error) {
	dir := filepath.Dir(entryAbs)
	distDir := filepath.Join(dir, "__next-on-pages-dist__")
	if info, err := os.Stat(distDir); err != nil || !info.IsDir() {
		return "", nil // not a next-on-pages worker
	}

	// Collect the modules the worker imports dynamically: per-route functions and
	// the suspense-cache adaptors. Keys are the paths the worker passes to
	// import(), relative to the worker dir.
	type mod struct{ key, abs string }
	var mods []mod
	err := filepath.WalkDir(distDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".js") {
			return err
		}
		if !strings.Contains(p, string(filepath.Separator)+"functions"+string(filepath.Separator)) &&
			!strings.Contains(p, string(filepath.Separator)+"cache"+string(filepath.Separator)) {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		mods = append(mods, mod{key: filepath.ToSlash(rel), abs: p})
		return nil
	})
	if err != nil {
		return "", err
	}

	// Rewrite the worker's dynamic imports to a global registry lookup; literal
	// node:* imports are untouched so esbuild still bundles them.
	src, err := os.ReadFile(entryAbs)
	if err != nil {
		return "", err
	}
	rewritten := nopDynamicImport.ReplaceAll(src, []byte("__nopImport(${1}"))
	workerPath := filepath.Join(tmp, "nop-worker.mjs")
	if err := os.WriteFile(workerPath, rewritten, 0o644); err != nil {
		return "", err
	}

	// next-on-pages wraps each route module so that, at module-evaluation time, it
	// grabs an isolated global view via globalThis.__nextOnPagesRoutesIsolation
	// .getProxyFor(routeId). The worker only installs that inside fetch() (it
	// expects routes to be dynamically imported per request); since the registry
	// evaluates them eagerly at init, install a faithful equivalent first.
	isoPath := filepath.Join(tmp, "nop-isolation.mjs")
	if err := os.WriteFile(isoPath, []byte(nopIsolationSetup), 0o644); err != nil {
		return "", err
	}

	// Entry: install isolation, build the registry from static imports, define
	// __nopImport, then run the rewritten worker. esbuild bundles every registered
	// module (and its transitive webpack/manifest deps) into the single output.
	var b strings.Builder
	b.WriteString(fmt.Sprintf("import %q;\n", isoPath))
	b.WriteString("const __NOP__ = {};\n")
	for i, m := range mods {
		b.WriteString(fmt.Sprintf("import * as nop%d from %q;\n", i, m.abs))
		b.WriteString(fmt.Sprintf("__NOP__[%q] = nop%d;\n", m.key, i))
	}
	b.WriteString("globalThis.__nopImport = function (p) {\n")
	b.WriteString("  p = String(p).replace(/^\\.\\//, \"\");\n")
	b.WriteString("  const m = __NOP__[p];\n")
	b.WriteString("  return m ? Promise.resolve(m) : Promise.reject(new Error(\"module not found: \" + p));\n")
	b.WriteString("};\n")
	// Re-export the worker's default (its `export default { fetch }`) so the
	// adapter bridge can dispatch through it.
	b.WriteString(fmt.Sprintf("import __nopWorker from %q;\nexport default __nopWorker;\n", workerPath))

	entryPath := filepath.Join(tmp, "nop-entry.mjs")
	if err := os.WriteFile(entryPath, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return entryPath, nil
}

// nopIsolationSetup installs globalThis.__nextOnPagesRoutesIsolation before the
// route modules evaluate. It mirrors next-on-pages' own implementation: each
// route gets a Proxy over globalThis that isolates writes (except a small
// passthrough set) into a per-route store, so modules sharing globals don't
// clobber each other.
const nopIsolationSetup = `
globalThis.__nextOnPagesRoutesIsolation = globalThis.__nextOnPagesRoutesIsolation || (function () {
  var passthrough = new Set(["_nextOriginalFetch", "fetch", "__incrementalCache"]);
  var map = new Map();
  function makeProxy() {
    var store = new Map();
    return new Proxy(globalThis, {
      get: function (t, r) { return store.has(r) ? store.get(r) : Reflect.get(globalThis, r); },
      set: function (t, r, s) {
        if (passthrough.has(r)) return Reflect.set(globalThis, r, s);
        store.set(r, s);
        return true;
      },
    });
  }
  return {
    _map: map,
    getProxyFor: function (id) {
      var p = map.get(id);
      if (!p) { p = makeProxy(); map.set(id, p); }
      return p;
    },
  };
})();
`
