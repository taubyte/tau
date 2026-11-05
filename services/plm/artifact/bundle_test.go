package artifact_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/plm/artifact"
)

var aDigest = strings.Repeat("a", 64)

func TestABundleRoundTrips(t *testing.T) {
	tree := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tree, "services", "hello"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tree, "project.yaml"), []byte("id: x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tree, "services", "hello", "run.sh"), []byte("echo"), 0o755))
	product := filepath.Join(t.TempDir(), "hello.wasm")
	require.NoError(t, os.WriteFile(product, []byte("wasm"), 0o644))

	var packed bytes.Buffer
	require.NoError(t, artifact.PackBundle(&packed, tree, []string{"services/hello/run.sh", "project.yaml"}, map[string]string{aDigest: product}))

	b, err := artifact.UnpackBundle(&packed, t.TempDir())
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(b.Tree, "project.yaml"))
	require.NoError(t, err)
	require.Equal(t, "id: x", string(got))
	info, err := os.Stat(filepath.Join(b.Tree, "services", "hello", "run.sh"))
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&0o100, "a script stays runnable")

	r, ok := b.Product(aDigest)
	require.True(t, ok)
	body, err := io.ReadAll(r)
	r.Close()
	require.NoError(t, err)
	require.Equal(t, "wasm", string(body))

	_, ok = b.Product(strings.Repeat("b", 64))
	require.False(t, ok, "a recipe nobody built has no product")
}

func TestPackingRefusesWhatIsNotABundle(t *testing.T) {
	require.ErrorIs(t, artifact.PackBundle(io.Discard, t.TempDir(), nil, map[string]string{"short": "x"}), artifact.ErrBadBundle)
	require.Error(t, artifact.PackBundle(io.Discard, t.TempDir(), []string{"missing"}, nil))
	require.Error(t, artifact.PackBundle(io.Discard, t.TempDir(), nil, map[string]string{aDigest: "/no/such/product"}))

	// A link in the tree is not carried: it would name something on the
	// developer's machine.
	tree := t.TempDir()
	require.NoError(t, os.Symlink("/etc/passwd", filepath.Join(tree, "link")))
	var packed bytes.Buffer
	require.NoError(t, artifact.PackBundle(&packed, tree, []string{"link"}, nil))
	b, err := artifact.UnpackBundle(&packed, t.TempDir())
	require.NoError(t, err)
	require.NoFileExists(t, filepath.Join(b.Tree, "link"))
}

func tarball(t *testing.T, headers ...tar.Header) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	archive := tar.NewWriter(gz)
	for _, h := range headers {
		h.Size = int64(len("x"))
		if h.Typeflag != tar.TypeReg {
			h.Size = 0
		}
		require.NoError(t, archive.WriteHeader(&h))
		if h.Size > 0 {
			archive.Write([]byte("x"))
		}
	}
	require.NoError(t, archive.Close())
	require.NoError(t, gz.Close())
	return &buf
}

func TestUnpackingRefusesWhatIsNotABundle(t *testing.T) {
	for name, r := range map[string]io.Reader{
		"not gzip":       strings.NewReader("plain"),
		"not tar":        gzipped(t, "plain text that is no archive"),
		"a climb":        tarball(t, tar.Header{Name: "tree/../../etc/passwd", Typeflag: tar.TypeReg, Mode: 0o644}),
		"the tree":       tarball(t, tar.Header{Name: "tree/.", Typeflag: tar.TypeReg, Mode: 0o644}),
		"a link":         tarball(t, tar.Header{Name: "tree/link", Typeflag: tar.TypeSymlink, Linkname: "/etc"}),
		"a bad digest":   tarball(t, tar.Header{Name: "products/abc", Typeflag: tar.TypeReg, Mode: 0o644}),
		"somewhere else": tarball(t, tar.Header{Name: "elsewhere", Typeflag: tar.TypeReg, Mode: 0o644}),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := artifact.UnpackBundle(r, t.TempDir())
			require.ErrorIs(t, err, artifact.ErrBadBundle)
		})
	}
}

func gzipped(t *testing.T, body string) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write([]byte(body))
	require.NoError(t, gz.Close())
	return &buf
}
