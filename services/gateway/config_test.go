package gateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/register"
)

func TestNewRefusesWhatItCannotWorkWithout(t *testing.T) {
	h := newHarness(t, Config{})

	_, err := New(Config{}, WithSource(h.Reg), WithWorkers(h.Workers))
	require.ErrorIs(t, err, ErrNoHosts)

	_, err = New(Config{}, WithHosts(h.Reg), WithSource(h.Reg))
	require.ErrorIs(t, err, ErrNoWorkers)

	_, err = New(Config{}, WithHosts(h.Reg), WithWorkers(h.Workers))
	require.ErrorIs(t, err, ErrNoSource)

	// A nil handed to an option is caught by the option, so the message names
	// the setting rather than surfacing later as a missing dependency.
	for _, opt := range []Option{WithHosts(nil), WithWorkers(nil), WithSource(nil), WithClock(nil)} {
		_, err := New(Config{}, WithHosts(h.Reg), WithSource(h.Reg), WithWorkers(h.Workers), opt)
		require.Error(t, err)
	}
}

func TestTheConfigRefusesNumbersThatMeanNothing(t *testing.T) {
	h := newHarness(t, Config{})

	for _, cfg := range []Config{
		{MaxHosts: -1},
		{Timeout: -time.Second},
		{MaxBody: -1},
		{MaxProjects: -1},
	} {
		_, err := New(cfg, WithHosts(h.Reg), WithSource(h.Reg), WithWorkers(h.Workers))
		require.Error(t, err, "%+v", cfg)
	}
}

func TestTheDefaultsAreApplied(t *testing.T) {
	h := newHarness(t, Config{})

	s, err := New(Config{}, WithHosts(h.Reg), WithSource(h.Reg), WithWorkers(h.Workers))
	require.NoError(t, err)

	require.Equal(t, DefaultMaxHosts, s.cfg.MaxHosts)
	require.Equal(t, DefaultTimeout, s.cfg.Timeout)
	require.Equal(t, int64(DefaultMaxBody), s.cfg.MaxBody)
}

func TestTheCacheSettingsReachTheCache(t *testing.T) {
	h := newHarness(t, Config{})

	s, err := New(Config{MaxProjects: 3},
		WithHosts(h.Reg), WithSource(h.Reg), WithWorkers(h.Workers), WithClock(h.clock.Now))
	require.NoError(t, err)

	require.Zero(t, s.Held())
}

func TestThisIsThePublicService(t *testing.T) {
	require.Equal(t, IngressPublic, Descriptor.Ingress)
	require.Equal(t, "gateway", Descriptor.Name)
}

func TestAWorkersInterfaceIsAllItNeeds(t *testing.T) {
	var _ Workers = (*recordingWorkers)(nil)
	var _ register.Registry = (*fakeSource)(nil)
	var _ Hosts = brokenHosts{}
}

type fakeSource struct{ register.Registry }
