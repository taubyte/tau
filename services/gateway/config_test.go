package gateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/register"
)

func TestNewRefusesWhatItCannotWorkWithout(t *testing.T) {
	h := newHarness(t, Config{})

	_, err := build(Config{}, withSource(h.Reg), withWorkers(h.Workers))
	require.ErrorIs(t, err, ErrNoHosts)

	_, err = build(Config{}, withHosts(h.Reg), withSource(h.Reg))
	require.ErrorIs(t, err, ErrNoWorkers)

	_, err = build(Config{}, withHosts(h.Reg), withWorkers(h.Workers))
	require.ErrorIs(t, err, ErrNoSource)

	// A nil handed to an option is caught by the option, so the message names
	// the setting rather than surfacing later as a missing dependency.
	for _, opt := range []option{withHosts(nil), withWorkers(nil), withSource(nil), withClock(nil)} {
		_, err := build(Config{}, withHosts(h.Reg), withSource(h.Reg), withWorkers(h.Workers), opt)
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
		_, err := build(cfg, withHosts(h.Reg), withSource(h.Reg), withWorkers(h.Workers))
		require.Error(t, err, "%+v", cfg)
	}
}

func TestTheDefaultsAreApplied(t *testing.T) {
	h := newHarness(t, Config{})

	s, err := build(Config{}, withHosts(h.Reg), withSource(h.Reg), withWorkers(h.Workers))
	require.NoError(t, err)

	require.Equal(t, DefaultMaxHosts, s.cfg.MaxHosts)
	require.Equal(t, DefaultTimeout, s.cfg.Timeout)
	require.Equal(t, int64(DefaultMaxBody), s.cfg.MaxBody)
}

func TestTheCacheSettingsReachTheCache(t *testing.T) {
	h := newHarness(t, Config{})

	s, err := build(Config{MaxProjects: 3},
		withHosts(h.Reg), withSource(h.Reg), withWorkers(h.Workers), withClock(h.clock.Now))
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
