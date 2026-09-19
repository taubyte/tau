package boot

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
)

// A COMMAND IS NOT A NODE, but it has to talk to one, and the only way into a
// private swarm is to join it. Reach is that: a stats-only node in a directory
// it owns, held open just long enough to find who runs what.
func TestReachFindsAServiceInTheFleet(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(swarmKey())

	fleet, err := Start(t.Context(), File{
		Node:     node.Config{Services: []string{node.Register, node.Stats}, Cloud: "acme"},
		Root:     t.TempDir(),
		Listen:   []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey: key,
	})
	require.NoError(t, err)
	defer fleet.Close()

	require.NotNil(t, fleet.Host(), "a caller that has to dial needs the host")

	client, err := Reach(t.Context(), File{
		Node:      node.Config{Cloud: "acme"},
		Listen:    []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey:  key,
		Bootstrap: addresses(t, fleet),
	}, node.Register, 30*time.Second)
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

// A service nobody runs is a wait that ends, and says why.
func TestReachGivesUp(t *testing.T) {
	_, err := Reach(t.Context(), File{
		Listen:   []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey: base64.StdEncoding.EncodeToString(swarmKey()),
	}, node.Worker, 500*time.Millisecond)
	require.ErrorContains(t, err, node.Worker)
	require.ErrorContains(t, err, "swarm key")
}

func TestReachRefusesTheSameFileStartRefuses(t *testing.T) {
	_, err := Reach(t.Context(), File{Root: t.TempDir()}, node.Register, time.Second)
	require.ErrorIs(t, err, ErrNoSwarmKey)
}

// AN ADDRESS THAT DOES NOT NAME WHO IS ON IT is the operator mistake this
// catches: /ip4/…/tcp/… is a socket, and on a private swarm the peer id is the
// whole question. Dropping it silently produces a node that finds nobody and
// says nothing about why.
func TestBootstrapAddressesNameAPeer(t *testing.T) {
	_, err := Start(t.Context(), File{
		Root:      t.TempDir(),
		SwarmKey:  base64.StdEncoding.EncodeToString(swarmKey()),
		Bootstrap: []string{"/ip4/127.0.0.1/tcp/4001"},
	})
	require.ErrorContains(t, err, "bootstrap")

	_, err = Start(t.Context(), File{
		Root:      t.TempDir(),
		SwarmKey:  base64.StdEncoding.EncodeToString(swarmKey()),
		Bootstrap: []string{"not-an-address"},
	})
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

// TestReachStopsWhenTheCallerDoes. The wait is the CLI's, and a Ctrl-C during
// it has to take the temporary node with it — a client that kept joining the
// swarm after its process was told to stop is a peer nobody can account for.
func TestReachStopsWhenTheCallerDoes(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	_, err := Reach(ctx, File{
		Listen:   []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey: base64.StdEncoding.EncodeToString(swarmKey()),
	}, node.Worker, time.Minute)
	require.ErrorIs(t, err, context.Canceled)
}
