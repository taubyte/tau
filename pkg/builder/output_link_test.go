package builder

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/core/builders"
	spec "github.com/taubyte/tau/pkg/specs/builders"
	"github.com/taubyte/tau/pkg/specs/builders/common"
	"github.com/taubyte/tau/pkg/specs/builders/wasm"
	"gotest.tools/v3/assert"
)

// A build controls its source directory, so a link it leaves where the archive
// used to be written must never redirect that write onto the host.
func TestCompressDoesNotFollowALinkInTheSourceDir(t *testing.T) {
	cases := []struct {
		name   string
		method builders.CompressionMethod
		output string
		link   func(wd spec.Dir) string
	}{
		{"website", builders.Website, "index.html", func(wd spec.Dir) string { return wd.Website().BuildZip() }},
		{"wasm", builders.WASM, wasm.WasmFile, func(wd spec.Dir) string { return wd.Wasm().Zip() }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workDir := t.TempDir()
			assert.NilError(t, os.Mkdir(filepath.Join(workDir, ".taubyte"), 0755))
			wd, err := common.Wd(workDir)
			assert.NilError(t, err)

			canary := filepath.Join(t.TempDir(), "authorized_keys")
			assert.NilError(t, os.WriteFile(canary, []byte("original"), 0600))
			if err := os.Symlink(canary, tc.link(wd)); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}

			outDir := t.TempDir()
			assert.NilError(t, os.WriteFile(filepath.Join(outDir, tc.output), []byte("built"), 0644))

			o := &output{wd: wd, outDir: outDir}
			zipped, err := o.Compress(tc.method)
			assert.NilError(t, err)

			body, err := io.ReadAll(zipped)
			assert.NilError(t, err)
			assert.Assert(t, len(body) > 0)
			assert.NilError(t, zipped.Close())

			got, err := os.ReadFile(canary)
			assert.NilError(t, err)
			assert.Equal(t, string(got), "original")
		})
	}
}

// Closing the archive removes it and the build's output directory: nothing else
// does, and a node builds all day.
func TestCompressedArchiveCleansUpOnClose(t *testing.T) {
	workDir := t.TempDir()
	assert.NilError(t, os.Mkdir(filepath.Join(workDir, ".taubyte"), 0755))
	wd, err := common.Wd(workDir)
	assert.NilError(t, err)

	outDir, err := os.MkdirTemp("", "out-*")
	assert.NilError(t, err)
	t.Cleanup(func() { os.RemoveAll(outDir) })
	assert.NilError(t, os.WriteFile(filepath.Join(outDir, "index.html"), []byte("built"), 0644))

	o := &output{wd: wd, outDir: outDir}
	zipped, err := o.Compress(builders.Website)
	assert.NilError(t, err)

	named, ok := zipped.(interface{ Name() string })
	assert.Assert(t, ok, "archive should expose its file name")
	archive := named.Name()

	assert.NilError(t, zipped.Close())

	_, err = os.Stat(archive)
	assert.Assert(t, os.IsNotExist(err), "archive still on disk: %v", err)
	_, err = os.Stat(outDir)
	assert.Assert(t, os.IsNotExist(err), "output directory still on disk: %v", err)
}
