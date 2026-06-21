package websiteSpec

import (
	"testing"
)

func TestParseManifestDefaults(t *testing.T) {
	m, err := ParseManifest([]byte(`{"render":"ssr"}`))
	if err != nil {
		t.Fatal(err)
	}

	if m.Version != ManifestVersion {
		t.Errorf("expected default version %q, got %q", ManifestVersion, m.Version)
	}
	if m.Entry != DefaultEntry {
		t.Errorf("expected default entry %q, got %q", DefaultEntry, m.Entry)
	}
	if m.Handler != DefaultHandlerPath {
		t.Errorf("expected default handler %q, got %q", DefaultHandlerPath, m.Handler)
	}
	if m.Memory != DefaultSSRMemory {
		t.Errorf("expected default memory %d, got %d", DefaultSSRMemory, m.Memory)
	}
	if m.Timeout != DefaultSSRTimeout {
		t.Errorf("expected default timeout %d, got %d", DefaultSSRTimeout, m.Timeout)
	}
	if m.fallback() != RouteSSR {
		t.Errorf("expected ssr fallback, got %q", m.fallback())
	}
	if !m.IsSSR() {
		t.Error("expected manifest to be SSR")
	}
}

func TestParseManifestValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		ok   bool
	}{
		{"empty defaults to ssr", `{}`, true},
		{"static ok", `{"render":"static"}`, true},
		{"invalid render", `{"render":"isomorphic"}`, false},
		{"invalid route type", `{"render":"ssr","routes":[{"pattern":"/","type":"edge"}]}`, false},
		{"explicit handler cid", `{"render":"ssr","handlerCid":"bafy123"}`, true},
		{"bad json", `{`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(tc.in))
			if tc.ok && err != nil {
				t.Errorf("expected ok, got error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestManifestRoundTrip(t *testing.T) {
	original := &Manifest{
		Render:    RenderSSR,
		Framework: "nextjs",
		Static:    []string{"/_next/static/"},
		Routes:    []Route{{Pattern: "/api/", Type: RouteAPI}},
	}
	original.SetDefaults()

	data, err := original.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}

	if parsed.Framework != "nextjs" {
		t.Errorf("framework not preserved: %q", parsed.Framework)
	}
	if len(parsed.Routes) != 1 || parsed.Routes[0].Type != RouteAPI {
		t.Errorf("routes not preserved: %#v", parsed.Routes)
	}
}

func TestManifestABI(t *testing.T) {
	// Default ABI for an SSR manifest is the function calling convention.
	m, err := ParseManifest([]byte(`{"render":"ssr"}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.ABIOrDefault() != ABIFunction {
		t.Errorf("default abi = %q, want %q", m.ABIOrDefault(), ABIFunction)
	}

	// wasi-stdio is valid and does not require an entry point.
	m, err = ParseManifest([]byte(`{"render":"ssr","abi":"wasi-stdio","entry":""}`))
	if err != nil {
		t.Fatalf("wasi-stdio manifest should be valid: %v", err)
	}
	if m.ABIOrDefault() != ABIWasiStdio {
		t.Errorf("abi = %q, want %q", m.ABIOrDefault(), ABIWasiStdio)
	}

	// The component ABI (future richer engine) is accepted by the spec.
	if _, err := ParseManifest([]byte(`{"render":"ssr","abi":"component","handlerCid":"bafy"}`)); err != nil {
		t.Errorf("component abi should be accepted by the spec: %v", err)
	}

	// An unknown ABI is rejected.
	if _, err := ParseManifest([]byte(`{"render":"ssr","abi":"v8-isolate"}`)); err == nil {
		t.Error("expected unknown abi to be rejected")
	}
}

func TestClassify(t *testing.T) {
	m := &Manifest{
		Render: RenderSSR,
		Static: []string{"/_next/static/", "/assets/", "/favicon.ico"},
		Routes: []Route{
			{Pattern: "/api/", Type: RouteAPI},
			{Pattern: "/", Type: RouteSSR},
		},
	}
	m.SetDefaults()

	for _, tc := range []struct {
		path string
		want RouteType
	}{
		{"/", RouteSSR},
		{"/blog/post-1", RouteSSR},
		{"/api/users", RouteAPI},
		{"/api", RouteAPI},
		{"/_next/static/chunks/main.js", RouteStatic},
		{"/assets/logo.svg", RouteStatic},
		{"/favicon.ico", RouteStatic},
		// most specific rule wins: /api beats the catch-all /
		{"/api/v2/orders", RouteAPI},
		// a path that merely shares a prefix string but not a segment boundary
		// must not match the static prefix.
		{"/assetsX/oops", RouteSSR},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if got := m.Classify(tc.path); got != tc.want {
				t.Errorf("Classify(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestClassifyFallback(t *testing.T) {
	m := &Manifest{Render: RenderSSR, Fallback: RouteSSR}
	m.SetDefaults()
	if got := m.Classify("/anything"); got != RouteSSR {
		t.Errorf("expected ssr fallback, got %q", got)
	}

	// No explicit catch-all route, static-only classification.
	s := &Manifest{Render: RenderStatic, Static: []string{"/public/"}}
	s.SetDefaults()
	if got := s.Classify("/public/x.png"); got != RouteStatic {
		t.Errorf("expected static, got %q", got)
	}
	if got := s.Classify("/page"); got != RouteStatic {
		t.Errorf("expected static fallback for static site, got %q", got)
	}
}

func TestStaticPrefixesSorted(t *testing.T) {
	m := &Manifest{Static: []string{"/a/", "/abcd/", "/ab/"}}
	got := m.StaticPrefixes()
	if got[0] != "/abcd/" {
		t.Errorf("expected longest prefix first, got %v", got)
	}
}
