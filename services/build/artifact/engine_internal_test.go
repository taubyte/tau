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

// A FILE IS ITSELF. A wasm component is one file, and handing it over is an
// open.
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

// A DIRECTORY IS A TAR.GZ. A website is a tree, and the vocabulary lets an
// author name either — so the artifact's shape follows what was declared
// rather than forcing a site to be zipped by hand into a file the config would
// then have to name.
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

// A SYMLINK IS NOT FOLLOWED. A build controls what it writes, and one that
// wrote a link to /etc/passwd into its output directory would otherwise have
// this read that file into the artifact — which then goes to a
// content-addressed store anybody with the id can read.
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

// THE MOST COMMON WAY A BUILD FAILS WITHOUT FAILING: the commands succeeded and
// put the product somewhere else. Saying what was expected is the whole of what
// makes that findable.
func TestCollectWhenTheBuildLeftNothing(t *testing.T) {
	_, err := collect(t.TempDir(), "target/release/app.wasm")
	require.ErrorContains(t, err, "left nothing at")
	require.ErrorContains(t, err, "target/release/app.wasm")
}

// An output that climbs out collapses to the checkout root, which is a
// directory that exists — so what comes back is the checkout, not /etc.
func TestCollectCannotBeAimedOutside(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "only"), []byte("x"), 0o644))

	product, err := collect(dir, "../../../../etc")
	require.NoError(t, err)
	defer product.Close()

	files := untar(t, product)
	require.Equal(t, map[string]string{"only": "x"}, files)
}

// An empty output directory is an empty archive rather than an error: the
// build declared it and produced nothing, which is the build's answer.
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
