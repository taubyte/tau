package services

import (
	"testing"

	"github.com/taubyte/tau/services/plm"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/database"
	"github.com/taubyte/tau/services/dns"
	"github.com/taubyte/tau/services/primitives"
	"github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/secrets"
	statsService "github.com/taubyte/tau/services/stats"
	"github.com/taubyte/tau/services/storage"
)

func TestANameIsTheServiceItRuns(t *testing.T) {
	shipped := []string{cm.Name, statsService.Descriptor.Name, register.Descriptor.Name, secrets.Descriptor.Name, auth.Descriptor.Name,
		primitives.Descriptor.Name, database.Descriptor.Name, storage.Descriptor.Name, plm.Builder.Name, dns.Name}
	require.Equal(t, append(append([]string(nil), shipped...), plm.PLM.Name), Names(), "in start order")
	require.Equal(t, shipped, Default(), "every one but plm, which listens where its config says and has no default")

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
	require.Equal(t, []string{cm.Name}, Needs(primitives.Descriptor.Name))
	require.Equal(t, []string{cm.Name}, Needs(database.Descriptor.Name))
	require.Equal(t, []string{cm.Name}, Needs(plm.PLM.Name), "a build queue is a raft group")
	require.Empty(t, Needs(plm.Builder.Name), "a builder holds no state")
	require.Equal(t, []string{statsService.Descriptor.Name}, Needs(dns.Name), "dns answers from the stats view")
	require.Empty(t, Needs(cm.Name))
	require.Empty(t, Needs("nothing"))
}
