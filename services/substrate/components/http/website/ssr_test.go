package website

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	"github.com/taubyte/tau/services/substrate/components/http/common"
)

func buildAssetZip(t *testing.T, files map[string]string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func TestLoadManifestStatic(t *testing.T) {
	w := &Website{}
	zr := buildAssetZip(t, map[string]string{"index.html": "<html></html>"})

	if err := w.loadManifest(zr); err != nil {
		t.Fatal(err)
	}
	if w.isSSR() {
		t.Error("an asset without a manifest must stay static")
	}
}

func TestLoadManifestSSR(t *testing.T) {
	w := &Website{config: structureSpec.Website{Name: "blog"}}
	manifest := `{"render":"ssr","framework":"nextjs","handler":"__taubyte__/handler.wasm.zip","static":["/_next/static/"]}`
	zr := buildAssetZip(t, map[string]string{
		websiteSpec.ManifestPath:          manifest,
		websiteSpec.DefaultHandlerPath:    "WASM-ZIP-BYTES",
		"index.html":                      "x",
		"_next/static/chunks/main-abc.js": "console.log(1)",
	})

	if err := w.loadManifest(zr); err != nil {
		t.Fatal(err)
	}
	if !w.isSSR() {
		t.Fatal("expected SSR website")
	}
	if w.config.Render != websiteSpec.RenderSSR {
		t.Errorf("config.Render = %q, want ssr", w.config.Render)
	}
	if w.config.Framework != "nextjs" {
		t.Errorf("config.Framework = %q, want nextjs", w.config.Framework)
	}
	if string(w.ssrHandlerData) != "WASM-ZIP-BYTES" {
		t.Errorf("ssr handler bytes = %q", string(w.ssrHandlerData))
	}
	if w.ssr.Entry != websiteSpec.DefaultEntry {
		t.Errorf("expected default entry, got %q", w.ssr.Entry)
	}
}

func TestLoadManifestSSROverrides(t *testing.T) {
	w := &Website{config: structureSpec.Website{Entry: "render", SSRMemory: 64 << 20, SSRTimeout: 5}}
	zr := buildAssetZip(t, map[string]string{
		websiteSpec.ManifestPath:       `{"render":"ssr","handler":"__taubyte__/handler.wasm.zip"}`,
		websiteSpec.DefaultHandlerPath: "w",
	})

	if err := w.loadManifest(zr); err != nil {
		t.Fatal(err)
	}
	if w.ssr.Entry != "render" {
		t.Errorf("config entry override ignored: %q", w.ssr.Entry)
	}
	if w.ssr.Memory != 64<<20 {
		t.Errorf("config memory override ignored: %d", w.ssr.Memory)
	}
	if w.ssr.Timeout != 5 {
		t.Errorf("config timeout override ignored: %d", w.ssr.Timeout)
	}
}

func TestLoadManifestSSRMissingHandler(t *testing.T) {
	w := &Website{}
	zr := buildAssetZip(t, map[string]string{
		websiteSpec.ManifestPath: `{"render":"ssr","handler":"__taubyte__/handler.wasm.zip"}`,
	})

	if err := w.loadManifest(zr); err == nil {
		t.Error("expected error when the server bundle is missing from the asset")
	}
}

func TestIsStaticAsset(t *testing.T) {
	w := &Website{assetFiles: map[string]struct{}{
		"/index.html":      {},
		"/assets/app.js":   {},
		"/blog/index.html": {},
	}}

	for path, want := range map[string]bool{
		"/assets/app.js": true,
		"/index.html":    true,
		"/":              true, // -> /index.html
		"/blog/":         true, // -> /blog/index.html
		"/blog":          true, // -> /blog/index.html
		"/missing":       false,
		"/api/users":     false,
	} {
		if got := w.isStaticAsset(path); got != want {
			t.Errorf("isStaticAsset(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestResolveStaticAsset(t *testing.T) {
	w := &Website{assetFiles: map[string]struct{}{
		"/index.html":      {},
		"/assets/app.js":   {},
		"/blog/index.html": {},
	}}

	for _, tc := range []struct {
		path, wantFile string
		wantOK         bool
	}{
		{"/assets/app.js", "/assets/app.js", true}, // exact file
		{"/", "/index.html", true},                 // root -> index.html
		{"/blog", "/blog/index.html", true},        // clean URL -> directory index
		{"/blog/", "/blog/index.html", true},       // trailing slash -> directory index
		{"/missing", "", false},
	} {
		gotFile, gotOK := w.resolveStaticAsset(tc.path)
		if gotOK != tc.wantOK || gotFile != tc.wantFile {
			t.Errorf("resolveStaticAsset(%q) = (%q, %v), want (%q, %v)", tc.path, gotFile, gotOK, tc.wantFile, tc.wantOK)
		}
	}
}

func TestCleanRequestPath(t *testing.T) {
	for _, tc := range []struct {
		urlPath, pathMatch, want string
	}{
		{"/", "/", "/"},                     // root must stay "/", never "//"
		{"/foo", "/", "/foo"},               // simple
		{"/foo/", "/", "/foo/"},             // trailing slash preserved
		{"/blog/hello", "/", "/blog/hello"}, // nested
		{"/app/x", "/app", "/x"},            // sub-path mount
		{"/app", "/app", "/"},               // mount root -> "/"
		{"/app/", "/app", "/"},              // mount root with slash -> "/"
		{"/a//b", "/", "/a/b"},              // collapse double slashes
	} {
		if got := cleanRequestPath(tc.urlPath, tc.pathMatch); got != tc.want {
			t.Errorf("cleanRequestPath(%q, %q) = %q, want %q", tc.urlPath, tc.pathMatch, got, tc.want)
		}
	}
}

func TestEncodeStdioRequestForwardsHostAndProto(t *testing.T) {
	r := httptest.NewRequest("POST", "http://myapp.com/contact?x=1", bytes.NewReader([]byte("name=dylan")))
	r.Header.Set("Origin", "https://myapp.com")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	data, err := encodeStdioRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	var got stdioRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	// Host must be forwarded even though Go promotes it off r.Header onto r.Host;
	// the bundle reconstructs the request origin from it for CSRF checks.
	if got.Headers["Host"] != "myapp.com" {
		t.Errorf("Host header = %q, want %q", got.Headers["Host"], "myapp.com")
	}
	// Scheme is propagated so the reconstructed origin matches the Origin header.
	if got.Headers["X-Forwarded-Proto"] != "http" {
		t.Errorf("X-Forwarded-Proto = %q, want %q", got.Headers["X-Forwarded-Proto"], "http")
	}
	if got.Headers["Origin"] != "https://myapp.com" {
		t.Errorf("Origin header = %q, want forwarded as-is", got.Headers["Origin"])
	}
	if got.URL != "/contact?x=1" {
		t.Errorf("URL = %q, want path+query %q", got.URL, "/contact?x=1")
	}
	if got.Body != "name=dylan" {
		t.Errorf("Body = %q, want %q", got.Body, "name=dylan")
	}
}

func TestEncodeStdioRequestKeepsExistingProto(t *testing.T) {
	r := httptest.NewRequest("GET", "http://myapp.com/", nil)
	r.Header.Set("X-Forwarded-Proto", "https") // edge already set it
	data, err := encodeStdioRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	var got stdioRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Headers["X-Forwarded-Proto"] != "https" {
		t.Errorf("X-Forwarded-Proto = %q, want preserved %q", got.Headers["X-Forwarded-Proto"], "https")
	}
}

func TestMatchSSRClaimsAllMethods(t *testing.T) {
	ssr := &Website{config: structureSpec.Website{Paths: []string{"/"}, Render: websiteSpec.RenderSSR}}
	static := &Website{config: structureSpec.Website{Paths: []string{"/"}}}

	if ssr.Match(common.New("host", "/api/users", "POST")) == matcherSpec.NoMatch {
		t.Error("SSR website should match a POST to /api")
	}
	if static.Match(common.New("host", "/api/users", "POST")) != matcherSpec.NoMatch {
		t.Error("static website must not claim a POST request")
	}
	if static.Match(common.New("host", "/page", "GET")) == matcherSpec.NoMatch {
		t.Error("static website should still match GET requests")
	}
	if ssr.Match(common.New("host", "/page", "GET")) == matcherSpec.NoMatch {
		t.Error("SSR website should match GET requests")
	}
}
