package static_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"

	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/static"
)

// tarball is what services/build/artifact stores for a directory output.
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

// store is an asset store holding one artifact.
type store struct {
	content []byte
	err     error
	gets    int
}

func (s *store) Get(_ context.Context, id string) (io.ReadSeekCloser, error) {
	s.gets++
	if s.err != nil {
		return nil, s.err
	}
	return nopSeekCloser{bytes.NewReader(s.content)}, nil
}

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }

func serve(t *testing.T, files map[string]string, settings map[string]string) invoke.Instance {
	t.Helper()

	r, err := static.New(&store{content: tarball(t, files)})
	require.NoError(t, err)

	instance, err := r.Start(t.Context(), invoke.Spec{
		Runtime: static.Name, Name: "shop/prod/website/home", Revision: "rev1",
		Artifact: "bafysite", Settings: settings,
	})
	require.NoError(t, err)
	t.Cleanup(func() { instance.Close() })
	return instance
}

func get(t *testing.T, instance invoke.Instance, path string) *invoke.Response {
	t.Helper()
	res, err := instance.Serve(t.Context(), &invoke.Request{Method: http.MethodGet, Path: path})
	require.NoError(t, err)
	return res
}

func TestASiteServesItsFiles(t *testing.T) {
	instance := serve(t, map[string]string{
		"index.html":     "<h1>shop</h1>",
		"assets/app.js":  "console.log(1)",
		"assets/app.css": "body{}",
	}, nil)

	res := get(t, instance, "/index.html")
	require.Equal(t, http.StatusOK, res.Status)
	require.Equal(t, "<h1>shop</h1>", string(res.Body))
	require.Contains(t, res.Headers["Content-Type"][0], "text/html")

	// The type is the extension's, never sniffed.
	res = get(t, instance, "/assets/app.js")
	require.Equal(t, http.StatusOK, res.Status)
	require.Contains(t, res.Headers["Content-Type"][0], "javascript")
	require.Equal(t, []string{"nosniff"}, res.Headers["X-Content-Type-Options"])

	// A DIRECTORY IS ITS INDEX.
	for _, path := range []string{"/", ""} {
		res = get(t, instance, path)
		require.Equal(t, http.StatusOK, res.Status, path)
		require.Equal(t, "<h1>shop</h1>", string(res.Body), path)
	}
}

func TestADirectoryIsNotAListing(t *testing.T) {
	instance := serve(t, map[string]string{
		"index.html":        "<h1>shop</h1>",
		"private/notes.txt": "internal",
		"private/keys.pem":  "-----BEGIN",
	}, nil)

	res := get(t, instance, "/private")
	require.Equal(t, http.StatusNotFound, res.Status)
	require.NotContains(t, string(res.Body), "notes.txt")
	require.NotContains(t, string(res.Body), "keys.pem")
}

func TestSPAModeAnswersAnUnknownPathWithTheIndex(t *testing.T) {
	files := map[string]string{"index.html": "<div id=app></div>", "404.html": "<h1>gone</h1>"}

	spa := serve(t, files, map[string]string{static.SettingMode: static.ModeSPA})
	res := get(t, spa, "/orders/42")
	require.Equal(t, http.StatusOK, res.Status, "the page's own router renders it")
	require.Equal(t, "<div id=app></div>", string(res.Body))

	// AND STATIC MODE DOES NOT, which is the whole reason the author says which.
	site := serve(t, files, map[string]string{
		static.SettingMode:     static.ModeStatic,
		static.SettingNotFound: "404.html",
	})
	res = get(t, site, "/orders/42")
	require.Equal(t, http.StatusNotFound, res.Status)
	require.Equal(t, "<h1>gone</h1>", string(res.Body))

	// The default is static, and with no not-found document it is a bare 404.
	bare := serve(t, files, nil)
	res = get(t, bare, "/orders/42")
	require.Equal(t, http.StatusNotFound, res.Status)
	require.Empty(t, res.Body)
}

