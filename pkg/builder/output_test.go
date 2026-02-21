package builder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taubyte/tau/core/builders"
	"github.com/taubyte/tau/pkg/specs/builders/common"
	"gotest.tools/v3/assert"
)

func TestCompressWebsite_EmptyOutDir(t *testing.T) {
	workDir := t.TempDir()
	err := os.Mkdir(filepath.Join(workDir, ".taubyte"), 0755)
	assert.NilError(t, err)

	wd, err := common.Wd(workDir)
	assert.NilError(t, err)

	emptyOutDir := t.TempDir()
	o := &output{wd: wd, outDir: emptyOutDir}

	_, err = o.Compress(builders.Website)
	assert.Assert(t, err != nil, "expected error when output directory is empty")
	assert.Check(t, strings.Contains(err.Error(), "output directory is empty"))
}

func TestCompressWebsite_OutDirWithOnlySubdirs(t *testing.T) {
	workDir := t.TempDir()
	err := os.Mkdir(filepath.Join(workDir, ".taubyte"), 0755)
	assert.NilError(t, err)

	wd, err := common.Wd(workDir)
	assert.NilError(t, err)

	outDir := t.TempDir()
	err = os.Mkdir(filepath.Join(outDir, "subdir"), 0755)
	assert.NilError(t, err)

	o := &output{wd: wd, outDir: outDir}

	_, err = o.Compress(builders.Website)
	assert.Assert(t, err != nil, "expected error when output has only subdirs and no files")
	assert.Check(t, strings.Contains(err.Error(), "output directory is empty"))
}

func TestCompressWebsite_NonEmptyOutDir(t *testing.T) {
	workDir := t.TempDir()
	err := os.Mkdir(filepath.Join(workDir, ".taubyte"), 0755)
	assert.NilError(t, err)

	wd, err := common.Wd(workDir)
	assert.NilError(t, err)

	outDir := t.TempDir()
	err = os.WriteFile(filepath.Join(outDir, "index.html"), []byte("hello"), 0644)
	assert.NilError(t, err)

	o := &output{wd: wd, outDir: outDir}

	rsc, err := o.Compress(builders.Website)
	assert.NilError(t, err)
	assert.Assert(t, rsc != nil, "expected non-nil ReadSeekCloser")
	defer rsc.Close()

	zipPath := wd.Website().BuildZip()
	_, err = os.Stat(zipPath)
	assert.NilError(t, err)
}
