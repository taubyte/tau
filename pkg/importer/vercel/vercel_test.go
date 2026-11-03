package vercel

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/importer"
)

func importOf(t *testing.T, js string) importer.Result {
	t.Helper()
	r, err := Import([]byte(js))
	require.NoError(t, err)
	return r
}

func finding(t *testing.T, r importer.Result, resource, key string) importer.Finding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Resource == resource && f.Key == key {
			return f
		}
	}
	t.Fatalf("no finding for %q/%q", resource, key)
	return importer.Finding{}
}

func TestTheBuildHalfIsWhatTheFileIsFor(t *testing.T) {
	r := importOf(t, `{
      "buildCommand": "next build",
      "installCommand": "pnpm i",
      "outputDirectory": ".next"
    }`)

	// WHAT THE FILE DESCRIBES IS A WEBSITE: a repository built by a command
	// into a directory that is then served. The field names are the
	// vocabulary's own and not Vercel's.
	doc, ok := r.Doc("website", SiteName)
	require.True(t, ok, "got %v", r.Resources)
	built := SiteName + "_code"
	require.Equal(t, map[string]any{"name": built}, doc["package"])
	require.Equal(t, map[string]any{"generated": map[string]any{"name": SiteName}}, doc["host"],
		"it answers at a generated name, as a Vercel project does before a domain is added")
	pkg, ok := r.Doc("package", built)
	require.True(t, ok)
	require.Equal(t, map[string]any{
		"type":   "assets",
		"output": ".next",
		"build": map[string]any{"steps": []any{map[string]any{
			"name": "build",
			// The image is written: without it the project imported with an empty to-do list and
			// failed to compile.
			"image":    defaultBuildImage,
			"commands": []string{"pnpm i", "next build"},
		}}},
	}, pkg)

	// AND THE CHOICE IS REPORTED, because it is a runtime nobody chose.
	f := finding(t, r, built, "build.steps.image")
	require.Equal(t, importer.Approximated, f.Level)
	require.Equal(t, "SOURCE_IN_API:node-version", f.Code)
}

func TestTheHalfThatIsNotInTheFileIsReportedAsElsewhere(t *testing.T) {
	r := importOf(t, `{"buildCommand": "next build"}`)

	elsewhere := r.Elsewhere()
	require.NotEmpty(t, elsewhere)

	var keys []string
	for _, f := range elsewhere {
		keys = append(keys, f.Key)
		require.Empty(t, f.Needs, "%s names a contract, but there is nothing for tau to build", f.Key)
	}
	require.Subset(t, keys, []string{"env", "domains"})
}

func TestFunctionLimitsAreTheOneCleanMapping(t *testing.T) {
	r := importOf(t, `{
      "functions": {
        "api/heavy.ts": { "memory": 3008, "maxDuration": 300 }
      }
    }`)

	doc, ok := r.Doc("function", "api_heavy_ts")
	require.True(t, ok, "got %v", r.Names("function"))
	// `call` NAMES THE EXPORT THE HOST CALLS and Vercel has no such field: its
	// Node runtime calls the module's default export, written by convention as
	// `export default function handler`. The vocabulary requires a name, so the
	// convention is what is written, and it is reported.
	require.Equal(t, map[string]any{
		"memory": "3008MB", "timeout": "300s", "call": defaultCall,
	}, doc["execution"])

	// A GLOB IS NOT A RESOURCE. `api/heavy.ts` names a file; a pattern names
	// however many the repository has, and this file does not say how many.
	f := finding(t, r, "api_heavy_ts", "functions.api/heavy.ts")
	require.Equal(t, importer.Approximated, f.Level)
	require.Equal(t, "GLOB_IS_NOT_A_RESOURCE", f.Code)
}

func TestARuntimeIsClassifiedHonestly(t *testing.T) {
	r := importOf(t, `{
      "functions": {
        "api/edge.ts":  { "runtime": "edge" },
        "api/node.ts":  { "runtime": "nodejs20.x" }
      }
    }`)

	edge := finding(t, r, "api/edge.ts", "runtime")
	require.Equal(t, importer.Approximated, edge.Level,
		"an edge function is the cheapest import there is, but it is still a different runtime")

	node := finding(t, r, "api/node.ts", "runtime")
	require.Equal(t, importer.Blocked, node.Level)
	require.Equal(t, "NEEDS_JS_ON_WASM", node.Code)
	require.NotEmpty(t, node.Needs)
}

func TestRoutingSplitsInTwo(t *testing.T) {
	r := importOf(t, `{
      "rewrites":  [ { "source": "/api/:path", "destination": "/api-handler" } ],
      "redirects": [ { "source": "/old", "destination": "/new", "permanent": true } ],
      "headers":   [ { "source": "/(.*)", "headers": [ { "key": "X-Frame-Options", "value": "DENY" } ] } ]
    }`)

	rw := finding(t, r, "", "rewrites")
	require.Equal(t, importer.Approximated, rw.Level, "a rewrite is routing and becomes an edge")
	require.Equal(t, "ROUTING_BECOMES_AN_EDGE", rw.Code)

	for _, key := range []string{"redirects", "headers"} {
		f := finding(t, r, "", key)
		require.Equal(t, importer.Blocked, f.Level, key)
		require.NotEmpty(t, f.Needs, key)
		require.Contains(t, f.Detail, "response", key)
	}
}

func TestAPatternIsNotApproximatedIntoAPrefix(t *testing.T) {
	r := importOf(t, `{
      "rewrites": [ { "source": "/(products|items)/(.*)", "destination": "/catalog" } ]
    }`)

	f := finding(t, r, "", "rewrites")
	require.Equal(t, importer.Blocked, f.Level)
	require.Equal(t, "PATTERN_NOT_EXPRESSIBLE", f.Code)
}

func TestANamedSegmentIsNotAPattern(t *testing.T) {
	require.False(t, isPCRE("/api/:path"))
	require.False(t, isPCRE("/blog/:slug/comments"))
	require.True(t, isPCRE("/(.*)"))
	require.True(t, isPCRE("/a|b"))
	require.True(t, isPCRE("^/x$"))
}

func TestCronsParseCleanlyAndHaveNowhereToGo(t *testing.T) {
	r := importOf(t, `{
      "crons": [ { "path": "/api/nightly", "schedule": "0 2 * * *" } ]
    }`)

	f := finding(t, r, "", "crons")
	require.Equal(t, importer.Blocked, f.Level)
	require.Equal(t, "NO_TAU_FEATURE:cron", f.Code)
	require.Contains(t, f.Detail, "0 2 * * *")
	require.NotEmpty(t, f.Needs)
}

func TestAFrameworkPresetIsNotExpanded(t *testing.T) {
	r := importOf(t, `{"framework": "nextjs"}`)

	f := finding(t, r, "", "framework")
	require.Equal(t, importer.Approximated, f.Level)
	require.Contains(t, f.Detail, "nextjs")
	require.NotContains(t, r.Resources, "framework")
}

func TestRegionsAreIgnored(t *testing.T) {
	r := importOf(t, `{"regions": ["iad1", "sfo1"]}`)
	require.Equal(t, importer.Ignored, finding(t, r, "", "regions").Level)
}

func TestMalformedJsonIsRefused(t *testing.T) {
	_, err := Import([]byte("{not json"))
	require.Error(t, err)
}

func TestAnEmptyFileStillReportsWhatIsElsewhere(t *testing.T) {
	r := importOf(t, `{}`)
	require.Empty(t, r.Resources)
	require.NotEmpty(t, r.Elsewhere())
	require.Equal(t, Source, r.Source)
}
