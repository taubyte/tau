package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

// artifact_test is about the one function that decides WHAT RUNS.
//
// It had no test. That is how a component reference shipped resolving to a
// configuration document id: `source.component` is declared `Ref("component")`,
// so a compiled resource carries the referenced component's own RESOURCE id, and
// this function returned it verbatim as the artifact. The tier then asked the
// asset store for bytes filed under an id the store has never held.
//
// The corpus could not catch it, and still cannot: `node/dream/project_test.go`
// rewrites every `source` to a real asset CID before publishing, so
// `kind: component` is only ever exercised with something that IS an artifact —
// never with what an author writes.
//
// The same switch already carried this bug's post-mortem, one case further down:
// the builtin arm exists because falling through to the artifact seam "asked the
// build service for the output of" a program that was never built, "which is why
// every workflow this compiler produced resolved to ErrNoArtifact." Same
// function, same class, opposite direction.

// asked records what the artifact seam was asked about.
type asked struct {
	project     string
	environment string
	kind        string
	id          string
	answer      string
	err         error
	calls       int
}

func (a *asked) Artifact(_ context.Context, project, environment, kind, id string) (string, error) {
	a.calls++
	a.project, a.environment, a.kind, a.id = project, environment, kind, id
	if a.err != nil {
		return "", a.err
	}
	return a.answer, nil
}

func resolving(seam *asked) *Service {
	s := &Service{}
	if seam != nil {
		s.artifacts = seam
	}
	return s
}

var aWorkload = Target{Project: "shop", Environment: "prod", Kind: "function", Name: "checkout"}

// TestACOMPONENTREFERENCEISRESOLVEDANDNOTRETURNED. The component's id is asked
// ABOUT, not answered WITH -- and it is asked about as a `component`, because the
// artifact was published under the component's own id and not under the id of
// whichever workload happens to name it.
func TestAComponentReferenceGoesThroughTheArtifactSeam(t *testing.T) {
	seam := &asked{answer: "bafyrealartifact"}

	got, err := resolving(seam).artifactOf(context.Background(), aWorkload,
		specs.Resource{"source": map[string]any{"kind": "component", "component": "QmComponentResourceID"}},
		"QmTheWorkloadsOwnID")
	require.NoError(t, err)

	require.Equal(t, "bafyrealartifact", got,
		"what the store holds, not the id of the thing that names it")
	require.Equal(t, 1, seam.calls, "the seam was asked")
	require.Equal(t, "QmComponentResourceID", seam.id,
		"asked about the COMPONENT -- the artifact is published under its id")
	require.Equal(t, "component", seam.kind)
	require.Equal(t, "shop", seam.project)
	require.Equal(t, "prod", seam.environment)
}

// TestTheWorkloadsOwnIdIsNotWhatIsAsked states the half above that is easiest to
// get wrong: `resourceID` is the workload's, and passing it here would look right
// and resolve to the wrong build.
func TestAComponentIsNotResolvedByTheNamingResourcesId(t *testing.T) {
	seam := &asked{answer: "bafy"}

	_, err := resolving(seam).artifactOf(context.Background(), aWorkload,
		specs.Resource{"source": map[string]any{"kind": "component", "component": "QmComponentResourceID"}},
		"QmTheWorkloadsOwnID")
	require.NoError(t, err)
	require.NotEqual(t, "QmTheWorkloadsOwnID", seam.id)
}

// TestANAMELESSCOMPONENTISREFUSEDBYNAME rather than asked about as "".
func TestAnEmptyComponentReferenceIsRefused(t *testing.T) {
	seam := &asked{answer: "bafy"}

	_, err := resolving(seam).artifactOf(context.Background(), aWorkload,
		specs.Resource{"source": map[string]any{"kind": "component"}}, "QmWorkload")
	require.ErrorIs(t, err, ErrNoArtifact)
	require.ErrorContains(t, err, "names no component")
	require.Zero(t, seam.calls, "nothing was asked about nothing")
}

// TestNOSEAMISREFUSEDANDNAMESTHECOMPONENT. A node that resolves no artifacts
// cannot run a component, and the refusal says which one so an operator is not
// left guessing which resource stalled.
func TestAComponentWithNoArtifactSeamIsRefused(t *testing.T) {
	_, err := resolving(nil).artifactOf(context.Background(), aWorkload,
		specs.Resource{"source": map[string]any{"kind": "component", "component": "QmComponentResourceID"}},
		"QmWorkload")
	require.ErrorIs(t, err, ErrNoArtifact)
	require.ErrorContains(t, err, "QmComponentResourceID")
}

// TestASEAMFAILUREISNOTSWALLOWED -- an unbuilt component must not resolve to "".
func TestAFailingSeamRefusesTheComponent(t *testing.T) {
	boom := errors.New("no artifact for that resource")

	_, err := resolving(&asked{err: boom}).artifactOf(context.Background(), aWorkload,
		specs.Resource{"source": map[string]any{"kind": "component", "component": "QmComponentResourceID"}},
		"QmWorkload")
	require.ErrorIs(t, err, boom)
}

// TestTHEOTHERARMSAREUNCHANGED, which is what says this fix moved one case and
// not the switch. An image is its own reference, a builtin has no artifact at
// all, and anything else is the workload's own build.
func TestTheOtherSourceKindsResolveAsTheyDid(t *testing.T) {
	t.Run("an image is its own reference and asks nothing", func(t *testing.T) {
		seam := &asked{answer: "unused"}
		got, err := resolving(seam).artifactOf(context.Background(), aWorkload,
			specs.Resource{"source": map[string]any{"kind": "image", "image": "ghcr.io/acme/db:16"}}, "QmWorkload")
		require.NoError(t, err)
		require.Equal(t, "ghcr.io/acme/db:16", got)
		require.Zero(t, seam.calls)
	})

	t.Run("a builtin has no artifact and asks nothing", func(t *testing.T) {
		seam := &asked{answer: "unused"}
		got, err := resolving(seam).artifactOf(context.Background(), aWorkload,
			specs.Resource{"source": map[string]any{"kind": sourceBuiltin, sourceBuiltin: "step"}}, "QmWorkload")
		require.NoError(t, err)
		require.Empty(t, got, "a builtin is shipped by the runtime; asking for one is the bug")
		require.Zero(t, seam.calls)
	})

	t.Run("anything built is asked about by the workload's own id", func(t *testing.T) {
		seam := &asked{answer: "bafybuilt"}
		got, err := resolving(seam).artifactOf(context.Background(), aWorkload,
			specs.Resource{"source": map[string]any{"kind": "git", "git": "https://github.com/acme/app"}},
			"QmTheWorkloadsOwnID")
		require.NoError(t, err)
		require.Equal(t, "bafybuilt", got)
		require.Equal(t, "QmTheWorkloadsOwnID", seam.id, "its own build, under its own id")
		require.Equal(t, "function", seam.kind, "and under its own kind")
	})

	t.Run("no seam at all is refused naming the resource", func(t *testing.T) {
		_, err := resolving(nil).artifactOf(context.Background(), aWorkload,
			specs.Resource{"source": map[string]any{"kind": "git"}}, "QmWorkload")
		require.ErrorIs(t, err, ErrNoArtifact)
		require.ErrorContains(t, err, "function/checkout")
	})
}
