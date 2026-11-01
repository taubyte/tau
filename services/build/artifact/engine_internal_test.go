package artifact

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Collecting what a build left behind needs no daemon, and it is where the
// interesting cases are.

func TestCollectAFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "target", "release"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "target", "release", "app.wasm"),
		[]byte("\x00asm\x01\x00\x00\x00"), 0o644))

	product, err := collect(dir, "target/release/app.wasm")
	require.NoError(t, err)
	defer product.Close()

	body, err := io.ReadAll(product)
	require.NoError(t, err)
	require.Equal(t, "\x00asm\x01\x00\x00\x00", string(body))
}

func TestCollectADirectory(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "dist")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>shop</h1>"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "assets", "app.js"), []byte("console.log(1)"), 0o644))

	product, err := collect(dir, "dist")
	require.NoError(t, err)
	defer product.Close()

	files := untar(t, product)
	require.Equal(t, "<h1>shop</h1>", files["index.html"])
	require.Equal(t, "console.log(1)", files["assets/app.js"])

	// The paths are relative to the output directory and use forward slashes,
	// so an artifact built on one platform unpacks on another.
	for name := range files {
		require.False(t, filepath.IsAbs(name), name)
	}
}

func TestCollectDoesNotFollowASymlink(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "dist")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "index.html"), []byte("ok"), 0o644))

	secret := filepath.Join(dir, "secret")
	require.NoError(t, os.WriteFile(secret, []byte("the password"), 0o600))
	require.NoError(t, os.Symlink(secret, filepath.Join(root, "leak")))

	product, err := collect(dir, "dist")
	require.NoError(t, err)
	defer product.Close()

	files := untar(t, product)
	require.Contains(t, files, "index.html")
	require.NotContains(t, files, "leak")
	for _, content := range files {
		require.NotContains(t, content, "the password")
	}
}

func TestCollectWhenTheBuildLeftNothing(t *testing.T) {
	_, err := collect(t.TempDir(), "target/release/app.wasm")
	require.ErrorContains(t, err, "left nothing at")
	require.ErrorContains(t, err, "target/release/app.wasm")
}

func TestCollectCannotBeAimedOutside(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "only"), []byte("x"), 0o644))

	product, err := collect(dir, "../../../../etc")
	require.NoError(t, err)
	defer product.Close()

	files := untar(t, product)
	require.Equal(t, map[string]string{"only": "x"}, files)
}

func TestCollectAnEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "dist"), 0o755))

	product, err := collect(dir, "dist")
	require.NoError(t, err)
	defer product.Close()

	require.Empty(t, untar(t, product))
}

func untar(t *testing.T, r io.Reader) map[string]string {
	t.Helper()

	gz, err := gzip.NewReader(r)
	require.NoError(t, err)
	defer gz.Close()

	out := map[string]string{}
	archive := tar.NewReader(gz)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			return out
		}
		require.NoError(t, err)
		if header.Typeflag == tar.TypeDir {
			continue
		}
		body, err := io.ReadAll(archive)
		require.NoError(t, err)
		out[header.Name] = string(body)
	}
}