func TestACustomIndex(t *testing.T) {
	files := map[string]string{"home.html": "<h1>home</h1>"}

	instance := serve(t, files, map[string]string{static.SettingIndex: "home.html"})
	require.Equal(t, "<h1>home</h1>", string(get(t, instance, "/").Body))

	spa := serve(t, files, map[string]string{
		static.SettingMode:  static.ModeSPA,
		static.SettingIndex: "home.html",
	})
	require.Equal(t, "<h1>home</h1>", string(get(t, spa, "/anything").Body))
}

func TestARequestCannotClimbOutOfTheSite(t *testing.T) {
	instance := serve(t, map[string]string{"index.html": "<h1>shop</h1>"}, nil)

	for _, path := range []string{
		"/../../../../etc/passwd",
		"../../etc/passwd",
		"/assets/../../../etc/passwd",
		"//etc/passwd",
		"/./../etc/passwd",
	} {
		res := get(t, instance, path)
		require.Equal(t, http.StatusNotFound, res.Status, path)
		require.NotContains(t, string(res.Body), "root:", path)
	}
}

func TestASiteAnswersReads(t *testing.T) {
	instance := serve(t, map[string]string{"index.html": "<h1>shop</h1>"},
		map[string]string{static.SettingMode: static.ModeSPA})

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		res, err := instance.Serve(t.Context(), &invoke.Request{Method: method, Path: "/"})
		require.NoError(t, err)
		require.Equal(t, http.StatusMethodNotAllowed, res.Status, method)
		require.Equal(t, []string{"GET, HEAD"}, res.Headers["Allow"])
	}

	// A HEAD carries the headers and the length, without the body.
	res, err := instance.Serve(t.Context(), &invoke.Request{Method: http.MethodHead, Path: "/"})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.Status)
	require.Empty(t, res.Body)
	require.Equal(t, []string{"13"}, res.Headers["Content-Length"])
}

func TestAnArchiveThatWritesOutsideItsRootIsRefused(t *testing.T) {
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	archive := tar.NewWriter(gz)
	body := "owned"
	require.NoError(t, archive.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "../../../../tmp/tau-escaped", Mode: 0o644, Size: int64(len(body)),
	}))
	_, err := archive.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	require.NoError(t, gz.Close())

	r, err := static.New(&store{content: out.Bytes()})
	require.NoError(t, err)

	_, err = r.Start(t.Context(), invoke.Spec{Artifact: "bafyevil"})
	require.ErrorContains(t, err, "climbs out of the site")
	require.NoFileExists(t, "/tmp/tau-escaped")
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

	r, err := static.New(&store{content: out.Bytes()})
	require.NoError(t, err)

	instance, err := r.Start(t.Context(), invoke.Spec{Artifact: "bafysite"})
	require.NoError(t, err)
	defer instance.Close()

	require.Equal(t, http.StatusNotFound, get(t, instance, "/secrets").Status)
	require.Equal(t, http.StatusOK, get(t, instance, "/").Status)
}

func TestAnInstanceOwnsItsDirectory(t *testing.T) {
	r, err := static.New(&store{content: tarball(t, map[string]string{"index.html": "one"})})
	require.NoError(t, err)

	first, err := r.Start(t.Context(), invoke.Spec{Artifact: "bafyone", Revision: "rev1"})
	require.NoError(t, err)
	second, err := r.Start(t.Context(), invoke.Spec{Artifact: "bafyone", Revision: "rev2"})
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, get(t, first, "/").Status)
	require.NoError(t, first.Close())

	// Closing one does not take the other's files.
	require.Equal(t, http.StatusOK, get(t, second, "/").Status)
	require.NoError(t, second.Close())
}

