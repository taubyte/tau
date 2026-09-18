package wasm

import (
	"os"
	"regexp"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// host_test holds the two sides of the ABI to each other.
//
// EVERY CAPABILITY IS TWO FILES NOW — a WIT package under wit/deps and a row in
// host.go — and the failure mode of that shape is adding one and forgetting the
// other. A world that imports an interface nothing registers is a guest that
// TRAPS on its first call to it, taking the instance with it; a row for an
// interface the world does not import is a registration nobody can reach.
//
// Neither is visible from any other test here, because every one of them drives
// a fixture that was built before the mismatch.

// worldImport matches the import lines of wit/handler.wit. The `%` a keyword
// package needs in WIT SOURCE is not in the name a component carries, so it is
// dropped — see the stream package.
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

// TestTHEREGISTERCoversTheWorldExactly.
func TestEveryImportedInterfaceHasAHostComponent(t *testing.T) {
	var registered []string
	for _, c := range hostComponents() {
		require.NotEmpty(t, c.iface, "%s names no interface", c.name)
		require.NotNil(t, c.register, "%s registers nothing", c.name)
		registered = append(registered, c.iface)
	}
	sort.Strings(registered)

	require.Equal(t, importsOfTheWorld(t), registered,
		"wit/handler.wit and host.go disagree about what this tier serves")
}

// TestASECONDWORLDCANONLYREACHWHATTHEFIRSTONESERVES.
//
// `implements:` names seven worlds and this tier now carries the WIT for four of
// them. A second world is a SUBSET of the capabilities — that subsetting is half
// the reason the packages were split up in the first place — so every interface
// it names has to be one the host actually backs. A world importing a capability
// with no host half is a world nothing can run, and the way that is discovered
// today is a guest trapping on a missing import at its first call.
//
// It is a subset check and NOT equality, which is the difference from the test
// above: the handler world is the union by definition, and an authorizer that
// imported all of it would be claiming a decision may do anything.
func TestASecondWorldReachesOnlyWhatThisTierServes(t *testing.T) {
	served := map[string]bool{}
	for _, c := range hostComponents() {
		served[c.iface] = true
	}

	for _, world := range []string{
		"wit/deps/taubyte-authorizer/authorizer.wit",
		"wit/deps/taubyte-instance-validator/instance-validator.wit",
		"wit/deps/taubyte-gateway-transformer/gateway-transformer.wit",
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

// TestEVERYRowThatCanBeGrantedCanBeRefused. A capability with grants and no
// backing must have something to say at Start; one without grants must not
// claim a backing it never checks.
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

// TestNoTwoRowsServeOneInterface, which would make the second registration lose
// silently to the first.
func TestEachInterfaceIsRegisteredOnce(t *testing.T) {
	seen := map[string]string{}
	for _, c := range hostComponents() {
		if first, twice := seen[c.iface]; twice {
			t.Fatalf("%s and %s both register %s", first, c.name, c.iface)
		}
		seen[c.iface] = c.name
	}
}
