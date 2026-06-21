package nextjs

import (
	"os"
	"path/filepath"
	"testing"

	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

// writeNextBuild lays out a minimal but realistic .next build tree.
func writeNextBuild(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestTranslateNoBuild(t *testing.T) {
	if _, _, err := Translate(t.TempDir()); err == nil {
		t.Error("expected error when .next is absent")
	}
}

func TestTranslateBasic(t *testing.T) {
	dir := writeNextBuild(t, map[string]string{
		".next/routes-manifest.json": `{
			"version": 3,
			"basePath": "",
			"staticRoutes": [{"page": "/"}, {"page": "/about"}, {"page": "/api/health"}],
			"dynamicRoutes": [{"page": "/blog/[slug]"}, {"page": "/api/users/[id]"}]
		}`,
		".next/prerender-manifest.json": `{
			"version": 4,
			"routes": {"/about": {"initialRevalidateSeconds": false, "srcRoute": "/about"}},
			"dynamicRoutes": {"/blog/[slug]": {"routeRegex": "..."}}
		}`,
		".next/server/middleware-manifest.json": `{
			"version": 2,
			"sortedMiddleware": ["/"],
			"middleware": {"/": {"matchers": [{"regexp": "^/(?!api).*$"}], "page": "/"}},
			"functions": {}
		}`,
	})

	m, rep, err := Translate(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Report
	if rep.BasePath != "" {
		t.Errorf("basePath = %q", rep.BasePath)
	}
	if !rep.HasMiddleware || len(rep.MiddlewareMatchers) != 1 {
		t.Errorf("middleware not detected: %+v", rep)
	}
	if !contains(rep.PrerenderedRoutes, "/about") {
		t.Errorf("expected /about prerendered, got %v", rep.PrerenderedRoutes)
	}
	if !contains(rep.APIRoutes, "/api/health") || !contains(rep.APIRoutes, "/api/users/[id]") {
		t.Errorf("api routes wrong: %v", rep.APIRoutes)
	}
	if !contains(rep.DynamicRoutes, "/blog/[slug]") {
		t.Errorf("dynamic routes wrong: %v", rep.DynamicRoutes)
	}

	// Manifest must be a valid wasi-stdio SSR manifest.
	data, _ := m.Marshal()
	parsed, err := websiteSpec.ParseManifest(data)
	if err != nil {
		t.Fatalf("manifest invalid: %v", err)
	}
	if parsed.ABIOrDefault() != websiteSpec.ABIWasiStdio {
		t.Errorf("abi = %q", parsed.ABIOrDefault())
	}

	// Classification: assets static, api -> api, prerendered page -> static,
	// dynamic page + root -> ssr.
	for path, want := range map[string]websiteSpec.RouteType{
		"/_next/static/chunks/main.js": websiteSpec.RouteStatic,
		"/api/health":                  websiteSpec.RouteAPI,
		"/about":                       websiteSpec.RouteStatic,
		"/blog/anything":               websiteSpec.RouteSSR,
		"/":                            websiteSpec.RouteSSR,
	} {
		if got := parsed.Classify(path); got != want {
			t.Errorf("Classify(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestTranslateBasePath(t *testing.T) {
	dir := writeNextBuild(t, map[string]string{
		".next/routes-manifest.json": `{"version":3,"basePath":"/app","staticRoutes":[{"page":"/"}],"dynamicRoutes":[]}`,
	})

	m, rep, err := Translate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.BasePath != "/app" {
		t.Errorf("basePath = %q", rep.BasePath)
	}
	if m.Classify("/app/_next/static/x.js") != websiteSpec.RouteStatic {
		t.Error("basePath static prefix not applied")
	}
	if m.Classify("/app/api/x") != websiteSpec.RouteAPI {
		t.Error("basePath api route not applied")
	}
}

func TestTranslateAppRouter(t *testing.T) {
	// App Router app with route handlers + pages, no Pages-Router routes-manifest
	// staticRoutes/dynamicRoutes for them (only app-path-routes-manifest).
	dir := writeNextBuild(t, map[string]string{
		".next/routes-manifest.json": `{"version":3,"basePath":"","staticRoutes":[],"dynamicRoutes":[]}`,
		".next/app-path-routes-manifest.json": `{
			"/page": "/",
			"/ui/login/page": "/ui/login",
			"/api/health/route": "/api/health",
			"/api/wf/webhook/[token]/route": "/api/wf/webhook/[token]",
			"/blog/[slug]/page": "/blog/[slug]"
		}`,
	})

	_, rep, err := Translate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(rep.APIRoutes, "/api/health") || !contains(rep.APIRoutes, "/api/wf/webhook/[token]") {
		t.Errorf("app-router api routes not detected: %v", rep.APIRoutes)
	}
	if !contains(rep.DynamicRoutes, "/blog/[slug]") {
		t.Errorf("app-router dynamic page not detected: %v", rep.DynamicRoutes)
	}
}

func TestTranslateAppRouterOnly(t *testing.T) {
	// Only the App Router manifest present (no routes-manifest.json) must work.
	dir := writeNextBuild(t, map[string]string{
		".next/app-path-routes-manifest.json": `{"/page":"/","/api/x/route":"/api/x"}`,
	})
	_, rep, err := Translate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(rep.APIRoutes, "/api/x") {
		t.Errorf("api route missing: %v", rep.APIRoutes)
	}
}

func TestTranslateNoManifests(t *testing.T) {
	// .next exists but has no route manifests (a dev/incomplete build).
	dir := writeNextBuild(t, map[string]string{
		".next/build-manifest.json": `{"pages":{}}`,
	})
	if _, _, err := Translate(dir); err == nil {
		t.Error("expected a graceful error for a build without route manifests")
	}
}

func TestTranslateMinimal(t *testing.T) {
	// Only routes-manifest (no prerender/middleware) must still work.
	dir := writeNextBuild(t, map[string]string{
		".next/routes-manifest.json": `{"version":3,"basePath":"","staticRoutes":[{"page":"/"}],"dynamicRoutes":[]}`,
	})
	m, rep, err := Translate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.HasMiddleware {
		t.Error("should report no middleware")
	}
	if !m.IsSSR() {
		t.Error("expected ssr manifest")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
