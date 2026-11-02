package specs_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

// A package's source is an Either and its build a block, both read off the compiled
// package: the only kind that builds.

func TestABarePathIsAPathInTheProjectsOwnRepository(t *testing.T) {
	p := load(t)

	r, _, err := p.Resource(nil, "package", "reserve_stock_code")
	require.NoError(t, err)

	source := r.Source()
	require.Equal(t, specs.SourcePath, source.Kind)
	require.Equal(t, "services/reserve", source.Path)
	require.Empty(t, source.Git)
	require.True(t, source.Built(), "a path in a repository is something this platform makes")

	pkg := specs.ReadPackage(r)
	require.True(t, pkg.Build.Declared)
	require.Equal(t, specs.PackageBuildKindSteps, pkg.Build.Kind)
	require.Len(t, pkg.Build.Steps, 1)
	require.Equal(t, "rust:1.83", pkg.Build.Steps[0].Image)
	require.Equal(t, []string{"cargo fetch --locked", "cargo build --release --target wasm32-wasip2"}, pkg.Build.Steps[0].Commands)
	require.Equal(t, "target/wasm32-wasip2/release/reserve.wasm", pkg.Output)
}

func TestAPackageCanNameItsOwnRepository(t *testing.T) {
	p := load(t)

	r, _, err := p.Resource(nil, "package", "paylib")
	require.NoError(t, err)

	source := r.Source()
	require.Equal(t, specs.SourceGit, source.Kind)
	require.Equal(t, "https://github.com/acme/paylib", source.Git)
	require.Equal(t, "main", source.Branch)
	require.True(t, source.Built())

	// A script is a step like any other.
	pkg := specs.ReadPackage(r)
	require.Equal(t, "taubyte/go-wasi:v2", pkg.Build.Steps[0].Image)
	require.Equal(t, "paylib.wasm", pkg.Output)
}

func TestWhatIsNotBuiltFromSource(t *testing.T) {
	p := load(t)

	// A workload names its package and has no source of its own. A REFERENCE COMPILES
	// TO AN ID, not to the name it was written as — which is what makes a rename of
	// the package not a rewrite of everything that names it.
	r, _, err := p.Resource(nil, "function", "checkout")
	require.NoError(t, err)
	require.False(t, r.Source().Declared)
	require.Equal(t, "QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv", specs.ReadWorkloadBlock(r).PackageName)

	// A package of an image somebody else made.
	r, _, err = p.Resource(nil, "package", "api_image")
	require.NoError(t, err)
	source := r.Source()
	require.Equal(t, specs.SourceImage, source.Kind)
	require.False(t, source.Built())
}

func TestBuildablesAreEveryPackageMadeFromSource(t *testing.T) {
	p := load(t)

	built := p.Buildables()
	require.NotEmpty(t, built)

	byName := map[string]specs.Buildable{}
	for _, b := range built {
		byName[b.Name] = b

		// Everything here is buildable and says how.
		require.True(t, b.Source.Built(), b.Name)
		require.True(t, b.Package.Build.Declared, b.Name)
		require.NotEmpty(t, b.ID, b.Name)
	}

	require.Contains(t, byName, "reserve_stock_code")
	require.Contains(t, byName, "paylib")
	require.Contains(t, byName, "shop_site")

	// Nothing but a package is built: a workload runs one.
	for _, b := range built {
		kind, _, _, ok := p.Locate(b.ID)
		require.True(t, ok)
		require.Equal(t, specs.KindPackage, kind)
	}
	// Nor is a package of a pulled image.
	require.NotContains(t, byName, "api_image")

	// The order is stable across reads, though the source is a map.
	for i := range 5 {
		require.Equal(t, built, load(t).Buildables(), "run %d", i)
	}
}

func TestABuildableSaysWhichRepositoryItNeeds(t *testing.T) {
	byName := map[string]specs.Buildable{}
	for _, b := range load(t).Buildables() {
		byName[b.Name] = b
	}

	own := byName["reserve_stock_code"]
	require.Empty(t, own.Source.Git, "a bare path is the project's own code repository")
	require.Equal(t, "services/reserve", own.Source.Path)

	elsewhere := byName["paylib"]
	require.Equal(t, "https://github.com/acme/paylib", elsewhere.Source.Git)
}

func TestAnAbsentSourceIsNotTheRepositoryRoot(t *testing.T) {
	p := load(t)

	// A database declares none.
	r, _, err := p.Resource(nil, "database", "orders")
	require.NoError(t, err)

	source := r.Source()
	require.False(t, source.Declared)
	require.False(t, source.Built())

	// And one that does declare one says so.
	r, _, err = p.Resource(nil, "package", "reserve_stock_code")
	require.NoError(t, err)
	require.True(t, r.Source().Declared)
}

func TestVariablesAreLiterals(t *testing.T) {
	p := load(t)

	r, _, err := p.Resource(nil, "package", "paylib")
	require.NoError(t, err)

	byName := map[string]specs.Variable{}
	for _, v := range r.Variables() {
		byName[v.Name] = v
	}
	require.Equal(t, "github.com/acme/*", byName["GOPRIVATE"].Value)
	_, handedOver := byName["GITHUB_TOKEN"]
	require.False(t, handedOver, "a secret is read through the vault, never a variable")

	// A resource that declares none reads as none rather than as a panic.
	r, _, err = p.Resource(nil, "database", "orders")
	require.NoError(t, err)
	require.Empty(t, r.Variables())
}
