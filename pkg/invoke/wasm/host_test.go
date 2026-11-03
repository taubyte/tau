package wasm

import (
	"os"
	"regexp"
	"sort"
	"testing"

	"github.com/samyfodil/warpjs/hostmod"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
)

// Holds wit/deps and host.go's rows to each other: a world import nothing registers
// traps a guest; a row the world does not import is unreachable.

// worldImport matches wit/handler.wit's import lines; the `%` a keyword package
// needs in WIT source is not in a component's name.
var worldImport = regexp.MustCompile(`(?m)^\s*import\s+([a-z0-9:%/@.\-]+);`)

// importsOfTheWorld is what wit/handler.wit says a guest may reach.
func importsOfTheWorld(t *testing.T) []string {
	t.Helper()
	return importsOf(t, "wit/handler.wit")
}

// importsOf is the import list of one world file.
func importsOf(t *testing.T, path string) []string {
	t.Helper()

	source, err := os.ReadFile(path)
	require.NoError(t, err)

	var out []string
	for _, match := range worldImport.FindAllStringSubmatch(string(source), -1) {
		name := match[1]
		for i := 0; i < len(name); i++ {
			if name[i] == '%' {
				name = name[:i] + name[i+1:]
				i--
			}
		}
		out = append(out, name)
	}
	require.NotEmpty(t, out, "the world imports nothing, so this test proves nothing")
	sort.Strings(out)
	return out
}

func TestEveryImportedInterfaceHasAHostComponent(t *testing.T) {
	var registered []string
	for _, c := range hostComponents() {
		require.NotEmpty(t, c.iface, "%s names no interface", c.name)
		require.True(t, (c.register == nil) != (c.module == nil), "%s must register imports or define a module, one of the two", c.name)
		if c.module != nil {
			reg := hostmod.New()
			require.NoError(t, c.module(&Runtime{}, reg, invoke.Spec{}))
			provided, err := reg.Interfaces()
			require.NoError(t, err)
			require.Len(t, provided, 1, "%s's module provides more than its interface", c.name)
			require.Equal(t, c.iface, provided[0].Name, "%s's module provides another interface", c.name)
		}
		registered = append(registered, c.iface)
	}
	sort.Strings(registered)

	require.Equal(t, importsOfTheWorld(t), registered,
		"wit/handler.wit and host.go disagree about what this tier serves")
}

func TestASecondWorldReachesOnlyWhatThisTierServes(t *testing.T) {
	served := map[string]bool{}
	for _, c := range hostComponents() {
		served[c.iface] = true
	}

	for _, world := range []string{
		"wit/deps/taubyte-authorizer/authorizer.wit",
		"wit/deps/taubyte-instance-validator/instance-validator.wit",
		"wit/deps/taubyte-gateway-transformer/gateway-transformer.wit",
		"wit/deps/taubyte-gateway-router/gateway-router.wit",
	} {
		imports := importsOf(t, world)
		for _, name := range imports {
			require.True(t, served[name],
				"%s imports %s and host.go backs no such interface", world, name)
		}
		require.Less(t, len(imports), len(served),
			"%s imports everything; a world that is not a subset is the handler world with another name", world)
	}
}

func TestARowWithGrantsHasABackingAndAnError(t *testing.T) {
	for _, c := range hostComponents() {
		if c.reaches == nil {
			require.Nil(t, c.backed, "%s is ungranted and names a backing", c.name)
			require.NoError(t, c.missing, "%s is ungranted and names a refusal", c.name)
			continue
		}
		require.NotNil(t, c.backed, "%s can be granted and has no backing to check", c.name)
		require.Error(t, c.missing, "%s can be granted and says nothing when it is not backed", c.name)
	}
}

func TestEachInterfaceIsRegisteredOnce(t *testing.T) {
	seen := map[string]string{}
	for _, c := range hostComponents() {
		if first, twice := seen[c.iface]; twice {
			t.Fatalf("%s and %s both register %s", first, c.name, c.iface)
		}
		seen[c.iface] = c.name
	}
}
