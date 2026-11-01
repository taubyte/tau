package artifact

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/js"
	"github.com/taubyte/tau/pkg/invoke/js/archive"
	"github.com/taubyte/tau/pkg/specs"
)

const echoBundle = `export default { async fetch(request) { return new Response(request.method + " " + await request.text()); } };
//# sourceMappingURL=worker.js.map
`

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
}

func packed(t *testing.T, dir, output string) []byte {
	t.Helper()
	product, err := product(dir, Spec{Runtime: specs.WorkloadBlockRuntimeJs, Output: output})
	require.NoError(t, err)
	defer product.Close()
	body, err := io.ReadAll(product)
	require.NoError(t, err)
	return body
}

func members(t *testing.T, packed []byte) map[string]string {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(packed), int64(len(packed)))
	require.NoError(t, err)
	out := map[string]string{}
	for _, f := range z.File {
		rc, err := f.Open()
		require.NoError(t, err)
		body, err := io.ReadAll(rc)
		require.NoError(t, err)
		rc.Close()
		out[f.Name] = string(body)
	}
	return out
}

type held []byte

func (h held) Get(context.Context, string) (io.ReadSeekCloser, error) {
	return readSeekCloser{bytes.NewReader(h)}, nil
}

type readSeekCloser struct{ *bytes.Reader }

func (readSeekCloser) Close() error { return nil }

func TestAJSBundleIsPackedIntoTheArchiveTheTierRuns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dist/worker.js", echoBundle)
	writeFile(t, dir, "dist/worker.js.map", `{"version":3,"sources":["../src/index.js"]}`)
	writeFile(t, dir, "dist/stale.js", "an earlier build's leftovers")

	archived := packed(t, dir, "dist/worker.js")
	got := members(t, archived)
	require.Len(t, got, 3)
	require.Equal(t, echoBundle, got["worker.js"])
	require.Contains(t, got["worker.js.map"], `"version":3`)

	var manifest archive.Manifest
	require.NoError(t, json.Unmarshal([]byte(got[archive.ManifestName]), &manifest))
	require.Equal(t, archive.Format, manifest.Format)
	require.Equal(t, "worker.js", manifest.Entry)

	require.Equal(t, archived, packed(t, dir, "dist/worker.js"), "one build packs to one digest")

	tier, err := js.New(held(archived))
	require.NoError(t, err)
	instance, err := tier.Start(t.Context(), invoke.Spec{Name: "shop/prod/function/echo", Runtime: js.Name, Artifact: "bafyarchive"})
	require.NoError(t, err)
	t.Cleanup(func() { instance.Close() })
	res, err := instance.Serve(t.Context(), &invoke.Request{Method: "POST", Path: "/", Body: []byte("ping")})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)
	require.Equal(t, "POST ping", string(res.Body))
}

func TestABundleWithNoSourceMapIsPackedAlone(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "worker.mjs", echoBundle)

	got := members(t, packed(t, dir, "worker.mjs"))
	require.Len(t, got, 2)
	require.Contains(t, got, "worker.mjs")
	require.Contains(t, got, archive.ManifestName)
}

func TestOnlyAJSProductIsPacked(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "app.wasm", "\x00asm")

	for _, runtime := range []string{"", specs.WorkloadBlockRuntimeWasm} {
		product, err := product(dir, Spec{Runtime: runtime, Output: "app.wasm"})
		require.NoError(t, err)
		body, err := io.ReadAll(product)
		require.NoError(t, err)
		product.Close()
		require.Equal(t, "\x00asm", string(body))
	}
}

func TestWhatAJSBuildCannotBePackedFrom(t *testing.T) {
	cases := map[string]struct {
		prepare func(t *testing.T, dir string)
		output  string
		says    string
	}{
		"nothing at the output": {
			func(*testing.T, string) {}, "dist/worker.js", `the build left nothing at "dist/worker.js"`,
		},
		"a directory at the output": {
			func(t *testing.T, dir string) { writeFile(t, dir, "dist/worker.js", "") },
			"dist", `left a directory at "dist"`,
		},
		"a directory where the source map goes": {
			func(t *testing.T, dir string) {
				writeFile(t, dir, "worker.js", echoBundle)
				writeFile(t, dir, "worker.js.map/inside", "")
			},
			"worker.js", `left a directory at "worker.js.map"`,
		},
		"a link at the output": {
			func(t *testing.T, dir string) {
				writeFile(t, dir, "elsewhere.js", echoBundle)
				require.NoError(t, os.Symlink(filepath.Join(dir, "elsewhere.js"), filepath.Join(dir, "worker.js")))
			},
			"worker.js", `left "worker.js" as a link`,
		},
		"a link where the source map goes": {
			func(t *testing.T, dir string) {
				writeFile(t, dir, "worker.js", echoBundle)
				require.NoError(t, os.Symlink("/etc/hostname", filepath.Join(dir, "worker.js.map")))
			},
			"worker.js", `left "worker.js.map" as a link`,
		},
		"a bundle larger than the tier reads": {
			func(t *testing.T, dir string) {
				writeFile(t, dir, "worker.js", "")
				require.NoError(t, os.Truncate(filepath.Join(dir, "worker.js"), archive.DefaultMax+1))
			},
			"worker.js", `"worker.js" makes the archive larger`,
		},
		"a source map that makes it larger": {
			func(t *testing.T, dir string) {
				writeFile(t, dir, "worker.js", echoBundle)
				writeFile(t, dir, "worker.js.map", "")
				require.NoError(t, os.Truncate(filepath.Join(dir, "worker.js.map"), archive.DefaultMax-int64(len(echoBundle))+1))
			},
			"worker.js", `"worker.js.map" makes the archive larger`,
		},
		"a path through a file": {
			func(t *testing.T, dir string) { writeFile(t, dir, "dist", "a file, not a directory") },
			"dist/worker.js", `reading "dist/worker.js"`,
		},
		"a bundle named as the manifest": {
			func(t *testing.T, dir string) { writeFile(t, dir, archive.ManifestName, echoBundle) },
			archive.ManifestName, `packing "manifest.json"`,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			c.prepare(t, dir)
			_, err := product(dir, Spec{Runtime: specs.WorkloadBlockRuntimeJs, Output: c.output})
			require.ErrorContains(t, err, c.says)
		})
	}
}
