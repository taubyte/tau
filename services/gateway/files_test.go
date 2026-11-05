package gateway

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/site"
	"github.com/taubyte/tau/pkg/specs"
)

// The fixture's website is storefront, at a generated name, serving shop_site in spa
// mode. Its build is shopArtifact, holding shopSite.
const shopArtifact = "bafyshopsite"

var shopSite = map[string]string{
	"index.html":    "<h1>storefront</h1>",
	"assets/app.js": "console.log(1)",
}

// tarball is what services/plm/artifact stores for a directory output.
func tarball(t testing.TB, files map[string]string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	archive := tar.NewWriter(gz)
	for name, body := range files {
		require.NoError(t, archive.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o644, Size: int64(len(body))}))
		_, err := archive.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, archive.Close())
	require.NoError(t, gz.Close())
	return out.Bytes()
}

// builtFiles is a build that produced shopArtifact for every package, and the store
// holding it.
type builtFiles struct {
	mu        sync.Mutex
	artifacts map[string][]byte
	built     string // the artifact Artifact answers; empty means shopArtifact
	unbuilt   error
	gets      int
}

func (b *builtFiles) Artifact(_ context.Context, project, environment, kind, id string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.unbuilt != nil {
		return "", b.unbuilt
	}
	if b.built != "" {
		return b.built, nil
	}
	return shopArtifact, nil
}

