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

// TestTheBuildHalfIsWhatTheFileIsFor, and it maps onto the build stanza.
func TestTheBuildHalfIsWhatTheFileIsFor(t *testing.T) {
	r := importOf(t, `{
      "buildCommand": "next build",
      "installCommand": "pnpm i",
      "outputDirectory": ".next"
    }`)

	require.Equal(t, map[string]any{
		"command": "next build",
		"install": "pnpm i",
		"output":  ".next",
	}, r.Resources["build"])
}

// TestTheHalfThatIsNotInTheFileIsReportedAsElsewhere, not as a gap in tau.
// Filing it as Blocked would put "connect your account" on the list of things
// to build.
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

// TestFunctionLimitsAreTheOneCleanMapping. tau has per-resource limits and they
// are the same two numbers.
func TestFunctionLimitsAreTheOneCleanMapping(t *testing.T) {
	r := importOf(t, `{
      "functions": {
        "api/heavy.ts": { "memory": 3008, "maxDuration": 300 }
      }
    }`)

	require.Equal(t, map[string]any{"memory": "3008MB", "timeout": "300s"},
		r.Resources["api/heavy.ts"])
}

// TestAnEdgeFunctionIsFetchShapedAndSaysSo, and a Node one needs a JS tier.
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

// TestRoutingSplitsInTwo — the subtlest thing here. A rewrite selects a target
// and becomes an edge; a redirect or a header modifies a response and has no
// target at all. Reading the whole block as routing would create edges from
// rules that were only ever meant to set a header.
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

// TestAPatternIsNotApproximatedIntoAPrefix. A PCRE source matches different
// requests than any prefix does, and quietly turning one into the other sends
// traffic somewhere nobody asked for.
func TestAPatternIsNotApproximatedIntoAPrefix(t *testing.T) {
	r := importOf(t, `{
      "rewrites": [ { "source": "/(products|items)/(.*)", "destination": "/catalog" } ]
    }`)

	f := finding(t, r, "", "rewrites")
	require.Equal(t, importer.Blocked, f.Level)
	require.Equal(t, "PATTERN_NOT_EXPRESSIBLE", f.Code)
}

// TestANamedSegmentIsNotAPattern. Vercel's own :param syntax is an ordinary
// segment, and treating it as a regex would refuse the common case.
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

// TestAFrameworkPresetIsNotExpanded. The name stands for build defaults Vercel
// supplies; carrying the word would put something in the config nothing reads,
// and anything the preset supplied silently is simply missing.
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

// TestAnEmptyFileStillReportsWhatIsElsewhere. Even a vercel.json with nothing
// in it means a project whose env and domains live in the API.
func TestAnEmptyFileStillReportsWhatIsElsewhere(t *testing.T) {
	r := importOf(t, `{}`)
	require.Empty(t, r.Resources)
	require.NotEmpty(t, r.Elsewhere())
	require.Equal(t, Source, r.Source)
}
