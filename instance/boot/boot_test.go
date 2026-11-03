package boot

import (
	"encoding/base64"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/instance"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

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
open: true
http: ":8080"
`))
	require.NoError(t, err)

	require.Equal(t, []string{instance.Register, instance.Worker}, f.Node.Services)
	require.Equal(t, "acme", f.Node.Cloud)
	require.Equal(t, "io.containerd.runsc.v1", f.Node.Container.Runtime,
		"the isolation handler reaches the node that builds the tier")
	require.Equal(t, 8, f.Node.Worker.MaxProjects)
	require.Equal(t, "tau.example.io", f.Node.Gateway.CloudFqdn)
	require.Equal(t, int64(1<<20), f.Node.Gateway.MaxBody)
	require.Equal(t, "/var/lib/tau", f.Root)
	require.Equal(t, ":8080", f.HTTP)
}

func TestAMissingCertificateIsRefused(t *testing.T) {
	_, err := Read(write(t, "root: /var/lib/tau\n"))
	require.ErrorIs(t, err, ErrNoCertificate)
	require.Contains(t, err.Error(), "open: true", "and it says how to mean it")

	// A root with no certificate of this node's own is no admission either.
	_, err = Read(write(t, "ca: x\n"))
	require.ErrorIs(t, err, ErrNoCertificate)

	// Saying it is a line somebody wrote.
	f, err := Read(write(t, "open: true\n"))
	require.NoError(t, err)
	require.True(t, f.Open)
}

func TestTheDefaultsAreApplied(t *testing.T) {
	f, err := Read(write(t, "open: true\n"))
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

func TestKeysAreBase64(t *testing.T) {
	_, err := Start(t.Context(), File{Open: true, PrivateKey: "not base64!"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "privatekey")
}

func TestStartRefusesAnOpenNetworkItWasNotToldAbout(t *testing.T) {
	_, err := Start(t.Context(), File{Root: t.TempDir()})
	require.ErrorIs(t, err, ErrNoCertificate)
}

func TestTheExampleRoundTrips(t *testing.T) {
	example := File{
		Node:      instance.Config{Services: instance.All, Cloud: "acme"},
		Root:      DefaultRoot,
		Listen:    []string{DefaultListen},
		Open:      true,
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

func TestANodeStartsAndServes(t *testing.T) {
	f := admit(t, File{
		Node:   instance.Config{Services: []string{instance.Register, instance.Discovery, instance.Gateway}},
		Root:   t.TempDir(),
		Listen: []string{"/ip4/127.0.0.1/tcp/0"},
		HTTP:   "127.0.0.1:0",
	})

	running, err := Start(t.Context(), f)
	require.NoError(t, err)
	defer running.Close()

	require.NotNil(t, running.Register())
	require.NotNil(t, running.Discovery())
	require.NotNil(t, running.Handler())

	// It reports itself, which is what the rest of the fleet routes on.
	snapshot, err := running.Snapshot(t.Context())
	require.NoError(t, err)
	require.True(t, snapshot.Ready)
	require.NotEmpty(t, snapshot.Addresses)
	require.Contains(t, snapshot.Serving, instance.Register)

	require.NoError(t, running.Close())
	require.NoError(t, running.Close(), "close is idempotent enough to be deferred")
}

func TestANodeWithNoGatewayBindsNothing(t *testing.T) {
	f := admit(t, File{
		Node:   instance.Config{Services: []string{instance.Register}},
		Root:   t.TempDir(),
		Listen: []string{"/ip4/127.0.0.1/tcp/0"},
		HTTP:   "127.0.0.1:0",
		HTTPS:  "127.0.0.1:0",
	})

	running, err := Start(t.Context(), f)
	require.NoError(t, err)
	defer running.Close()

	require.Nil(t, running.Handler())
	require.Nil(t, running.TLSConfig())
	require.Empty(t, running.Address())
	require.Empty(t, running.SecureAddress())
}

func TestAnAddressAlreadyTaken(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer held.Close()

	_, err = Start(t.Context(), admit(t, File{
		Node:   instance.Config{Services: []string{instance.Discovery, instance.Gateway}},
		Root:   t.TempDir(),
		Listen: []string{"/ip4/127.0.0.1/tcp/0"},
		HTTP:   held.Addr().String(),
	}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "listening on")
}

func TestAnUnstartedRunningAnswersNothing(t *testing.T) {
	var r Running
	require.Nil(t, r.Host())
	require.Empty(t, r.Address(), "nothing is being served")
}

func TestANodeThatCannotBeBuiltClosesWhatWasOpened(t *testing.T) {
	root := t.TempDir()
	f := admit(t, File{
		// A service nobody runs: the node's own closed set refuses it, which
		// happens after the peer and the asset store are already open.
		Node:   instance.Config{Services: []string{"not-a-service"}},
		Root:   root,
		Listen: []string{"/ip4/127.0.0.1/tcp/0"},
	})

	_, err := Start(t.Context(), f)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not-a-service")

	// AND THE PORT IS FREE AGAIN, which is the observable half of the peer
	// having been closed: a second attempt on the same root would fail on a
	// locked datastore if the first had held it.
	f.Node = instance.Config{Services: []string{instance.Register}}
	running, err := Start(t.Context(), f)
	require.NoError(t, err)
	require.NoError(t, running.Close())
}

func TestANodeWithBootstrapPeersDialsThem(t *testing.T) {
	first, err := Start(t.Context(), admit(t, File{
		Node:   instance.Config{Services: []string{instance.Register, instance.Discovery}},
		Root:   t.TempDir(),
		Listen: []string{"/ip4/127.0.0.1/tcp/0"},
	}))
	require.NoError(t, err)
	defer first.Close()

	addrs := first.Host().Addrs()
	require.NotEmpty(t, addrs)
	bootstrap := addrs[0].String() + "/p2p/" + first.Host().ID().String()

	second, err := Start(t.Context(), admit(t, File{
		Node:      instance.Config{Services: []string{instance.Register, instance.Discovery}},
		Root:      t.TempDir(),
		Listen:    []string{"/ip4/127.0.0.1/tcp/0"},
		Bootstrap: []string{bootstrap},
	}))
	require.NoError(t, err)
	defer second.Close()

	require.NotEqual(t, first.Host().ID(), second.Host().ID())
}

func TestAServingNodeSaysWhereItIsBound(t *testing.T) {
	running, err := Start(t.Context(), admit(t, File{
		Node:   instance.Config{Services: []string{instance.Register, instance.Discovery, instance.Gateway}},
		Root:   t.TempDir(),
		Listen: []string{"/ip4/127.0.0.1/tcp/0"},
		HTTP:   "127.0.0.1:0",
	}))
	require.NoError(t, err)
	defer running.Close()

	addr := running.Address()
	require.NotEmpty(t, addr)
	require.NotEqual(t, "127.0.0.1:0", addr, "the bound port, not the one asked for")

	_, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	require.NotEqual(t, "0", port)
}

func TestAConfiguredIdentityIsTheOneUsed(t *testing.T) {
	minted, err := identity("")
	require.NoError(t, err)

	encoded := base64.StdEncoding.EncodeToString(minted)
	again, err := identity(encoded)
	require.NoError(t, err)
	require.Equal(t, minted, again, "a configured key is read back, not replaced")

	f := admit(t, File{
		Node:       instance.Config{Services: []string{instance.Register}},
		Root:       t.TempDir(),
		Listen:     []string{"/ip4/127.0.0.1/tcp/0"},
		PrivateKey: encoded,
	})

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
