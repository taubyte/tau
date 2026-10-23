package specs_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// loadFixture compiles a fixture AS ONE DEPLOYMENT sees it, which is what the
// register holds — so what these tests read back is what a consumer reads back.
func loadFixture(t testing.TB, dir, environment, branch string) *specs.Project {
	t.Helper()

	options := []schema.Option{schema.WithLocal(dir), schema.WithBranch(branch)}
	declared, err := schema.EnvironmentsOf(afero.NewOsFs(), dir)
	require.NoError(t, err)
	if environment != "" {
		options = append(options,
			schema.WithEnvironments(schema.EnvironmentNames(declared)...),
			schema.WithEnvironment(environment))
	}

	c, err := schema.New(options...)
	require.NoError(t, err)

	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)

	raw, err := json.Marshal(obj.Flat())
	require.NoError(t, err)

	p, err := specs.Load(raw)
	require.NoError(t, err)
	return p
}

const (
	basicFixture = "../tcc/taubyte/v2/fixtures/basic"
	envsFixture  = "../tcc/taubyte/v2/fixtures/environments"
)

// THE TABLE SURVIVES THE COMPILE, and that is what makes a code push buildable:
// a push knows a project and a branch and nothing else, and everything needed
// to decide what to build for is already in what the register published.
func TestABranchResolvesToItsDeploymentsFromTheCompiledObject(t *testing.T) {
	p := loadFixture(t, envsFixture, "production", "main")

	names := func(branch string) []string { return p.Deployments(branch) }

	// main feeds production AND the shadow written inside it.
	require.Equal(t, []string{"preview", "production", "ab"}, names("main"))
	require.Equal(t, []string{"preview", "production"}, names("master"))
	require.Equal(t, []string{"preview", "staging"}, names("release/1.4"))
	require.Equal(t, []string{"preview"}, names("feature/checkout"))

	// The table itself reads back whole, nesting included.
	table := p.Environments()
	require.Len(t, table, 3)

	byName := map[string]int{}
	for i, e := range table {
		byName[e.Name] = i
	}
	production := table[byName["production"]]
	require.Equal(t, []string{"main", "master"}, production.Branches)
	require.Equal(t, []string{"region-1", "region-2"}, production.Regions)
	require.Len(t, production.Shadows, 1)
	require.Equal(t, "ab", production.Shadows[0].Name)
	require.Equal(t, "production", production.Shadows[0].Of)

	// A shadow places no state of its own: it runs over its parent's.
	require.Empty(t, production.Shadows[0].Regions)
}

// One environment fed by every branch is the whole of what a single-deployment
// project declares.
func TestOneEnvironmentFedByEveryBranch(t *testing.T) {
	p := loadFixture(t, basicFixture, "", "main")
	require.Len(t, p.Environments(), 1)
	require.Equal(t, []string{"prod"}, p.Deployments("main"))
	require.Equal(t, []string{"prod"}, p.Deployments("feature/x"))
}
