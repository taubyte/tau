package specs_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"

	"github.com/taubyte/tau/pkg/specs"
)

// compiled is tcc compiling the vocabulary's own fixture, marshalled as the build
// service publishes it. A hand-written fixture would keep passing after the compiler
// changed shape.
func compiled(t testing.TB) []byte {
	t.Helper()

	c, err := schema.New(schema.WithLocal("../tcc/taubyte/v2/fixtures/basic"), schema.WithBranch("main"))
	require.NoError(t, err)

	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)

	b, err := json.Marshal(obj.Flat())
	require.NoError(t, err)
	return b
}

func load(t testing.TB) *specs.Project {
	t.Helper()
	p, err := specs.Load(compiled(t))
	require.NoError(t, err)
	return p
}

func TestAResourceIsFoundByName(t *testing.T) {
	p := load(t)
	require.NotEmpty(t, p.ID())

	api, id, err := p.Resource(nil, "service", "api")
	require.NoError(t, err)
	require.NotEmpty(t, id)

	require.Equal(t, "api", api.String("name"))

	// The values a runtime actually needs, in the units tcc compiled them to.
	mem, ok := api.Int("execution", "memory")
	require.True(t, ok)
	require.Equal(t, int64(512_000_000), mem, "512MB compiled to bytes")

	conc, ok := api.Int("limits", "concurrency")
	require.True(t, ok)
	require.Equal(t, int64(80), conc)

	port, ok := api.Int("ports", "http", "number")
	require.True(t, ok)
	require.Equal(t, int64(8080), port)

	require.ElementsMatch(t, []string{"http", "metrics"}, api.Keys("ports"))
	require.Equal(t, []string{"api.stripe.com"}, api.Strings("egress"))
}

func TestADurationCarriesItsUnit(t *testing.T) {
	p := load(t)

	fn, _, err := p.Resource(nil, "agent", "support")
	require.NoError(t, err)

	d, ok := fn.Duration("limits", "timeout")
	require.True(t, ok)
	require.Equal(t, 5*time.Minute, d)
}

func TestANameResolvesOutward(t *testing.T) {
	p := load(t)

	// `api` is declared at the project root and nowhere else.
	_, rootID, err := p.Resource(nil, "service", "api")
	require.NoError(t, err)

	_, fromShop, err := p.Resource([]string{"shop"}, "service", "api")
	require.NoError(t, err)
	require.Equal(t, rootID, fromShop, "the same resource, reached from a child scope")

	_, fromEU, err := p.Resource([]string{"shop", "local"}, "service", "api")
	require.NoError(t, err)
	require.Equal(t, rootID, fromEU, "and from a grandchild")
}

func TestTheNearestDeclarationWins(t *testing.T) {
	p := load(t)

	_, shop, err := p.Resource([]string{"shop"}, "cache", "sessions")
	require.NoError(t, err)

	_, local, err := p.Resource([]string{"shop", "local"}, "cache", "sessions")
	require.NoError(t, err)

	_, billing, err := p.Resource([]string{"billing"}, "cache", "sessions")
	require.NoError(t, err)

	require.NotEqual(t, shop, local, "local declares its own, so the parent's is not what it gets")
	require.NotEqual(t, shop, billing)

	// And the root does not have one at all, so a lookup there is a miss
	// rather than somebody else's.
	_, _, err = p.Resource(nil, "cache", "sessions")
	require.ErrorIs(t, err, specs.ErrNotFound)
}

func TestAChildsResourceIsNotVisibleFromItsParent(t *testing.T) {
	p := load(t)

	_, _, err := p.Resource([]string{"billing"}, "database", "ledger")
	require.NoError(t, err)

	_, _, err = p.Resource(nil, "database", "ledger")
	require.ErrorIs(t, err, specs.ErrNotFound)

	_, _, err = p.Resource([]string{"shop"}, "database", "ledger")
	require.ErrorIs(t, err, specs.ErrNotFound, "a sibling namespace is not a parent")
}

func TestWalkingTheProject(t *testing.T) {
	p := load(t)

	require.ElementsMatch(t, []string{"shop", "billing"}, mustNamespaces(t, p, nil))
	require.ElementsMatch(t, []string{"local"}, mustNamespaces(t, p, []string{"shop"}))
	require.Empty(t, mustNamespaces(t, p, []string{"billing"}))

	kinds, err := p.Kinds(nil)
	require.NoError(t, err)
	require.Contains(t, kinds, "service")
	require.Contains(t, kinds, "function")
	require.NotContains(t, kinds, "namespace", "a namespace is where things are, not a thing")

	names, err := p.Names(nil, "service")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"api", "db", "worker_pool"}, names)

	// And the walk reaches every resource it named.
	for _, kind := range kinds {
		names, err := p.Names(nil, kind)
		require.NoError(t, err)
		for _, name := range names {
			_, _, err := p.Resource(nil, kind, name)
			require.NoError(t, err, "%s/%s was listed and could not be read", kind, name)
		}
	}
}

func TestTheEastWestEdgeIsReadable(t *testing.T) {
	p := load(t)

	ch, _, err := p.Resource(nil, "channel", "sync")
	require.NoError(t, err)

	require.Equal(t, "pgwire", ch.String("protocol"))
	require.Equal(t, "service", ch.String("serve", "kind"))
	require.Equal(t, "db", ch.String("serve", "name"))
	require.Equal(t, "pg", ch.String("serve", "port"))

	// And what it names resolves, which is the half that makes it an edge
	// rather than a string.
	db, _, err := p.Resource(nil, ch.String("serve", "kind"), ch.String("serve", "name"))
	require.NoError(t, err)
	_, ok := db.Int("ports", ch.String("serve", "port"), "number")
	require.True(t, ok, "the channel names a port the workload declares")
}

func TestMissingThingsSayWhatIsMissing(t *testing.T) {
	p := load(t)

	_, _, err := p.Resource(nil, "service", "nope")
	require.ErrorIs(t, err, specs.ErrNotFound)

	_, _, err = p.Resource(nil, "nosuchkind", "api")
	require.ErrorIs(t, err, specs.ErrNotFound)

	_, _, err = p.Resource(nil, "", "api")
	require.ErrorIs(t, err, specs.ErrNotFound)

	_, _, err = p.Resource(nil, "service", "")
	require.ErrorIs(t, err, specs.ErrNotFound)

	_, _, err = p.Resource([]string{"nosuchnamespace"}, "service", "api")
	require.ErrorIs(t, err, specs.ErrNotFound)

	_, err = p.Namespaces([]string{"nosuchnamespace"})
	require.ErrorIs(t, err, specs.ErrNotFound)

	_, err = p.Kinds([]string{"nosuchnamespace"})
	require.ErrorIs(t, err, specs.ErrNotFound)

	_, err = p.Names([]string{"nosuchnamespace"}, "service")
	require.ErrorIs(t, err, specs.ErrNotFound)
}

func mustNamespaces(t *testing.T, p *specs.Project, scope []string) []string {
	t.Helper()
	out, err := p.Namespaces(scope)
	require.NoError(t, err)
	return out
}
