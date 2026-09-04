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
	require.Equal(t, 3, c.R())
	require.Equal(t, 2, c.K())
	require.Equal(t, DefaultMaxSkew, c.MaxSkew)
	require.Equal(t, 24*time.Hour, c.GraceGC)
	require.Equal(t, 5*time.Second, c.Liveness)
	require.Equal(t, 60*time.Second, c.Stabilize)
}

// TestKIsNeverInferred is owner decision O-5's default, at the place it bites.
// A K that adapted to the membership view would ack a write during a partition
// that no second node ever saw — which is v1's shipped lie, and the reason a
// single-node cloud has to WRITE Barrier: 1 rather than have it guessed.
func TestKIsNeverInferred(t *testing.T) {
	one, err := Config{Replicas: 1, Barrier: 1}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, 1, one.K(), "a single-node cloud says so in operator config")

	// Nothing in this package reads a membership count to choose K: the only
	// way to get K=1 is to have written it.
	def, err := Config{}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, 2, def.K())
}

func TestConfigRefusesWhatCannotBeCorrect(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  Config
		says string
	}{
		{"negative replicas", Config{Replicas: -1}, "places nothing"},
		{"zero-ish barrier", Config{Barrier: -1}, "shipped lie"},
		{"barrier above replicas", Config{Replicas: 2, Barrier: 3}, "every write would fail"},
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
	// The compiled-config service does not exist. Declaring the dependency as
	// a one-method interface is what makes that explicit rather than blocking.
	ctx := context.Background()
	cat := staticCatalog{buckets: []Address{addr(t)}}
	got, err := cat.Buckets(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)

	shards, err := occupiedShards(got)
	require.NoError(t, err)
	require.Len(t, shards, 1)
}

// TestRaftAppearsNowhere is the owner test, held by the compiler's own import
// graph rather than by a reviewer remembering. Raft is used for nothing here:
// not the pointer, not placement, not the durability barrier, not quota, not
// GC, not compaction scheduling. The one structure that would earn it — a
// per-bucket index range-split table — is deferred, and doc.go says why.
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

// TestNoPeerNodeAnywhere holds config.go's other structural claim: this service
// never holds a peer.Node. peer.Node bundles a host, a DHT, a datastore, a DAG
// and pubsub, so a test that wanted one of them got a DHT bootstrap and an
// on-disk datastore with it — and every test in this package runs in memory
// because nothing here can reach for it.
func TestNoPeerNodeAnywhere(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly)
	require.NoError(t, err)

	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				require.NotEqual(t, "github.com/taubyte/tau/p2p/peer", path, "%s imports p2p/peer", name)
			}
		}
	}
}

// TestOnlyStoreGoBuildsAKey is the census that keeps the grammar in one place.
// Every other file asks store.go or refs.go for a key; a file that started
// concatenating its own would be a second place that has to agree with the
// first about where a boundary is, and boundaries are what tenancy is made of.
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

// TestTheStorageBriefsSurfaceIsWhatIsExported pins the exported names so the
// next round — the RPC surface and the byte plane — reads a stable API rather
// than whatever this package happens to have today.
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
		"Accounted", "Address", "Blocks", "Bucket", "Catalog", "Clock", "Config", "Datastore",
		"DeadObject", "DefaultMaxSkew",
		"ErrEmptyIdentity", "ErrEmptyObjectName", "ErrGateClosed", "ErrLocalClockSkew",
		"ErrLogicalOverflow", "ErrNoAddress", "ErrNotFound", "ErrNotThisServicesRow",
		"ErrRemoteClockSkew", "ErrStampRange", "ErrWriterMismatch",
		"Gates", "Local", "Marker", "Meter", "MisplacementDrop",
		"NewAddress", "NewBloomMarker", "NewBucket", "NewClock", "NewExactMarker",
		"NewLocal", "NewSweeper", "Newest", "Op", "OpAdd", "OpRefuse", "OpRemove",
		"Order", "PackStamp", "ParseVersionKey", "Purpose",
		"Reference", "ShardOpener", "Shards", "SweepResult", "Sweeper",
		"UnpackStamp", "Usage", "Version", "VersionKey", "VersionRecord",
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
