package schema_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/afero"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// sourceOf compiles one document and returns its `source`; absent means this
// repository at the resource's own location.
func sourceOf(t *testing.T, doc string) (any, error) {
	t.Helper()
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/function/checkout.yaml", []byte(
		"id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n"+
			"runtime: wasm\n"+doc+"execution: {call: c, timeout: 1s, memory: 1MB}\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	if err != nil {
		return nil, err
	}
	fn := namedResource(t, resources(t, obj.Flat(), "function"), "checkout")
	return fn["source"], nil
}

func TestSourceSwitch(t *testing.T) {
	type testCase struct {
		name string
		doc  string
		want any
	}
	for _, tc := range []testCase{
		{"absent means here", "", nil},
		{"a bare path is this repository", "source: pkg/checkout\n", "pkg/checkout"},
		{"a single dot is the repository root", "source: \".\"\n", "."},
		{"another component of this project", "source:\n    component: paylib\n    namespace: /\n",
			map[string]any{"kind": "component", "component": "QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv", "namespace": "/"}},
		{"another repository, by clone URL", "source:\n    git: https://gitlab.com/acme/pay\n    branch: main\n    path: cmd\n",
			map[string]any{"kind": "git", "git": "https://gitlab.com/acme/pay", "branch": "main", "path": "cmd"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sourceOf(t, tc.doc)
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tc.want)
		})
	}
}

func TestSourceSwitchRefuses(t *testing.T) {
	type testCase struct{ name, doc, contains string }
	for _, tc := range []testCase{
		{"two branches at once", "source:\n    builtin: agent\n    component: paylib\n", "only one may be set"},
		{"a provider that is not a branch", "source:\n    github:\n        id: \"1\"\n", "unknown key 'github'"},
		{"git named as a block", "source:\n    git:\n        url: https://x/y\n", "takes a value, not a block"},
		{"a url with no host", "source:\n    git: acme/pay\n", "no scheme"},
		{"a url carrying a token", "source:\n    git: https://u:ghp_secret@github.com/a/b\n", "carries a credential"},
		{"an scp url carrying a token", "source:\n    git: ghp_x:y@github.com:a/b\n", "carries a credential"},
		{"a good scp url", "source:\n    git: git@github.com:acme/pay\n", ""},
		{"a good ssh url", "source:\n    git: ssh://git@github.com/acme/pay\n", ""},
		{"a good ssh url with a port", "source:\n    git: ssh://git@github.com:22/acme/pay\n", ""},
		{"an scp url with whitespace", "source:\n    git: git@github.com:acme/re po\n", "not a URL"},
		{"an scp url naming no repository", "source:\n    git: git@github.com:/\n", "no repository"},
		{"a path that climbs out", "source: ../elsewhere\n", "climbs out"},
		{"an absolute path", "source: /etc/passwd\n", "is absolute"},

		{"a path through a climb", "source: pkg/../etc\n", "climbs out"},
		{"a dot with a path after it", "source: ./pkg\n", "only dots"},
		{"the root as a path inside a repository", "source:\n    git: https://g/x\n    path: \".\"\n", "not a place this field can name"},
		{"prose", "source: You answer questions about orders, and never guess.\n", "not a repository path"},
		{"an empty value", "source: \"\"\n", "empty: write a path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sourceOf(t, tc.doc)
			if tc.contains == "" {
				assert.NilError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.contains)
		})
	}
}

func TestAScalarWhereABlockBelongsIsStillAnError(t *testing.T) {
	const gwID = "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zg\n"
	type testCase struct{ name, path, doc, contains string }
	for _, tc := range []testCase{
		{"a database engine", "/database/orders.yaml",
			"id: QmPChzsSKcAiSFqNqrH7LM4t9GhqfHmi3ihn7C9hNc8Vgd\nsql: postgres\n", "sql/engine has an empty key"},
		{"a model's inference", "/model/opus.yaml",
			"id: QmPChzsSKcAiSFqNqrH7LM4t9GhqfHmi3ihn7C9hNc8Vgd\nformat: anthropic\nidentifier: x\ninference: remote\n", "inference/kind has an empty key"},
		{"a gateway's host", "/gateway/ask.yaml",
			gwID + "host: api.example.com\nroute:\n    path: /\n    match: prefix\n    handler:\n        agent:\n            name: support\n", "host/kind has an empty key"},
		{"a route's handler", "/gateway/ask.yaml",
			gwID + "host:\n    generated:\n        name: ask\nroute:\n    path: /\n    match: prefix\n    handler: support\n", "route/handler/kind has an empty key"},
		{"a proxy's forward", "/proxy/postgres.yaml",
			"id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nservice: postgres\nhost:\n    generated:\n        name: db\nforward: db\n", "forward/kind has an empty key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
			assert.NilError(t, afero.WriteFile(fs, tc.path, []byte(tc.doc), 0o644))
			c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
			assert.NilError(t, err)
			_, _, err = c.Compile(context.Background())
			assert.ErrorContains(t, err, tc.contains)
		})
	}
}

func TestAnEmptyValueIsToldOnlyWhatTheFieldTakes(t *testing.T) {
	loc := schema.RepoLocationChecker()("")
	assert.ErrorContains(t, loc, "empty: write a path")
	assert.ErrorContains(t, loc, "\".\" for the repository root")

	path := schema.RepoPathChecker()("")
	assert.ErrorContains(t, path, "empty: write a path")
	for _, borrowed := range []string{"repository root", "own location"} {
		assert.Assert(t, !strings.Contains(path.Error(), borrowed),
			"a field that refuses the root is told about %q: %v", borrowed, path)
	}
}

func TestRepoPathPatternAndCheckerAgree(t *testing.T) {
	type testCase struct {
		name    string
		pattern string
		check   func(string) error
	}
	for _, g := range []testCase{
		{"path", schema.RepoPathPattern(), schema.RepoPathChecker()},
		{"location", schema.RepoLocationPattern(), schema.RepoLocationChecker()},
	} {
		grammarAgrees(t, g.name, regexp.MustCompile(g.pattern), g.check)
	}
}

func grammarAgrees(t *testing.T, name string, pattern *regexp.Regexp, check func(string) error) {
	t.Helper()
	for _, p := range []string{
		// legal
		"pkg", "pkg/checkout", "a.b/c-d_e", "agents/support", ".github/workflows", ".",
		"a", "z/y/x", "1", "a-b", "_x",
		"a1/b2/c3", "x.y.z", "-lead", "_under", "..a", "a..",
		// not
		"", "..", "...", "/abs", "rel/", "a//b", "a/../b", "../x", "a b",
		"café/x", "You answer questions about orders, and never guess.",
		"a\\b", "a?b", "a*b", "a:b", "#c", "a/./b",
	} {
		byPattern := pattern.MatchString(p)
		byChecker := check(p) == nil
		assert.Equal(t, byPattern, byChecker,
			"%s: pattern and checker disagree about %q (pattern=%v checker=%v)", name, p, byPattern, byChecker)
	}
}

func TestAResourcesOwnLocationIsATemplate(t *testing.T) {
	l := schema.LayoutOf()
	assert.Equal(t, l.Content, "{kind}/{name}")

	fill := func(tpl, kind, name string) string {
		return strings.ReplaceAll(strings.ReplaceAll(tpl, "{kind}", kind), "{name}", name)
	}
	// the document and the content sit beside each other, which is the whole rule
	assert.Equal(t, fill(l.Resource, "agent", "support"), "agent/support.yaml")
	assert.Equal(t, fill(l.Content, "agent", "support"), "agent/support")
}