func (b *builtFiles) Read(_ context.Context, id string) (io.ReadCloser, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.gets++
	content, ok := b.artifacts[id]
	if !ok {
		return nil, errors.New("no provider for that cid")
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

// storefront is the website's generated name, under the given cloud.
func storefront(t *testing.T, cloud string) string {
	t.Helper()
	p, err := specs.Load(compiled(t))
	require.NoError(t, err)
	for _, host := range p.Hosts() {
		if host.Generated && strings.HasPrefix(host.Fqdn, "storefront.") {
			return host.Fqdn + "." + cloud
		}
	}
	t.Fatal("the fixture's website answers on no generated name")
	return ""
}

func siteServing() site.Serving { return site.Serving{} }

func TestAWebsiteIsItsPackagesFilesServedByTheGateway(t *testing.T) {
	h := newHarness(t, Config{CloudFqdn: "tau.example.io"})
	name := storefront(t, "tau.example.io")

	w := h.do(t, http.MethodGet, "http://"+name+"/", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "<h1>storefront</h1>", w.Body.String())
	require.Contains(t, w.Header().Get("Content-Type"), "text/html")

	w = h.do(t, http.MethodGet, "http://"+name+"/assets/app.js", "")
	require.Equal(t, "console.log(1)", w.Body.String())

	// SPA, as the website says: the page's own router renders the route.
	w = h.do(t, http.MethodGet, "http://"+name+"/orders/42", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "<h1>storefront</h1>", w.Body.String())

	w = h.do(t, http.MethodPost, "http://"+name+"/", "")
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)

	h.Workers.mu.Lock()
	require.Empty(t, h.Workers.calls, "nothing was run to read a file")
	h.Workers.mu.Unlock()
	require.Equal(t, 1, h.Files.gets, "the artifact is unpacked once, not per request")
}

func TestAPackageThatHasNotBeenBuiltIsSaidSo(t *testing.T) {
	h := newHarness(t, Config{CloudFqdn: "tau.example.io"})
	h.Files.unbuilt = errors.New("nothing has built it yet")

	w := h.do(t, http.MethodGet, "http://"+storefront(t, "tau.example.io")+"/", "")
	require.NotEqual(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "has not been built")
}

func TestAnArtifactThatCannotBeReadIsSaidSo(t *testing.T) {
	h := newHarness(t, Config{CloudFqdn: "tau.example.io"})
	h.Files.built = "bafygone"

	w := h.do(t, http.MethodGet, "http://"+storefront(t, "tau.example.io")+"/", "")
	require.NotEqual(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "could not be read")

	// A failure is not remembered: the next request asks again.
	h.Files.built = ""
	w = h.do(t, http.MethodGet, "http://"+storefront(t, "tau.example.io")+"/", "")
	require.Equal(t, http.StatusOK, w.Code)
}

func TestAGatewayGivenNoFilesRefusesAPackageRoute(t *testing.T) {
	h := newHarness(t, Config{CloudFqdn: "tau.example.io"})
	bare, err := New(Config{CloudFqdn: "tau.example.io"}, WithHosts(h.Reg), WithSource(h.Reg), WithWorkers(h.Workers))
	require.NoError(t, err)
	h.Gateway = bare

	w := h.do(t, http.MethodGet, "http://"+storefront(t, "tau.example.io")+"/", "")
	require.Equal(t, http.StatusNotImplemented, w.Code)
	require.Contains(t, w.Body.String(), "nothing to read them from")
}

func TestUnpackedFilesStayWithinTheirBudget(t *testing.T) {
	u := newUnpacked(int64(len("aaaa")))
	t.Cleanup(func() { u.close() })
	store := &builtFiles{artifacts: map[string][]byte{
		"one": tarball(t, map[string]string{"a": "aaaa"}),
		"two": tarball(t, map[string]string{"b": "bbbb"}),
	}}
	open := func(id string) func() (io.ReadCloser, error) {
		return func() (io.ReadCloser, error) { return store.Read(t.Context(), id) }
	}

	first, release, err := u.acquire(t.Context(), "one", open("one"))
	require.NoError(t, err)
	held := first.Serve(siteServing(), http.MethodGet, "/a")
	require.Equal(t, "aaaa", string(held.Body))

	// Over budget while "one" is still being read: it is dropped from the table, and
	// its files stay until that request ends.
	_, releaseTwo, err := u.acquire(t.Context(), "two", open("two"))
	require.NoError(t, err)
	require.Equal(t, "aaaa", string(first.Serve(siteServing(), http.MethodGet, "/a").Body), "a request in flight keeps its files")
	release()
	releaseTwo()
	require.Equal(t, http.StatusNotFound, first.Serve(siteServing(), http.MethodGet, "/a").Status, "and they go when it ends")

	_, release, err = u.acquire(t.Context(), "one", open("one"))
	require.NoError(t, err)
	release()
	require.Equal(t, 3, store.gets, "an evicted artifact is unpacked again")

	require.NoError(t, u.close())
	_, err = os.Stat(u.root)
	require.True(t, os.IsNotExist(err))
}

func TestAWaitThatEndsLeavesTheArtifactToOthers(t *testing.T) {
	u := newUnpacked(DefaultFilesBudget)
	t.Cleanup(func() { u.close() })
	gate := make(chan struct{})
	store := &builtFiles{artifacts: map[string][]byte{"one": tarball(t, map[string]string{"a": "aaaa"})}}
	opened := make(chan struct{})
	go func() {
		_, release, err := u.acquire(context.Background(), "one", func() (io.ReadCloser, error) {
			close(opened)
			<-gate
			return store.Read(t.Context(), "one")
		})
		if err == nil {
			release()
		}
	}()
	<-opened

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err := u.acquire(ctx, "one", nil)
	require.ErrorIs(t, err, context.Canceled)
	close(gate)

	f, release, err := u.acquire(t.Context(), "one", nil)
	require.NoError(t, err)
	defer release()
	require.Equal(t, "aaaa", string(f.Serve(siteServing(), http.MethodGet, "/a").Body))
}

func TestFilesNeedBothTheLookupAndTheStore(t *testing.T) {
	h := newHarness(t, Config{})
	_, err := New(Config{}, WithHosts(h.Reg), WithSource(h.Reg), WithWorkers(h.Workers), WithFiles(nil, h.Files))
	require.ErrorContains(t, err, "both an artifact lookup and an asset store")
	_, err = New(Config{}, WithHosts(h.Reg), WithSource(h.Reg), WithWorkers(h.Workers), WithFiles(h.Files, nil))
	require.Error(t, err)
}

func TestAnArtifactThatIsNotAnArchiveIsRefusedAndNotKept(t *testing.T) {
	h := newHarness(t, Config{CloudFqdn: "tau.example.io"})
	h.Files.artifacts["bafyjunk"] = []byte("not a gzip")
	h.Files.built = "bafyjunk"

	w := h.do(t, http.MethodGet, "http://"+storefront(t, "tau.example.io")+"/", "")
	require.NotEqual(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "could not be read")
	require.Empty(t, h.Gateway.files.held, "a failed unpack is not remembered")
}
