package services

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/discovery"
	"github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/secrets"
	"github.com/taubyte/tau/services/storage"
)

func TestANameIsTheServiceItRuns(t *testing.T) {
	require.Equal(t, []string{discovery.Descriptor.Name, register.Descriptor.Name, secrets.Descriptor.Name, storage.Descriptor.Name}, Names())

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
