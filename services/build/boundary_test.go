package build

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// boundary_test.go is the trust boundary as a test rather than as a
// convention. service-topology.md §2 says the two-public-services invariant
// "is assertable in a test and should be one, because it is the kind of
// property that decays silently", and every property below is one somebody
// adds a line and breaks without noticing:
//
//   - the public role may not import the private one. If it could, the
//     process a stranger's POST reaches would hold the queue's writer and the
//     clone credentials, and the split would be a directory layout rather
//     than a boundary.
//   - the private role may not import pkg/http. That is
//     service-conventions.md §5a's second, mechanical half of the ingress
//     check: the one that catches a listener added without touching the
//     descriptor.
//   - the dial adapters may import neither role, so that neither role's
//     internals can reach the other through them.
//
// It reads the packages' own import lists rather than `go list -deps`,
// deliberately: a transitive scan would answer "does net/http appear anywhere
// below this" — which it does, through p2p/rpc — where the question is what
// THIS package was written against. That is the same scope
// service-conventions.md states ("grep for pkg/http in this package and find
// nothing"), and it is the scope a reviewer can check by eye.
const (
	buildPkg    = "github.com/taubyte/tau/services/build"
	backendPkg  = buildPkg + "/backend"
	frontendPkg = buildPkg + "/frontend"
	clientsPkg  = buildPkg + "/clients"
)

// imports returns the import paths of the non-test files of the package in
// dir, and whether the directory exists at all.
//
// IT CHECKS THAT IT READ EVERY FILE THAT IS THERE, because a scan that filters
// its own input by name proves nothing about the files it skipped. One line in
// the filter — `&& fi.Name() != "service.go"` — would take a file out of every
// check below at once, and every check below is an ABSENCE: an import that is
// not there. An absence measured over a file set nobody pinned is an absence of
// evidence. sources() is the directory's own answer and is the same rule this
// filter is meant to be applying.
func imports(t *testing.T, dir string) ([]string, bool) {
	t.Helper()

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, false
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly)
	require.NoError(t, err)

	var read, out []string
	for _, pkg := range pkgs {
		for name, f := range pkg.Files {
			read = append(read, filepath.Base(name))
			for _, spec := range f.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				require.NoError(t, err)
				out = append(out, path)
			}
		}
	}

	var there []string
	for name := range sources(t, dir) {
		there = append(there, name)
	}
	require.ElementsMatch(t, there, read,
		"the import scan of %s read a different set of files than the directory holds — a file "+
			"it skips is a file every check below is blind to, and every check below is an "+
			"absence", dir)

	return out, true
}

// sources returns the text of the non-test .go files in dir.
func sources(t *testing.T, dir string) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, err)
		out[e.Name()] = string(b)
	}
	return out
}

func requireNoImport(t *testing.T, paths []string, banned, why string) {
	t.Helper()
	for _, p := range paths {
		if p == banned || strings.HasPrefix(p, banned+"/") {
			t.Fatalf("import of %q is not allowed here: %s", p, why)
		}
	}
}

// This package is the pair's shared vocabulary, so anything it imports is
// imported by both roles. Keeping it to three is what makes the backend's
// "imports no pkg/http" check mechanical rather than transitive.
func TestTheSharedPackageStaysThreeImportsWide(t *testing.T) {
	got, ok := imports(t, ".")
	require.True(t, ok)
	require.ElementsMatch(t, []string{
		"github.com/libp2p/go-libp2p/core/protocol",
		"github.com/taubyte/tau/p2p/rpc",
		"github.com/taubyte/tau/pkg/proto/gen/tau/build/v2/buildv2connect",
	}, got)
}

func TestThePrivateRoleHasNoIngress(t *testing.T) {
	paths, ok := imports(t, "backend")
	require.True(t, ok, "the backend package must exist")

	requireNoImport(t, paths, "github.com/taubyte/tau/pkg/http",
		"IngressNone means no HTTP listener; a route added here would face whatever reaches the node")
	requireNoImport(t, paths, frontendPkg,
		"the private role must not depend on the public one")
	requireNoImport(t, paths, "github.com/taubyte/tau/legacy",
		"nothing new imports v1's frozen address space")

	for name := range sources(t, "backend") {
		require.NotEqual(t, "ingress.go", name,
			"ingress.go exists in exactly two packages in the tree, and neither is this one")
	}
}

