package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// assetContentTypes are the text-like asset extensions worth embedding so the
// bundle's env.ASSETS can resolve them in-process. Binary assets (images, fonts)
// are intentionally excluded: they are large, the substrate's static layer
// serves them directly, and SvelteKit's server-side read() targets text assets.
var assetContentTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".json":        "application/json; charset=utf-8",
	".svg":         "image/svg+xml",
	".txt":         "text/plain; charset=utf-8",
	".xml":         "application/xml; charset=utf-8",
	".webmanifest": "application/manifest+json",
}

// defaultAssetEmbedMax bounds an individual embedded asset. Anything larger is
// left to the substrate's static layer so the wasm module stays small.
const defaultAssetEmbedMax = 100 << 10 // 100 KiB

// buildAssetModule scans siteDir and emits a JS module that installs the
// text-like assets (each at or below maxBytes) on globalThis.__TAUBYTE_ASSETS__,
// keyed by site-root path. The shim's env.ASSETS.fetch resolves against this map
// so a bundle run standalone can serve its own prerendered pages, and SvelteKit's
// read() of a server asset returns real bytes instead of a 404. It returns the
// module source and the number of assets embedded. Host control files (handled
// by buildSiteZip's exclusion list) are skipped here too.
func buildAssetModule(siteDir string, maxBytes int64) (string, int, error) {
	type entry struct{ key, body, ctype string }
	var entries []entry

	err := filepath.WalkDir(siteDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(siteDir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if isSiteControlPath(rel) {
			return nil
		}
		ctype, ok := assetContentTypes[strings.ToLower(path.Ext(rel))]
		if !ok {
			return nil // not a text-like asset; leave it to the static layer
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxBytes {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		// Serve under the flat path and the clean-URL form (foo.html -> /foo too),
		// matching how the static layer resolves prerendered pages.
		for _, k := range siteAssetKeys(rel) {
			entries = append(entries, entry{key: "/" + k, body: string(data), ctype: ctype})
		}
		return nil
	})
	if err != nil {
		return "", 0, err
	}

	// Deterministic order for reproducible bundles.
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })

	var b strings.Builder
	b.WriteString("(function(g){var A=g.__TAUBYTE_ASSETS__||(g.__TAUBYTE_ASSETS__={});\n")
	for _, e := range entries {
		body, err := json.Marshal(e.body)
		if err != nil {
			return "", 0, err
		}
		key, _ := json.Marshal(e.key)
		ctype, _ := json.Marshal(e.ctype)
		b.WriteString(fmt.Sprintf("A[%s]={body:%s,type:%s};\n", key, body, ctype))
	}
	b.WriteString("})(globalThis);\n")
	return b.String(), len(entries), nil
}
