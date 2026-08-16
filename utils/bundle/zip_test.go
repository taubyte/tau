package bundle

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An archive must carry the files in the directory it was given and nothing
// else. The directories these archive are produced by a build, so a name inside
// one may point anywhere on the machine, and archiving runs outside whatever
// confined the build.
func TestZipDirArchivesRegularFilesOnly(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "elsewhere")
	require.NoError(t, os.WriteFile(outside, []byte("not part of the build"), 0o644))

	source := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(source, "index.html"), []byte("hello"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(source, "link")))

	target := filepath.Join(t.TempDir(), "out.zip")
	file, err := Zip(ZipDir, source, target)
	require.NoError(t, err)
	file.Close()

	archive, err := zip.OpenReader(target)
	require.NoError(t, err)
	defer archive.Close()

	names := make([]string, 0, len(archive.File))
	for _, entry := range archive.File {
		names = append(names, entry.Name)
		assert.NotEqual(t, "link", entry.Name, "a link must not be archived")
	}
	assert.Contains(t, names, "index.html", "the real file must be archived")
}

func TestZipFileRefusesALink(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "elsewhere")
	require.NoError(t, os.WriteFile(outside, []byte("not part of the build"), 0o644))

	source := filepath.Join(t.TempDir(), "artifact.wasm")
	require.NoError(t, os.Symlink(outside, source))

	_, err := Zip(ZipFile, source, filepath.Join(t.TempDir(), "out.zip"), "artifact.wasm")
	require.Error(t, err, "a link must not be archived")
	assert.Contains(t, err.Error(), "not a regular file")
}

func TestZipFileArchivesARegularFile(t *testing.T) {
	source := filepath.Join(t.TempDir(), "artifact.wasm")
	require.NoError(t, os.WriteFile(source, []byte("wasm"), 0o644))

	target := filepath.Join(t.TempDir(), "out.zip")
	file, err := Zip(ZipFile, source, target, "artifact.wasm")
	require.NoError(t, err)
	file.Close()

	archive, err := zip.OpenReader(target)
	require.NoError(t, err)
	defer archive.Close()

	require.Len(t, archive.File, 1)
	assert.Equal(t, "artifact.wasm", archive.File[0].Name)
}