// The adapters both roles dial through may not import either role. Returning a
// role's interface type from here would make every frontend that dials auth
// depend on the backend's package, which is the dependency this whole file
// exists to keep out — so the adapters return their own concrete types and the
// interface satisfaction is asserted in clients' TEST files, where a dependency
// costs nothing.
func TestTheDialAdaptersImportNeitherRole(t *testing.T) {
	paths, ok := imports(t, "clients")
	require.True(t, ok, "the clients package must exist")

	requireNoImport(t, paths, backendPkg, "an adapter must not depend on the role that consumes it")
	requireNoImport(t, paths, frontendPkg, "an adapter must not depend on the role that consumes it")
	requireNoImport(t, paths, "github.com/taubyte/tau/services/auth",
		"dialling auth needs auth's SCHEMA, never auth's service package and the store behind it")
}

// The public role's half of the same boundary, and the half that matters most:
// this is the one process in build a stranger can reach.
func TestThePublicRoleServesNoP2PRPC(t *testing.T) {
	paths, ok := imports(t, "frontend")
	require.True(t, ok, "the frontend package must exist")

	requireNoImport(t, paths, backendPkg,
		"the public role must not import the private one: it would hold the queue's writer")
	requireNoImport(t, paths, "github.com/taubyte/tau/p2p/rpc",
		"the public role dials through the interfaces it declares; the pool that speaks p2p lives in clients")
	requireNoImport(t, paths, "github.com/libp2p/go-libp2p/core/host",
		"a host is what a p2p listener is mounted on, and this role has none")
	requireNoImport(t, paths, "github.com/taubyte/tau/legacy",
		"nothing new imports v1's frozen address space")
	requireNoImport(t, paths, "gopkg.in/go-playground/webhooks.v5",
		"the library v1 verifies webhooks with is sha1-only and skips the MAC on an empty secret")

	// The mechanical half of service-conventions.md §5a, in the direction the
	// backend's test runs the other way: a listener added here without touching
	// the descriptor is caught by the source, not by a convention.
	for name, src := range sources(t, "frontend") {
		require.NotContains(t, src, "rpc.Listen(",
			"%s mounts a p2p listener on the public role; build.proto's header is explicit that it has none", name)
	}

	// And the positive half: IngressPublic means there IS an ingress.go, and it
	// is the file a reviewer opens to see every route this cloud exposes.
	require.Contains(t, sources(t, "frontend"), "ingress.go",
		"the public role's routes and limits live in ingress.go, as they do in gateway's")
}

// The identity adapter is a package of its own, and it names NO provider: the
// host picks the forge through pkg/forge, so no provider SDK is imported
// anywhere in this tree's build service and a new forge is a binding, not a
// sibling package.
func TestTheIdentityAdapterKnowsNoProvider(t *testing.T) {
	frontendImports, ok := imports(t, "frontend")
	require.True(t, ok)
	requireNoImport(t, frontendImports, "github.com/google/go-github",
		"the frontend knows Identity, not a provider")
	requireNoImport(t, frontendImports, "github.com/taubyte/tau/pkg/forge",
		"the frontend knows Identity, not how it is answered")

	adapterImports, ok := imports(t, filepath.Join("frontend", "forgeauth"))
	require.True(t, ok, "the identity adapter must exist")
	requireNoImport(t, adapterImports, "github.com/google/go-github",
		"a provider SDK is a driver's, in pkg/forge, never the adapter's")
	requireNoImport(t, adapterImports, "github.com/taubyte/tau/pkg/forge/github",
		"the adapter asks by host; which driver answers is the node's binding")
	requireNoImport(t, adapterImports, "github.com/taubyte/tau/pkg/forge/gitlab",
		"the adapter asks by host; which driver answers is the node's binding")
	requireNoImport(t, adapterImports, "github.com/taubyte/tau/pkg/forge/gitea",
		"the adapter asks by host; which driver answers is the node's binding")
	requireNoImport(t, adapterImports, "github.com/taubyte/tau/pkg/forge/onedev",
		"the adapter asks by host; which driver answers is the node's binding")
	requireNoImport(t, adapterImports, backendPkg,
		"the public role's own adapter must not reach the private role")
	requireNoImport(t, adapterImports, "github.com/taubyte/tau/services/auth",
		"restating auth's access rule is not importing auth")
}
