package storage

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfigDefaults(t *testing.T) {
	c, err := Config{}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, DefaultMaxSkew, c.MaxSkew)
	require.Equal(t, 24*time.Hour, c.GraceGC)
	require.Equal(t, 5*time.Second, c.Liveness)
	require.Equal(t, 60*time.Second, c.Stabilize)
}

func TestConfigRefusesWhatCannotBeCorrect(t *testing.T) {
	type testCase struct {
		name string
		cfg  Config
		says string
	}
	for _, c := range []testCase{
		// R and K are the cloud's; an impossible pair is refused where it is read
		// (pkg/fleet.Storage.Resolved).
		{"negative grace", Config{GraceGC: -time.Hour}, "must be positive"},
		{"negative skew", Config{MaxSkew: -time.Hour}, "must be positive"},
		{"stabilize below liveness", Config{Liveness: 90 * time.Second, Stabilize: 60 * time.Second}, "faster than acknowledgement"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.cfg.withDefaults()
			require.ErrorContains(t, err, c.says)
		})
	}
}

func TestAStaticCatalogSatisfiesTheDependency(t *testing.T) {
	// No compiled-config service exists; the one-method interface makes that explicit.
	ctx := context.Background()
	cat := staticCatalog{buckets: []Address{addr(t)}}
	got, err := cat.Buckets(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)

	shards, err := occupiedShards(got)
	require.NoError(t, err)
	require.Len(t, shards, 1)
}

func TestRaftAppearsNowhere(t *testing.T) {
	for _, dir := range []string{".", "../../pkg/placement", "../../pkg/membership"} {
		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, dir, nil, parser.ImportsOnly)
		require.NoError(t, err)
		require.NotEmpty(t, pkgs, "nothing parsed in %s", dir)

		for _, pkg := range pkgs {
			for name, file := range pkg.Files {
				for _, imp := range file.Imports {
					path := strings.Trim(imp.Path.Value, `"`)
					require.NotContains(t, path, "pkg/raft", "%s imports raft", name)
					require.NotContains(t, path, "hashicorp/raft", "%s imports raft", name)
					require.NotContains(t, path, "pkg/queue", "%s imports the raft-backed queue", name)
				}
			}
		}
	}
}

// The logic half takes narrow interfaces; only the two files that build them from
// the instance's peer may see it.
func TestOnlyTheHostSeesThePeer(t *testing.T) {
	sees := map[string]bool{"host.go": true, "bytes.go": true}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly)
	require.NoError(t, err)

	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			if sees[filepath.Base(name)] {
				continue
			}
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				require.NotEqual(t, "github.com/taubyte/tau/p2p/peer", path, "%s imports p2p/peer", name)
			}
		}
	}
}

func TestOnlyStoreGoBuildsAKey(t *testing.T) {
	// The two files that may name a key literal, and why:
	//   store.go  the replicated grammar
	//   refs.go   the node-local grammar
	allowed := map[string]bool{"store.go": true, "refs.go": true}

	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	checked := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || allowed[name] {
			continue
		}
		checked++

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		require.NoError(t, err)

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v := strings.Trim(lit.Value, "`\"")
			require.False(t, strings.HasPrefix(v, "/b/") || strings.HasPrefix(v, "/local/"),
				"%s builds a key literal %q; ask store.go or refs.go for it", name, v)
			return true
		})
	}
	require.GreaterOrEqual(t, checked, 5, "the census read almost nothing, which is how it passes for free")
}

func TestTheStorageBriefsSurfaceIsWhatIsExported(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)

	var names []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Recv != nil || !d.Name.IsExported() {
						continue
					}
					names = append(names, d.Name.Name)
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							if s.Name.IsExported() {
								names = append(names, s.Name.Name)
							}
						case *ast.ValueSpec:
							for _, id := range s.Names {
								if id.IsExported() {
									names = append(names, id.Name)
								}
							}
						}
					}
				}
			}
		}
	}
	sort.Strings(names)

	require.Equal(t, []string{
		"Accounted", "Address", "AssetsBucket", "Blocks", "Bucket", "Bytes", "Catalog", "Client",
		"Clock", "Config", "Datastore", "DeadObject", "DefaultBarrier", "DefaultMaxSkew",
		"DefaultReplicas", "DefaultSweep", "Descriptor", "Dial", "ErrEmptyIdentity",
		"ErrEmptyObjectName", "ErrGateClosed", "ErrLocalClockSkew", "ErrLogicalOverflow",
		"ErrNoAddress", "ErrNoBytes", "ErrNoCatalog", "ErrNoShards", "ErrNotFound",
		"ErrNotThisServicesRow", "ErrRemoteClockSkew", "ErrStampRange", "ErrUnderHeld",
		"ErrWriterMismatch", "Gates", "Local", "Marker", "Meter", "MisplacementDrop", "New",
		"NewAddress", "NewBloomMarker", "NewBucket", "NewClient", "NewClock", "NewExactMarker",
		"NewLocal", "NewSweeper", "Newest", "Object", "Op", "OpAdd", "OpRefuse", "OpRemove",
		"Order", "PackStamp", "ParseVersionKey", "Policy", "Protocol", "Purpose", "Reached", "Reference",
		"Service", "ShardOpener", "Shards", "Store", "StoreOn", "SweepResult", "Sweeper", "UnpackStamp",
		"Usage", "Version", "VersionKey", "VersionRecord",
	}, dedupe(names))
}

func dedupe(in []string) []string {
	out := in[:0:0]
	for i, v := range in {
		if i > 0 && in[i-1] == v {
			continue
		}
		out = append(out, v)
	}
	return out
}
