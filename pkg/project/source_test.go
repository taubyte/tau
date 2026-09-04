package project_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/project"
)

// A SOURCE IS AN EITHER AND SO IS A BUILD, and reading one is a switch on the
// `kind` the compiler flattens beside the branch's own fields — never a test
// for which fields happen to be present.

// THE BARE PATH IS A STRING WHERE THE OTHERS ARE OBJECTS, which is the
// vocabulary letting the common case be written the short way. Reading it as an
// object would find nothing and report a resource with no source at all.
func TestABarePathIsAPathInTheProjectsOwnRepository(t *testing.T) {
	p := load(t)

	r, _, err := p.Resource(nil, "function", "reserve_stock")
	require.NoError(t, err)

	source := r.Source()
	require.Equal(t, project.SourcePath, source.Kind)
	require.Equal(t, "services/reserve", source.Path)
	require.Empty(t, source.Git)
	require.True(t, source.Built(), "a path in a repository is something this platform makes")

	build := r.Build()
	require.True(t, build.Declared())
	require.Equal(t, project.BuildCommands, build.Kind)
	require.Equal(t, "rust:1.83", build.Image)
	require.Equal(t, "cargo fetch --locked", build.Install)
	require.Equal(t, "cargo build --release --target wasm32-wasip2", build.Run)
	require.Equal(t, "target/wasm32-wasip2/release/reserve.wasm", build.Output)
}

// THE URL IS THE PROVIDER. There is no `github:` key and no numeric id: the
// clone URL carries the host, so GitLab, Gitea, Codeberg and an internal mirror
// are values rather than schema edits.
func TestAResourceCanNameItsOwnRepository(t *testing.T) {
	p := load(t)

	r, _, err := p.Resource(nil, "component", "paylib")
	require.NoError(t, err)

	source := r.Source()
	require.Equal(t, project.SourceGit, source.Kind)
	require.Equal(t, "https://github.com/acme/paylib", source.Git)
	require.Equal(t, "main", source.Branch)
	require.True(t, source.Built())

	// A script is the escape hatch for a build that does not fit two commands.
	build := r.Build()
	require.Equal(t, project.BuildScript, build.Kind)
	require.Equal(t, "taubyte/go-wasi:v2", build.Image)
	require.Equal(t, ".taubyte/build.sh", build.Script)
	require.Equal(t, "paylib.wasm", build.Output)
	require.Empty(t, build.Run, "a script build declares no commands")
}

// AN IMAGE IS PULLED AND A COMPONENT IS BUILT AS ITS OWN RESOURCE, so neither
// is something a push to a code repository rebuilds.
func TestWhatIsNotBuiltFromSource(t *testing.T) {
	p := load(t)

	r, _, err := p.Resource(nil, "function", "checkout")
	require.NoError(t, err)
	source := r.Source()
	require.Equal(t, project.SourceComponent, source.Kind)
	// A REFERENCE COMPILES TO AN ID, not to the name it was written as — which
	// is what makes a rename of the component not a rewrite of everything that
	// names it.
	require.Equal(t, "QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv", source.Component)
	require.False(t, source.Built())

	// An application runs an image somebody else made.
	r, _, err = p.Resource(nil, "application", "db")
	require.NoError(t, err)
	source = r.Source()
	require.Equal(t, project.SourceImage, source.Kind)
	require.False(t, source.Built())

	// A resource with no source at all reads as one, rather than as a path of
	// "".
	r, _, err = p.Resource(nil, "database", "orders")
	require.NoError(t, err)
	require.False(t, r.Source().Built() && r.Build().Declared())
}

// WHAT A PUSH REBUILDS IS EVERY RESOURCE THE PROJECT MAKES FROM SOURCE, and the
// caller filters by which repository it was — because which repository a bare
// path belongs to lives in auth, not in the config.
func TestBuildablesAreEveryResourceMadeFromSource(t *testing.T) {
	p := load(t)

	built := p.Buildables()
	require.NotEmpty(t, built)

	byName := map[string]project.Buildable{}
	for _, b := range built {
		byName[b.Kind+"/"+b.Name] = b

		// Everything here is buildable and says how.
		require.True(t, b.Source.Built(), b.Name)
		require.True(t, b.Build.Declared(), b.Name)
		require.NotEmpty(t, b.ID, b.Name)
	}

	require.Contains(t, byName, "function/reserve_stock")
	require.Contains(t, byName, "component/paylib")
	require.Contains(t, byName, "website/shop")

	// A function whose source is a component is NOT rebuilt: the component is,
	// as its own resource.
	require.NotContains(t, byName, "function/checkout")
	// Nor is an application running a pulled image.
	require.NotContains(t, byName, "application/db")

	// THE ORDER IS STABLE, because the source is a map and a build order that
	// changed between two pushes of one commit is one nobody can read a log of.
	for i := range 5 {
		require.Equal(t, built, load(t).Buildables(), "run %d", i)
	}
}

// A resource that names a repository is separable from one that does not,
// which is the whole of what a caller needs to decide whether a given push
// touches it.
func TestABuildableSaysWhichRepositoryItNeeds(t *testing.T) {
	byName := map[string]project.Buildable{}
	for _, b := range load(t).Buildables() {
		byName[b.Kind+"/"+b.Name] = b
	}

	own := byName["function/reserve_stock"]
	require.Empty(t, own.Source.Git, "a bare path is the project's own code repository")
	require.Equal(t, "services/reserve", own.Source.Path)

	elsewhere := byName["component/paylib"]
	require.Equal(t, "https://github.com/acme/paylib", elsewhere.Source.Git)
}
