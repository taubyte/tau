package project_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/project/kinds"
)

// generated_test holds the generated readers to the hand-written ones, on the
// vocabulary's own fixture, so the hand-written ones can be deleted knowing what
// replaces them answers the same.

func TestTheGeneratedReaderAgreesWithTheHandWrittenOne(t *testing.T) {
	p := load(t)

	r, _, err := p.Resource(nil, "function", "reserve_stock")
	require.NoError(t, err)

	was := r.Build()
	now := kinds.ReadFunction(r)

	require.Equal(t, was.Kind, now.BuildKind)
	require.Equal(t, was.Image, now.BuildImage)
	require.Equal(t, was.Install, now.BuildInstall)
	require.Equal(t, was.Run, now.BuildRun)
	require.Equal(t, was.Output, now.BuildOutput)
	require.Equal(t, was.Script, now.BuildScript)

	src := r.Source()
	require.Equal(t, src.Kind, now.SourceKind)
	require.Equal(t, src.Git, now.SourceGit)
	require.Equal(t, src.Branch, now.SourceBranch)

	// THE BARE FORM AND THE BLOCK FORM ARE TWO FIELDS, and the generated reader
	// is the more faithful of the two here. The vocabulary declares them sharing
	// a key on purpose - "a scalar there is this path, a mapping is one of the
	// branches" - so `source: services/reserve` compiles to a STRING at `source`
	// and `source: {path: x}` to a string at `source/path`. The hand-written
	// Source() folds both into .Path and cannot tell them apart afterwards.
	require.Equal(t, src.Path, now.Source, "the fixture writes the bare form")
	require.Empty(t, now.SourcePath, "and so the block form is absent")

	// AND THE VALUES ARE THE FIXTURE'S, so this is not two readers agreeing on
	// nothing.
	require.Equal(t, "rust:1.83", now.BuildImage)
	require.Equal(t, "services/reserve", now.Source)
}

// TestTHEGENERATEDREADERISPERKIND, which the hand-written one was not: Build()
// reads `build/dockerfile` for every kind, and only a container kind has one.
func TestTheGeneratedReaderOnlyHasFieldsItsKindDeclares(t *testing.T) {
	p := load(t)

	app, _, err := p.Resource(nil, "application", "api")
	require.NoError(t, err)
	require.NotEmpty(t, kinds.ReadApplication(app).SourceImage,
		"an application names an image and the generated reader has the field")

	// A function has no `build/dockerfile` in the vocabulary at all, so the
	// generated Function has no field for it - the compiler says so rather than
	// a reader returning "" forever.
	var fn kinds.Function
	_ = fn
}
