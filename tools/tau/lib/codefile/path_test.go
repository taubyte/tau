package codefile

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

// WHERE A RESOURCE'S CODE LIVES: the project's code repository, then the
// resource's kind, then its name — and inside a scope, the scope's directory in
// between. The folder is passed in by the caller, so nothing here needs to know
// what the kinds are called.
func TestPath_TCCFixture_NoApp(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	p, err := Path("myfunc", "function")
	assert.NilError(t, err)
	assert.Assert(t, strings.HasSuffix(p.String(), filepath.Join("code", "function", "myfunc")), p.String())
}

func TestPath_TCCFixture_WithApp(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	scope := containerScope(t)
	assert.NilError(t, session.Set().SelectedApplication(scope))

	p, err := Path("myfunc", "function")
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(p.String(), scope), p.String())
	assert.Assert(t, strings.HasSuffix(p.String(), filepath.Join("function", "myfunc")), p.String())
}

// containerScope is a scope the fixture declares, found rather than named.
func containerScope(t *testing.T) string {
	t.Helper()

	groups, err := tcc.Groups()
	assert.NilError(t, err)

	st, err := tcc.Open()
	assert.NilError(t, err)

	for _, g := range groups {
		if !g.Container {
			continue
		}
		names, err := st.List(g.Dir)
		assert.NilError(t, err)
		assert.Assert(t, len(names) > 0, "the fixture declares no %s", g.Dir)
		return names[0]
	}

	t.Fatal("this vocabulary declares no container kind")
	return ""
}
