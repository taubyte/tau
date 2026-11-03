//go:build dreaming

package dream

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuiltFallsBackToTheBuildService(t *testing.T) {
	b := &Built{fallback: artifactsFunc(func(_ context.Context, project, _, _, resource string) (string, error) {
		if resource == "QmBuilt" {
			return "bafybuilt", nil
		}
		return "", errors.New("nothing has been built for " + resource)
	})}
	b.Publish("shop", "prod", map[string]string{"QmStaged": "bafystaged"})

	cid, err := b.Artifact(t.Context(), "shop", "prod", "function", "QmStaged")
	require.NoError(t, err)
	require.Equal(t, "bafystaged", cid)

	cid, err = b.Artifact(t.Context(), "shop", "prod", "function", "QmBuilt")
	require.NoError(t, err)
	require.Equal(t, "bafybuilt", cid)

	_, err = b.Artifact(t.Context(), "shop", "prod", "function", "QmNeither")
	require.ErrorContains(t, err, "nothing was staged")
	require.ErrorContains(t, err, "nothing has been built for QmNeither")
}

type artifactsFunc func(ctx context.Context, project, environment, kind, resource string) (string, error)

func (f artifactsFunc) Artifact(ctx context.Context, project, environment, kind, resource string) (string, error) {
	return f(ctx, project, environment, kind, resource)
}
