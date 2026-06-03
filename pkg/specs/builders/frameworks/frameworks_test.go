package frameworks

import (
	"os"
	"path/filepath"
	"testing"

	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

func pkg(deps ...string) *PackageJSON {
	p := &PackageJSON{Dependencies: map[string]string{}}
	for _, d := range deps {
		p.Dependencies[d] = "*"
	}
	return p
}

func TestDetect(t *testing.T) {
	for _, tc := range []struct {
		name string
		deps []string
		want string
	}{
		{"next over react", []string{"next", "react", "react-dom"}, "nextjs"},
		{"nuxt over vue", []string{"nuxt", "vue"}, "nuxt"},
		{"sveltekit over svelte", []string{"@sveltejs/kit", "svelte"}, "sveltekit"},
		{"remix", []string{"@remix-run/react"}, "remix"},
		{"solidstart over solid", []string{"@solidjs/start", "solid-js"}, "solidstart"},
		{"vite react spa", []string{"vite", "react"}, "vite"},
		{"create-react-app", []string{"react-scripts", "react"}, "react"},
		{"vue cli", []string{"@vue/cli-service", "vue"}, "vue"},
		{"angular", []string{"@angular/core"}, "angular"},
		{"astro", []string{"astro"}, "astro"},
		{"gatsby", []string{"gatsby", "react"}, "gatsby"},
		{"express", []string{"express"}, "express"},
		{"nestjs over express", []string{"@nestjs/core", "express"}, "nestjs"},
		{"fastify", []string{"fastify"}, "fastify"},
		{"plain react fallback", []string{"react", "react-dom"}, "react"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := Detect(pkg(tc.deps...), nil)
			if err != nil {
				t.Fatal(err)
			}
			if f.Name != tc.want {
				t.Errorf("Detect(%v) = %q, want %q", tc.deps, f.Name, tc.want)
			}
		})
	}
}

func TestDetectByConfigFile(t *testing.T) {
	f, err := Detect(nil, map[string]bool{"next.config.js": true})
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "nextjs" {
		t.Errorf("expected nextjs from config file, got %q", f.Name)
	}
}

func TestDetectNone(t *testing.T) {
	if _, err := Detect(pkg("left-pad"), nil); err == nil {
		t.Error("expected error for unrecognised project")
	}
}

func TestRenderModes(t *testing.T) {
	ssr := map[string]bool{"nextjs": true, "nuxt": true, "sveltekit": true, "remix": true, "express": true, "nestjs": true}
	for _, f := range Registry {
		if ssr[f.Name] && !f.IsSSR() {
			t.Errorf("%s should be SSR", f.Name)
		}
	}
	// A few that must stay static.
	for _, name := range []string{"vite", "react", "vue", "angular", "gatsby"} {
		f, _ := Get(name)
		if f.IsSSR() {
			t.Errorf("%s should be static", name)
		}
	}
}

func TestFrameworkManifest(t *testing.T) {
	next, _ := Get("nextjs")
	m := next.Manifest()
	if !m.IsSSR() {
		t.Error("nextjs manifest should be ssr")
	}
	if m.Framework != "nextjs" {
		t.Errorf("framework = %q", m.Framework)
	}
	if m.ABIOrDefault() != websiteSpec.ABIWasiStdio {
		t.Errorf("ssr framework manifest abi = %q, want wasi-stdio", m.ABIOrDefault())
	}
	// SSR frameworks get default api + catch-all routes.
	if m.Classify("/api/users") != websiteSpec.RouteAPI {
		t.Error("expected /api/users to classify as api")
	}
	if m.Classify("/_next/static/x.js") != websiteSpec.RouteStatic {
		t.Error("expected next static assets to classify as static")
	}
}

func TestDetectDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"next":"14"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := DetectDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "nextjs" {
		t.Errorf("DetectDir = %q, want nextjs", f.Name)
	}
}
