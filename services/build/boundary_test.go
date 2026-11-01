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

// The trust boundary as a test: the public role may not import the private one,
// the private role may not import pkg/http, and the dial adapters may import
// neither role. It reads each package's own imports, not its transitive set,
// since net/http appears below p2p/rpc.
const (
	buildPkg    = "github.com/taubyte/tau/services/build"
	backendPkg  = buildPkg + "/backend"
	frontendPkg = buildPkg + "/frontend"
	clientsPkg  = buildPkg + "/clients"
)

// imports returns the import paths of the non-test files in dir, and whether dir
// exists. It checks it read every file present, since every check here is an
// absence.
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

func TestTheDialAdaptersImportNeitherRole(t *testing.T) {
	paths, ok := imports(t, "clients")
	require.True(t, ok, "the clients package must exist")

	requireNoImport(t, paths, backendPkg, "an adapter must not depend on the role that consumes it")
	requireNoImport(t, paths, frontendPkg, "an adapter must not depend on the role that consumes it")
	requireNoImport(t, paths, "github.com/taubyte/tau/services/auth",
		"dialling auth needs auth's SCHEMA, never auth's service package and the store behind it")
}

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
	requireNoImport(t, adapterImports, "github.com/taubyte/tau/pkg/forge/gogs",
		"the adapter asks by host; which driver answers is the node's binding")
	requireNoImport(t, adapterImports, "github.com/taubyte/tau/pkg/forge/gerrit",
		"the adapter asks by host; which driver answers is the node's binding")
	requireNoImport(t, adapterImports, "github.com/taubyte/tau/pkg/forge/sourcehut",
		"the adapter asks by host; which driver answers is the node's binding")
	requireNoImport(t, adapterImports, "github.com/taubyte/tau/pkg/forge/bitbucket",
		"the adapter asks by host; which driver answers is the node's binding")
	requireNoImport(t, adapterImports, "github.com/taubyte/tau/pkg/forge/bitbucketserver",
		"the adapter asks by host; which driver answers is the node's binding")
	requireNoImport(t, adapterImports, "github.com/taubyte/tau/pkg/forge/azure",
		"the adapter asks by host; which driver answers is the node's binding")
	requireNoImport(t, adapterImports, backendPkg,
		"the public role's own adapter must not reach the private role")
	requireNoImport(t, adapterImports, "github.com/taubyte/tau/services/auth",
		"restating auth's access rule is not importing auth")
}
