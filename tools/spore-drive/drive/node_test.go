package drive

import (
	"encoding/base64"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"gotest.tools/v3/assert"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/tools/spore-drive/config"
	"github.com/taubyte/tau/tools/spore-drive/config/fixtures"
)

func driveFor(p config.Parser) *sporedrive { return &sporedrive{parser: p} }

// TestAHostWithoutBothBuildHalvesIsOneNode, and v2 service names pass through.
func TestAHostWithoutBothBuildHalvesIsOneNode(t *testing.T) {
	_, p := fixtures.VirtConfig()
	assert.NilError(t, p.Shapes().Shape("shape2").Services().Set("gateway", "monkey", "proxy"))

	nodes, err := driveFor(p).nodesFor("host1")
	assert.NilError(t, err)
	assert.Equal(t, len(nodes.instances), 1)
	assert.Equal(t, nodes.instances[0].name, mainInstance)
	assert.DeepEqual(t, nodes.instances[0].file.Node.Services,
		[]string{node.Auth, node.Secrets, node.Discovery, node.DNS, node.Gateway, node.BuildBackend, node.Proxy})
}

// TestEveryNodeRunsDiscovery, even when no shape asked for seer.
func TestEveryNodeRunsDiscovery(t *testing.T) {
	_, p := fixtures.VirtConfig()
	assert.NilError(t, p.Shapes().Shape("shape1").Services().Set("tns"))
	assert.NilError(t, p.Shapes().Shape("shape2").Services().Set("hoarder", "substrate"))

	nodes, err := driveFor(p).nodesFor("host1")
	assert.NilError(t, err)
	assert.DeepEqual(t, nodes.instances[0].file.Node.Services,
		[]string{node.Register, node.Database, node.Primitives, node.Worker, node.Gateway, node.Discovery})
	assert.Equal(t, nodes.instances[0].file.Node.Auth.DomainValidation.Private, "", "no auth, no domain key")
}

func TestAnUnknownServiceIsRefused(t *testing.T) {
	_, p := fixtures.VirtConfig()
	assert.NilError(t, p.Shapes().Shape("shape2").Services().Set("gatewya"))
	_, err := driveFor(p).nodesFor("host1")
	assert.ErrorContains(t, err, `"gatewya"`)
}

// TestWhatHasNoV2HomeIsReported.
func TestWhatHasNoV2HomeIsReported(t *testing.T) {
	_, p := fixtures.VirtConfig()
	assert.NilError(t, p.Cloud().Domain().SetHost("admin.test.com", "gateway"))
	assert.NilError(t, config.SetEnterprisePath(p, "svc", []string{"k"}, "v"))

	nodes, err := driveFor(p).nodesFor("host1")
	assert.NilError(t, err)
	for _, want := range []string{
		"hosts.host1.location (a v2 node reads no location)",
		"shapes.shape1.ports.lite",
		"shapes.shape2.ports.lite",
		"shapes.shape2.plugins",
		"service accounts",
		"cloud.domain.hosts",
		"accounts",
		"tenancy",
	} {
		assert.Assert(t, slices.Contains(nodes.ignored, want), "%q not in %v", want, nodes.ignored)
	}
	assert.Assert(t, !slices.Contains(nodes.ignored, "shapes.shape1.ports.main"))
}

// TestTheSplitIdentityIsStable: the same key every deploy, never the primary.
func TestTheSplitIdentityIsStable(t *testing.T) {
	_, p := fixtures.VirtConfig()
	primary := p.Hosts().Host("host1").Shapes().Instance("shape1").Key()

	a, err := derivedKey(primary, node.BuildFrontend)
	assert.NilError(t, err)
	b, err := derivedKey(primary, node.BuildFrontend)
	assert.NilError(t, err)
	assert.Equal(t, a, b)
	assert.Assert(t, a != primary)

	raw, err := base64.StdEncoding.DecodeString(a)
	assert.NilError(t, err)
	_, err = crypto.UnmarshalPrivateKey(raw)
	assert.NilError(t, err)

	_, err = derivedKey("not base64!", node.BuildFrontend)
	assert.ErrorContains(t, err, "decode")
}

func TestABrokenHostIsAnError(t *testing.T) {
	_, p := fixtures.VirtConfig()
	assert.NilError(t, p.Hosts().Host("host2").Addresses().Add("nonsense"))
	_, err := driveFor(p).nodesFor("host2")
	assert.ErrorContains(t, err, "nonsense")
	_, err = driveFor(p).nodesFor("host1")
	assert.ErrorContains(t, err, "nonsense", "the bootstrap peer's addresses are read too")

	_, p = fixtures.VirtConfig()
	assert.NilError(t, p.Hosts().Host("host2").Shapes().Delete("shape1"))
	assert.NilError(t, p.Hosts().Host("host2").Shapes().Delete("shape2"))
	_, err = driveFor(p).nodesFor("host2")
	assert.ErrorContains(t, err, "runs no shape")
	_, err = driveFor(p).nodesFor("host1")
	assert.ErrorContains(t, err, "runs no shape", "a bootstrap host with no node")
}

func TestReadAll(t *testing.T) {
	broken := errors.New("broken")
	_, err := readAll(func() (io.ReadCloser, error) { return nil, broken })
	assert.ErrorIs(t, err, broken)
}

func TestAMissingKeyFileIsAnError(t *testing.T) {
	fs, p := fixtures.VirtConfig()
	assert.NilError(t, fs.Remove("/keys/dv_public.key"))
	_, err := driveFor(p).nodesFor("host1")
	assert.ErrorContains(t, err, "public domain key")

	fs, p = fixtures.VirtConfig()
	assert.NilError(t, fs.Remove("/keys/dv_private.key"))
	_, err = driveFor(p).nodesFor("host1")
	assert.ErrorContains(t, err, "private domain key")

	fs, p = fixtures.VirtConfig()
	assert.NilError(t, fs.Remove("/keys/swarm.key"))
	_, err = driveFor(p).nodesFor("host1")
	assert.ErrorContains(t, err, "swarm key")
}
