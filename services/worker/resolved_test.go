package worker

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// latestBuild is what a package currently runs: cid, until a build moves it.
type latestBuild struct{ cid string }

func (b *latestBuild) Artifact(_ context.Context, _, _, _, id string) (string, error) {
	return b.cid + "@" + id, nil
}

// anySecret answers every secret.
type anySecret struct{}

func (anySecret) Secret(context.Context, string, string, string) (string, error) { return "sk", nil }

func TestATargetIsWorkedOutOncePerPointerAndItsArtifactAskedEveryTime(t *testing.T) {
	built := &latestBuild{cid: "bafy-first"}
	h := newBareHarness(t, WithArtifacts(built), WithSecrets(anySecret{}))
	target := Target{Project: "shop", Environment: "prod", Kind: specs.KindFunction, Name: "checkout"}

	first, err := h.Worker.Resolve(t.Context(), target)
	require.NoError(t, err)
	require.Contains(t, first.Artifact, "bafy-first")
	require.NotEmpty(t, first.Reaches)

	built.cid = "bafy-second"
	again, err := h.Worker.Resolve(t.Context(), target)
	require.NoError(t, err)
	require.Contains(t, again.Artifact, "bafy-second", "a build moves the artifact without moving the pointer, so it is asked each time")
	require.Same(t, &first.Reaches[0], &again.Reaches[0], "and the rest is the spec worked out under this pointer, not worked out again")

	_, err = h.Worker.Resolve(t.Context(), Target{Project: "shop", Environment: "prod", Kind: specs.KindFunction, Name: "nobody"})
	require.Error(t, err, "a target that resolves to nothing is not kept")
}

func TestAPointerThatMovedIsResolvedAgain(t *testing.T) {
	var r resolutions
	d := register.Deployment{Project: "shop", Environment: "prod"}
	k := resolvedKey{kind: "function", name: "checkout"}

	_, ok := r.get(d, "obj-1", k)
	require.False(t, ok)
	r.put(d, "obj-1", k, resolvedSpec{pkg: ranPackage{ID: "QmOne"}})
	got, ok := r.get(d, "obj-1", k)
	require.True(t, ok)
	require.Equal(t, "QmOne", got.pkg.ID)

	_, ok = r.get(d, "obj-2", k)
	require.False(t, ok, "what was worked out under another object is not this one's")
	r.put(d, "obj-2", k, resolvedSpec{pkg: ranPackage{ID: "QmTwo"}})
	_, ok = r.get(d, "obj-1", k)
	require.False(t, ok, "and the deployment's old object is let go whole")
	require.Equal(t, resolvedKey{scope: "a/b", kind: "cache", name: "c"}, keyOf(Target{Scope: []string{"a", "b"}, Kind: "cache", Name: "c"}))
}
