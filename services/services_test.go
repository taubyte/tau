package services

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/storage"
)

func TestANameIsTheServiceItRuns(t *testing.T) {
	require.Equal(t, []string{storage.Descriptor.Name}, Names())

	s, ok := New(storage.Descriptor.Name)
	require.True(t, ok)
	_, isStorage := s.(storage.Service)
	require.True(t, isStorage)

	again, _ := New(storage.Descriptor.Name)
	require.NotSame(t, s, again, "each instance its own")

	_, ok = New("nothing")
	require.False(t, ok)
}
