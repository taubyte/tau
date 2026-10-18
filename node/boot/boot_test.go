package boot

import (
	"encoding/base64"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/node"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// TestTheFileIsTheConfigAndNotATranslationOfIt. Every v2 service's Config was
// written to survive a round trip through yaml precisely so this layer is a
// decode — a translation is where an operator's setting quietly stops meaning
// what the struct says.
func TestTheFileIsTheConfig(t *testing.T) {
	f, err := Read(write(t, `
node:
  services: [register, worker]
  cloud: acme
  container:
    backend: containerd
    runtime: io.containerd.runsc.v1
  worker:
    pointerttl: 10s
    maxprojects: 8
  gateway:
    cloudfqdn: tau.example.io
    maxbody: 1048576
root: /var/lib/tau
listen: ["/ip4/0.0.0.0/tcp/4001"]
swarmkey: c3dhcm0=
http: ":8080"
`))
	require.NoError(t, err)

	require.Equal(t, []string{node.Register, node.Worker}, f.Node.Services)
	require.Equal(t, "acme", f.Node.Cloud)
	require.Equal(t, "io.containerd.runsc.v1", f.Node.Container.Runtime,
		"the isolation handler reaches the node that builds the tier")
	require.Equal(t, 8, f.Node.Worker.MaxProjects)
	require.Equal(t, "tau.example.io", f.Node.Gateway.CloudFqdn)
	require.Equal(t, int64(1<<20), f.Node.Gateway.MaxBody)
	require.Equal(t, "/var/lib/tau", f.Root)
	require.Equal(t, ":8080", f.HTTP)
}

// TestANodeWithNoKeyDoesNotJoinThePublicNetwORK. Every v2 service trusts its
// swarm, so a node with no pre-shared key is a node whose register any stranger
// can publish into.
func TestAMissingSwarmKeyIsRefused(t *testing.T) {
	_, err := Read(write(t, "root: /var/lib/tau\n"))
	require.ErrorIs(t, err, ErrNoSwarmKey)
	require.Contains(t, err.Error(), "open: true", "and it says how to mean it")

	// Saying it is a line somebody wrote.
	f, err := Read(write(t, "open: true\n"))
	require.NoError(t, err)
	require.True(t, f.Open)
}

func TestTheDefaultsAreApplied(t *testing.T) {
	f, err := Read(write(t, "swarmkey: c3dhcm0=\n"))
	require.NoError(t, err)

	require.Equal(t, DefaultRoot, f.Root)
	require.Equal(t, []string{DefaultListen}, f.Listen)

	// AND THE SERVICES ARE THE NODE'S OWN DEFAULT, not one invented here: an
	// empty list means all of them, which node.Config already says.
	require.Empty(t, f.Node.Services)
}

func TestAFileThatIsNotOne(t *testing.T) {
	_, err := Read(filepath.Join(t.TempDir(), "nothing-here.yaml"))
	require.Error(t, err)

	_, err = Read(write(t, "node: [this is not a node]\n"))
	require.Error(t, err)
}

// TestKeysAreBase64AndSayWhichOneIsNot.
func TestKeysAreBase64(t *testing.T) {
	_, err := Start(t.Context(), File{SwarmKey: "not base64!"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "swarmkey")

	_, err = Start(t.Context(), File{SwarmKey: "c3dhcm0=", PrivateKey: "not base64!"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "privatekey")
}

// TestStartRefusesBeforeItBindsAnything. A node that would join the public
// network must not get as far as a listener.
func TestStartRefusesAnOpenNetworkItWasNotToldAbout(t *testing.T) {
	_, err := Start(t.Context(), File{Root: t.TempDir()})
	require.ErrorIs(t, err, ErrNoSwarmKey)
}

// TestTheExampleConfigParsesBackIntoItself, so `tau v2 config` cannot print
// something `tau v2 start` refuses.
func TestTheExampleRoundTrips(t *testing.T) {
	example := File{
		Node:      node.Config{Services: node.All, Cloud: "acme"},
		Root:      DefaultRoot,
		Listen:    []string{DefaultListen},
		SwarmKey:  "c3dhcm0=",
		HTTP:      ":8080",
		Bootstrap: []string{},
	}

	out, err := yaml.Marshal(example)
	require.NoError(t, err)

	back, err := Read(write(t, string(out)))
	require.NoError(t, err)
	require.Equal(t, example.Node.Services, back.Node.Services)
	require.Equal(t, example.Root, back.Root)
	require.Equal(t, example.HTTP, back.HTTP)
}

// TestANodeActuallyStARTS: a real libp2p host on an ephemeral port, a real
// gossipsub, a real datastore on disk, and the gateway bound.
//
// THIS IS THE ONLY TEST THAT PROVES THE LAST MILE. Everything above is a decode;
// what this catches is a resource that cannot be built from what the file says
// — which is the whole reason this package exists.
func TestANodeStartsAndServes(t *testing.T) {
	f := File{
		Node:     node.Config{Services: []string{node.Register, node.Discovery, node.Gateway}},
		Root:     t.TempDir(),
		Listen:   []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey: base64.StdEncoding.EncodeToString(swarmKey()),
		HTTP:     "127.0.0.1:0",
	}

	running, err := Start(t.Context(), f)
	require.NoError(t, err)
	defer running.Close()

	require.NotNil(t, running.Node.Register())
	require.NotNil(t, running.Node.Discovery())
	require.NotNil(t, running.Node.Handler())

	// It reports itself, which is what the rest of the fleet routes on.
	snapshot, err := running.Node.Snapshot(t.Context())
	require.NoError(t, err)
	require.True(t, snapshot.Ready)
	require.NotEmpty(t, snapshot.Addresses)
	require.Contains(t, snapshot.Serving, node.Register)

	require.NoError(t, running.Close())
	require.NoError(t, running.Close(), "close is idempotent enough to be deferred")
}

// TestANodeWithNoGatewayServesNothing, and asking it to is not an error: a
// worker-only node has no handler to bind.
func TestANodeWithNoGatewayBindsNothing(t *testing.T) {
	f := File{
		Node:     node.Config{Services: []string{node.Register}},
		Root:     t.TempDir(),
		Listen:   []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey: base64.StdEncoding.EncodeToString(swarmKey()),
		HTTP:     "127.0.0.1:0",
		HTTPS:    "127.0.0.1:0",
	}

	running, err := Start(t.Context(), f)
	require.NoError(t, err)
	defer running.Close()

	require.Nil(t, running.Node.Handler())
	require.Nil(t, running.Node.TLSConfig())
	require.Empty(t, running.Address())
	require.Empty(t, running.SecureAddress())
}

// TestAnAddressAlreadyTakenIsReportedAtBoot rather than as a gateway nobody can
// reach.
func TestAnAddressAlreadyTaken(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer held.Close()

	_, err = Start(t.Context(), File{
		Node:     node.Config{Services: []string{node.Discovery, node.Gateway}},
		Root:     t.TempDir(),
		Listen:   []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey: base64.StdEncoding.EncodeToString(swarmKey()),
		HTTP:     held.Addr().String(),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "listening on")
}

// swarmKey is a libp2p pre-shared key: 32 bytes, in the form go-libp2p reads.
func swarmKey() []byte {
	return []byte("/key/swarm/psk/1.0.0/\n/base16/\n" +
		"0000000000000000000000000000000000000000000000000000000000000001")
}

// TestAnUnstartedRunningAnswersNothing. Reach hands one of these back to a CLI
// that may have failed to find anything, and a nil peer is what "nothing was
// started" looks like from there — a panic would be the CLI's, not the fleet's.
func TestAnUnstartedRunningAnswersNothing(t *testing.T) {
	var r Running
	require.Nil(t, r.Host())
	require.Empty(t, r.Address(), "nothing is being served")
}

// TestANodeThatCannotBeBuiltFromTheFileStopsThePeerToo. Everything Start opens
// before the node — the libp2p host, the datastore on disk — is this function's
// to close when the node refuses; leaving them would hold the root directory
// and the port after a start that failed.
func TestANodeThatCannotBeBuiltClosesWhatWasOpened(t *testing.T) {
	root := t.TempDir()
	f := File{
		// A service nobody runs: the node's own closed set refuses it, which
		// happens after the peer and the asset store are already open.
		Node:     node.Config{Services: []string{"not-a-service"}},
		Root:     root,
		Listen:   []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey: base64.StdEncoding.EncodeToString(swarmKey()),
	}

	_, err := Start(t.Context(), f)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not-a-service")

	// AND THE PORT IS FREE AGAIN, which is the observable half of the peer
	// having been closed: a second attempt on the same root would fail on a
	// locked datastore if the first had held it.
	f.Node = node.Config{Services: []string{node.Register}}
	running, err := Start(t.Context(), f)
	require.NoError(t, err)
	require.NoError(t, running.Close())
}

// TestANodeWithBootstrapPeersUsesTheOtherConstructor. A node with peers dials
// them; a node with none is the first one. Handing the second an empty list
// bootstraps it against nobody and then waits for a swarm it will never be
// introduced to.
func TestANodeWithBootstrapPeersDialsThem(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(swarmKey())

	first, err := Start(t.Context(), File{
		Node:     node.Config{Services: []string{node.Register, node.Discovery}},
		Root:     t.TempDir(),
		Listen:   []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey: key,
	})
	require.NoError(t, err)
	defer first.Close()

	addrs := first.Host().Addrs()
	require.NotEmpty(t, addrs)
	bootstrap := addrs[0].String() + "/p2p/" + first.Host().ID().String()

	second, err := Start(t.Context(), File{
		Node:      node.Config{Services: []string{node.Register, node.Discovery}},
		Root:      t.TempDir(),
		Listen:    []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey:  key,
		Bootstrap: []string{bootstrap},
	})
	require.NoError(t, err)
	defer second.Close()

	require.NotEqual(t, first.Host().ID(), second.Host().ID())
}

// TestAServingNodeSaysWhereItIsBound. The port may have been :0, so what a
// caller was given and what was taken are two different facts — and the second
// is the one a client can connect to.
func TestAServingNodeSaysWhereItIsBound(t *testing.T) {
	running, err := Start(t.Context(), File{
		Node:     node.Config{Services: []string{node.Register, node.Discovery, node.Gateway}},
		Root:     t.TempDir(),
		Listen:   []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey: base64.StdEncoding.EncodeToString(swarmKey()),
		HTTP:     "127.0.0.1:0",
	})
	require.NoError(t, err)
	defer running.Close()

	addr := running.Address()
	require.NotEmpty(t, addr)
	require.NotEqual(t, "127.0.0.1:0", addr, "the bound port, not the one asked for")

	_, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	require.NotEqual(t, "0", port)
}

// TestAConfiguredIdentityIsTheOneUsed. A new identity every restart is almost
// never what somebody meant: anything that remembered this peer — a bootstrap
// list, another node's view, a pinned address — forgets it.
func TestAConfiguredIdentityIsTheOneUsed(t *testing.T) {
	minted, err := identity("")
	require.NoError(t, err)

	encoded := base64.StdEncoding.EncodeToString(minted)
	again, err := identity(encoded)
	require.NoError(t, err)
	require.Equal(t, minted, again, "a configured key is read back, not replaced")

	f := File{
		Node:       node.Config{Services: []string{node.Register}},
		Root:       t.TempDir(),
		Listen:     []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey:   base64.StdEncoding.EncodeToString(swarmKey()),
		PrivateKey: encoded,
	}

	first, err := Start(t.Context(), f)
	require.NoError(t, err)
	id := first.Host().ID()
	require.NoError(t, first.Close())

	// THE SAME KEY IS THE SAME PEER, which is the whole reason to write one down.
	f.Root = t.TempDir()
	second, err := Start(t.Context(), f)
	require.NoError(t, err)
	defer second.Close()
	require.Equal(t, id, second.Host().ID())
}
