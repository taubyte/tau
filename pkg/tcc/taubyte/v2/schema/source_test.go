package schema_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// One `source` switch, and it is optional. Absent means this repository at the
// resource's own location, which is the common case and writes nothing — the state
// that used to be unwritable on a component and a website, and that a monorepo makes
// ordinary.
func sourceOf(t *testing.T, doc string) (any, error) {
	t.Helper()
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/function/checkout.yaml", []byte(
		"id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n"+
			doc+"execution: {call: c, timeout: 1s, memory: 1MB}\n"), 0o644))
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
	for _, tc := range []struct {
		name string
		doc  string
		want any
	}{
		{"absent means here", "", nil},
		{"a bare path is this repository", "source: pkg/checkout\n", "pkg/checkout"},
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

// The switch is what makes each of these an error at its own line instead of a key
// nothing ever reads. `source: "."` compiled clean before and meant nothing; it is
// now refused by IsRepoPath, which is the same check the bare path form gets.
func TestSourceSwitchRefuses(t *testing.T) {
	for _, tc := range []struct{ name, doc, contains string }{
		{"two branches at once", "source:\n    builtin: agent\n    component: paylib\n", "only one may be set"},
		{"a provider that is not a branch", "source:\n    github:\n        id: \"1\"\n", "unknown key 'github'"},
		{"git named as a block", "source:\n    git:\n        url: https://x/y\n", "takes a value, not a block"},
		{"a url with no host", "source:\n    git: acme/pay\n", "no scheme"},
		{"a path that climbs out", "source: ../elsewhere\n", "climbs out"},
		{"an absolute path", "source: /etc/passwd\n", "is absolute"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sourceOf(t, tc.doc)
			assert.ErrorContains(t, err, tc.contains)
		})
	}
}
