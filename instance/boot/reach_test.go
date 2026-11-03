package boot

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
)

func TestReachFindsAServiceInTheFleet(t *testing.T) {
	fleet, err := Start(t.Context(), admit(t, File{
		Node:   instance.Config{Services: []string{instance.Register, instance.Discovery}, Cloud: "acme"},
		Root:   t.TempDir(),
		Listen: []string{"/ip4/127.0.0.1/tcp/0"},
	}))
	require.NoError(t, err)
	defer fleet.Close()

	require.NotNil(t, fleet.Host(), "a caller that has to dial needs the host")

	client, err := Reach(t.Context(), admit(t, File{
		Node:      instance.Config{Cloud: "acme"},
		Listen:    []string{"/ip4/127.0.0.1/tcp/0"},
		Bootstrap: addresses(t, fleet),
	}), instance.Register, 30*time.Second)
	require.NoError(t, err)
	defer client.Close()

	// It answers with what it found, not with a promise that a second lookup
	// will find it again: the view has a freshness window, and re-asking can
	// come back empty for a node that is still there.
	require.NotEmpty(t, client.Found)
	require.Equal(t, fleet.Host().ID().String(), client.Found[0].Peer)

	// A CLIENT LEAVES NOTHING BEHIND. Its data directory is its own and goes
	// with it, so running one never touches what a node on this box holds.
	require.NotEmpty(t, client.temporary)
	require.NoError(t, client.Close())
	require.NoDirExists(t, client.temporary)
}

func TestReachGivesUp(t *testing.T) {
	_, err := Reach(t.Context(), admit(t, File{
		Listen: []string{"/ip4/127.0.0.1/tcp/0"},
	}), instance.Worker, 500*time.Millisecond)
	require.ErrorContains(t, err, instance.Worker)
	require.ErrorContains(t, err, "certificate")
}

func TestReachRefusesTheSameFileStartRefuses(t *testing.T) {
	_, err := Reach(t.Context(), File{Root: t.TempDir()}, instance.Register, time.Second)
	require.ErrorIs(t, err, ErrNoCertificate)
}

func TestBootstrapAddressesNameAPeer(t *testing.T) {
	_, err := Start(t.Context(), admit(t, File{
		Root:      t.TempDir(),
		Bootstrap: []string{"/ip4/127.0.0.1/tcp/4001"},
	}))
	require.ErrorContains(t, err, "bootstrap")

	_, err = Start(t.Context(), admit(t, File{
		Root:      t.TempDir(),
		Bootstrap: []string{"not-an-address"},
	}))
	require.ErrorContains(t, err, "bootstrap")
}

func addresses(t *testing.T, r *Running) []string {
	t.Helper()

	var out []string
	for _, a := range r.Host().Addrs() {
		out = append(out, a.String()+"/p2p/"+r.Host().ID().String())
	}
	require.NotEmpty(t, out)
	return out
}

func TestReachStopsWhenTheCallerDoes(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	_, err := Reach(ctx, admit(t, File{
		Listen: []string{"/ip4/127.0.0.1/tcp/0"},
	}), instance.Worker, time.Minute)
	require.ErrorIs(t, err, context.Canceled)
}
