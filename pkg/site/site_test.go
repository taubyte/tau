package site_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/site"
)

// tarball is what services/plm/artifact stores for a directory output.
func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	archive := tar.NewWriter(gz)

	for name, body := range files {
		require.NoError(t, archive.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
		}))
		_, err := archive.Write([]byte(body))
		require.NoError(t, err)
	}

	require.NoError(t, archive.Close())
	require.NoError(t, gz.Close())
	return out.Bytes()
}

func unpack(t *testing.T, content []byte) (*site.Files, error) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "site")
	files, err := site.Unpack(bytes.NewReader(content), root, site.MaxSize)
	if err == nil {
		t.Cleanup(func() { files.Close() })
	}
	return files, err
}

func files(t *testing.T, contents map[string]string) *site.Files {
	t.Helper()
	f, err := unpack(t, tarball(t, contents))
	require.NoError(t, err)
	return f
}

func get(f *site.Files, s site.Serving, path string) *invoke.Response {
	return f.Serve(s, http.MethodGet, path)
}

func TestFilesServeThemselves(t *testing.T) {
	f := files(t, map[string]string{
		"index.html":     "<h1>shop</h1>",
		"assets/app.js":  "console.log(1)",
		"assets/app.css": "body{}",
	})

	res := get(f, site.Serving{}, "/index.html")
	require.Equal(t, http.StatusOK, res.Status)
	require.Equal(t, "<h1>shop</h1>", string(res.Body))
	require.Contains(t, res.Headers["Content-Type"][0], "text/html")

	// The type is the extension's, never sniffed.
	res = get(f, site.Serving{}, "/assets/app.js")
	require.Equal(t, http.StatusOK, res.Status)
	require.Contains(t, res.Headers["Content-Type"][0], "javascript")
	require.Equal(t, []string{"nosniff"}, res.Headers["X-Content-Type-Options"])

	// A DIRECTORY IS ITS INDEX.
	for _, path := range []string{"/", ""} {
		res = get(f, site.Serving{}, path)
		require.Equal(t, http.StatusOK, res.Status, path)
		require.Equal(t, "<h1>shop</h1>", string(res.Body), path)
	}
	require.EqualValues(t, len("<h1>shop</h1>")+len("console.log(1)")+len("body{}"), f.Size())
}

func TestADirectoryIsNotAListing(t *testing.T) {
	f := files(t, map[string]string{
		"index.html":        "<h1>shop</h1>",
		"private/notes.txt": "internal",
		"private/keys.pem":  "-----BEGIN",
	})

	res := get(f, site.Serving{}, "/private")
	require.Equal(t, http.StatusNotFound, res.Status)
	require.NotContains(t, string(res.Body), "notes.txt")
	require.NotContains(t, string(res.Body), "keys.pem")
}

func TestSPAModeAnswersAnUnknownPathWithTheIndex(t *testing.T) {
	f := files(t, map[string]string{"index.html": "<div id=app></div>", "404.html": "<h1>gone</h1>"})

	res := get(f, site.Serving{Mode: site.ModeSPA}, "/orders/42")
	require.Equal(t, http.StatusOK, res.Status, "the page's own router renders it")
	require.Equal(t, "<div id=app></div>", string(res.Body))

	// AND STATIC MODE DOES NOT, which is the whole reason the author says which.
	res = get(f, site.Serving{Mode: site.ModeStatic, NotFound: "404.html"}, "/orders/42")
	require.Equal(t, http.StatusNotFound, res.Status)
	require.Equal(t, "<h1>gone</h1>", string(res.Body))

	// The default is static, and with no not-found document it is a bare 404.
	res = get(f, site.Serving{}, "/orders/42")
	require.Equal(t, http.StatusNotFound, res.Status)
	require.Empty(t, res.Body)
}

func TestACustomIndex(t *testing.T) {
	f := files(t, map[string]string{"home.html": "<h1>home</h1>"})

	require.Equal(t, "<h1>home</h1>", string(get(f, site.Serving{Index: "home.html"}, "/").Body))
	require.Equal(t, "<h1>home</h1>", string(get(f, site.Serving{Mode: site.ModeSPA, Index: "home.html"}, "/anything").Body))
}

func TestARequestCannotClimbOutOfTheFiles(t *testing.T) {
	f := files(t, map[string]string{"index.html": "<h1>shop</h1>"})

	for _, path := range []string{
		"/../../../../etc/passwd",
		"../../etc/passwd",
		"/assets/../../../etc/passwd",
		"//etc/passwd",
		"/./../etc/passwd",
	} {
		res := get(f, site.Serving{}, path)
		require.Equal(t, http.StatusNotFound, res.Status, path)
		require.NotContains(t, string(res.Body), "root:", path)
	}
}

