package services

import (
	"testing"

	"github.com/taubyte/tau/services/build"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/consensus"
	"github.com/taubyte/tau/services/database"
	"github.com/taubyte/tau/services/discovery"
	"github.com/taubyte/tau/services/primitives"
	"github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/secrets"
	"github.com/taubyte/tau/services/storage"
)

func TestANameIsTheServiceItRuns(t *testing.T) {
	require.Equal(t, []string{consensus.Descriptor.Name, discovery.Descriptor.Name, register.Descriptor.Name, secrets.Descriptor.Name, primitives.Descriptor.Name, database.Descriptor.Name, storage.Descriptor.Name, build.Runner.Name, build.Build.Name}, Names(), "in start order")
	require.Equal(t, []string{consensus.Descriptor.Name, discovery.Descriptor.Name, register.Descriptor.Name, secrets.Descriptor.Name, primitives.Descriptor.Name, database.Descriptor.Name, storage.Descriptor.Name, build.Runner.Name}, Default(),
		"every one but build, which may not run beside the runner")

	s, ok := New(storage.Descriptor.Name)
	require.True(t, ok)
	_, isStorage := s.(storage.Service)
	require.True(t, isStorage)

	for _, name := range Names() {
		created, ok := New(name)
		require.True(t, ok, name)
		require.NotNil(t, created, name)
	}

	again, _ := New(storage.Descriptor.Name)
	require.NotSame(t, s, again, "each instance its own")

	_, ok = New("nothing")
	require.False(t, ok)
}

func TestAServiceNeedsWhatItCannotStartWithout(t *testing.T) {
	require.Equal(t, []string{consensus.Descriptor.Name}, Needs(primitives.Descriptor.Name))
	require.Equal(t, []string{consensus.Descriptor.Name}, Needs(database.Descriptor.Name))
	require.Equal(t, []string{consensus.Descriptor.Name}, Needs(build.Runner.Name), "a build queue is a raft group")
	require.Empty(t, Needs(consensus.Descriptor.Name))
	require.Empty(t, Needs("nothing"))
}
