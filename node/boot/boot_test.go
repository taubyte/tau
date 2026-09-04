package boot

import (
	"encoding/base64"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/services/v2/node"
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
		Node:     node.Config{Services: []string{node.Register, node.Stats, node.Gateway}},
		Root:     t.TempDir(),
		Listen:   []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey: base64.StdEncoding.EncodeToString(swarmKey()),
		HTTP:     "127.0.0.1:0",
	}

	running, err := Start(t.Context(), f)
	require.NoError(t, err)
	defer running.Close()

	require.NotNil(t, running.Node.Register())
	require.NotNil(t, running.Node.Stats())
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
	}

	running, err := Start(t.Context(), f)
	require.NoError(t, err)
	defer running.Close()

	require.Nil(t, running.Node.Handler())
	require.Empty(t, running.Address())
}

// TestAnAddressAlreadyTakenIsReportedAtBoot rather than as a gateway nobody can
// reach.
func TestAnAddressAlreadyTaken(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer held.Close()

	_, err = Start(t.Context(), File{
		Node:     node.Config{Services: []string{node.Stats, node.Gateway}},
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