func TestFilesAnswerReads(t *testing.T) {
	f := files(t, map[string]string{"index.html": "<h1>shop</h1>"})

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		res := f.Serve(site.Serving{Mode: site.ModeSPA}, method, "/")
		require.Equal(t, http.StatusMethodNotAllowed, res.Status, method)
		require.Equal(t, []string{"GET, HEAD"}, res.Headers["Allow"])
	}

	// A HEAD carries the headers and the length, without the body.
	res := f.Serve(site.Serving{}, http.MethodHead, "/")
	require.Equal(t, http.StatusOK, res.Status)
	require.Empty(t, res.Body)
	require.Equal(t, []string{"13"}, res.Headers["Content-Length"])
}

func TestAnArchiveThatWritesOutsideItsRootIsRefused(t *testing.T) {
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	archive := tar.NewWriter(gz)
	body := "owned"
	escaped := filepath.Join(t.TempDir(), "escaped")
	require.NoError(t, archive.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "../../../../" + escaped, Mode: 0o644, Size: int64(len(body)),
	}))
	_, err := archive.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	require.NoError(t, gz.Close())

	_, err = unpack(t, out.Bytes())
	require.ErrorContains(t, err, "climbs out of the artifact")
	require.NoFileExists(t, escaped)
}

func TestASymlinkIsNotUnpacked(t *testing.T) {
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	archive := tar.NewWriter(gz)
	require.NoError(t, archive.WriteHeader(&tar.Header{
		Typeflag: tar.TypeSymlink, Name: "secrets", Linkname: "/etc/passwd", Mode: 0o777,
	}))
	index := "<h1>shop</h1>"
	require.NoError(t, archive.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "index.html", Mode: 0o644, Size: int64(len(index)),
	}))
	_, err := archive.Write([]byte(index))
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	require.NoError(t, gz.Close())

	f, err := unpack(t, out.Bytes())
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, get(f, site.Serving{}, "/secrets").Status)
	require.Equal(t, http.StatusOK, get(f, site.Serving{}, "/").Status)
}

func TestClosingRemovesTheFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "site")
	f, err := site.Unpack(bytes.NewReader(tarball(t, map[string]string{"index.html": "one"})), root, site.MaxSize)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "index.html"))
	require.NoError(t, f.Close())
	_, err = os.Stat(root)
	require.True(t, os.IsNotExist(err))
}

func TestBytesThatAreNotAnArchiveAreRefused(t *testing.T) {
	_, err := unpack(t, []byte("not a gzip"))
	require.Error(t, err)
}

func TestAnArtifactLargerThanItsBoundIsRefused(t *testing.T) {
	_, err := site.Unpack(bytes.NewReader(tarball(t, map[string]string{"a": "12345", "b": "67890"})), filepath.Join(t.TempDir(), "s"), 8)
	require.ErrorIs(t, err, site.ErrTooLarge)

	// The copy is what is bounded, so a truncated large body stands in for gigabytes.
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	archive := tar.NewWriter(gz)
	require.NoError(t, archive.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "huge.bin", Mode: 0o644, Size: 4 << 30,
	}))
	_, err = archive.Write(bytes.Repeat([]byte("x"), 1<<20))
	require.NoError(t, err)
	gz.Close()
	_, err = unpack(t, out.Bytes())
	require.Error(t, err, "an artifact that does not end is not a site")
}

func TestDirectoriesInTheArchive(t *testing.T) {
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	archive := tar.NewWriter(gz)
	require.NoError(t, archive.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: "assets", Mode: 0o755}))
	body := "console.log(1)"
	require.NoError(t, archive.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "assets/app.js", Mode: 0o644, Size: int64(len(body)),
	}))
	_, err := archive.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	require.NoError(t, gz.Close())

	f, err := unpack(t, out.Bytes())
	require.NoError(t, err)
	require.Equal(t, body, string(get(f, site.Serving{}, "/assets/app.js").Body))
}

func TestAnUnknownExtensionIsNotGuessed(t *testing.T) {
	f := files(t, map[string]string{
		"index.html":   "<h1>shop</h1>",
		"data.unknown": "<script>alert(1)</script>",
	})

	res := get(f, site.Serving{}, "/data.unknown")
	require.Equal(t, http.StatusOK, res.Status)
	require.Equal(t, "application/octet-stream", res.Headers["Content-Type"][0])
}

func TestANotFoundDocumentThatIsNotThere(t *testing.T) {
	f := files(t, map[string]string{"index.html": "<h1>shop</h1>"})

	res := get(f, site.Serving{NotFound: "404.html"}, "/nope")
	require.Equal(t, http.StatusNotFound, res.Status)
	require.Empty(t, res.Body)
}
