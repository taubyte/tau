package secrets

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAProjectNamedCloudCannotReachTheCloudScope: the scope kind is a path
// segment of its own, so no project name spells a cloud secret's key.
func TestAProjectNamedCloudCannotReachTheCloudScope(t *testing.T) {
	_, client, _ := single(t)
	ctx := t.Context()

	cloud := Ref{Scope: Cloud("auth"), Name: "signing"}
	impostor := Ref{Scope: Project("cloud", "auth"), Name: "signing"}
	require.NoError(t, client.Set(ctx, cloud, []byte("the cloud's")))

	_, err := client.Get(ctx, impostor)
	require.ErrorIs(t, err, ErrNotSet)
	names, err := client.List(ctx, impostor.Scope)
	require.NoError(t, err)
	assert.Empty(t, names)

	require.NoError(t, client.Set(ctx, impostor, []byte("a project's")))
	got, err := client.Get(ctx, cloud)
	require.NoError(t, err)
	assert.Equal(t, "the cloud's", string(got))
	names, err = client.List(ctx, Cloud("auth"))
	require.NoError(t, err)
	assert.Equal(t, []string{"signing"}, names)

	require.ErrorIs(t, Scope{Service: "auth", Project: "p"}.Validate(), ErrInvalidRef)
	require.ErrorIs(t, Cloud("a/b").Validate(), ErrInvalidRef)
	_, err = client.List(ctx, Scope{})
	require.ErrorIs(t, err, ErrInvalidRef)
}