func TestWhatItRefuses(t *testing.T) {
	_, err := static.New(nil)
	require.ErrorIs(t, err, static.ErrNoAssets)

	r, err := static.New(&store{content: tarball(t, map[string]string{"index.html": "x"})})
	require.NoError(t, err)
	require.Equal(t, static.Name, r.Name())

	_, err = r.Start(t.Context(), invoke.Spec{Name: "shop/prod/website/home"})
	require.ErrorIs(t, err, static.ErrNoArtifact)

	// An artifact the store cannot fetch is a cold start that failed, named.
	missing, err := static.New(&store{err: errors.New("no provider for that cid")})
	require.NoError(t, err)
	_, err = missing.Start(t.Context(), invoke.Spec{Artifact: "bafygone"})
	require.ErrorContains(t, err, "no provider for that cid")

	// And bytes that are not an archive.
	notATar, err := static.New(&store{content: []byte("not a gzip")})
	require.NoError(t, err)
	_, err = notATar.Start(t.Context(), invoke.Spec{Artifact: "bafyjunk"})
	require.ErrorContains(t, err, "unpacking")
}

func TestItIsAPoolTier(t *testing.T) {
	r, err := static.New(&store{content: tarball(t, map[string]string{"index.html": "<h1>shop</h1>"})})
	require.NoError(t, err)

	pool, err := invoke.NewPool(invoke.WithRuntime(r))
	require.NoError(t, err)
	defer pool.Close()

	spec := invoke.Spec{
		Runtime: static.Name, Name: "shop/prod/website/home", Revision: "rev1", Artifact: "bafysite",
	}
	res, err := pool.Do(t.Context(), spec, &invoke.Request{Method: http.MethodGet, Path: "/"})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.Status)
	require.Equal(t, "<h1>shop</h1>", string(res.Body))

	// A SECOND REQUEST IS WARM: the artifact is fetched on the cold start and
	// not per request.
	require.Equal(t, 1, pool.Warm(spec))
	_, err = pool.Do(t.Context(), spec, &invoke.Request{Method: http.MethodGet, Path: "/"})
	require.NoError(t, err)
	require.Equal(t, 1, pool.Warm(spec))
}

func TestASiteLargerThanTheCeilingIsRefused(t *testing.T) {
	// The copy is what is bounded, so a truncated large body stands in for two
	// gigabytes.
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	archive := tar.NewWriter(gz)
	require.NoError(t, archive.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "huge.bin", Mode: 0o644, Size: 4 << 30,
	}))
	// Not the whole of it: the reader stops at the ceiling long before here.
	_, err := archive.Write(bytes.Repeat([]byte("x"), 1<<20))
	require.NoError(t, err)
	gz.Close()

	r, err := static.New(&store{content: out.Bytes()})
	require.NoError(t, err)

	_, err = r.Start(t.Context(), invoke.Spec{Artifact: "bafyhuge"})
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

	r, err := static.New(&store{content: out.Bytes()})
	require.NoError(t, err)
	instance, err := r.Start(t.Context(), invoke.Spec{Artifact: "bafysite"})
	require.NoError(t, err)
	defer instance.Close()

	require.Equal(t, body, string(get(t, instance, "/assets/app.js").Body))
}

func TestAnUnknownExtensionIsNotGuessed(t *testing.T) {
	instance := serve(t, map[string]string{
		"index.html":   "<h1>shop</h1>",
		"data.unknown": "<script>alert(1)</script>",
	}, nil)

	res := get(t, instance, "/data.unknown")
	require.Equal(t, http.StatusOK, res.Status)
	require.Equal(t, "application/octet-stream", res.Headers["Content-Type"][0])
}

func TestAnEmptySettingIsTheDefault(t *testing.T) {
	instance := serve(t, map[string]string{"index.html": "<h1>shop</h1>"}, map[string]string{
		static.SettingIndex: "",
		static.SettingMode:  "",
	})
	require.Equal(t, http.StatusOK, get(t, instance, "/").Status)
}

func TestANotFoundDocumentThatIsNotThere(t *testing.T) {
	instance := serve(t, map[string]string{"index.html": "<h1>shop</h1>"},
		map[string]string{static.SettingNotFound: "404.html"})

	res := get(t, instance, "/nope")
	require.Equal(t, http.StatusNotFound, res.Status)
	require.Empty(t, res.Body)
}
