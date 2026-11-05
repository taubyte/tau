package drive

import (
	"encoding/base64"
	"slices"
	"testing"
	"time"

	"github.com/spf13/afero"
	"gotest.tools/v3/assert"

	"github.com/taubyte/tau/instance"
	"github.com/taubyte/tau/p2p/sectls"
	"github.com/taubyte/tau/tools/spore-drive/config"
	"github.com/taubyte/tau/tools/spore-drive/config/fixtures"
)

func driveFor(p config.Parser) *sporedrive {
	return &sporedrive{parser: p, root: DefaultRoot, now: time.Now}
}

func TestAHostIsOneNode(t *testing.T) {
	_, p := fixtures.VirtConfig()
	assert.NilError(t, p.Shapes().Shape("shape2").Services().Set("gateway", "monkey", "proxy"))

	nodes, err := driveFor(p).nodesFor("host1")
	assert.NilError(t, err)
	assert.Equal(t, len(nodes.instances), 1)
	assert.Equal(t, nodes.instances[0].name, "shape1", "the main node is named after the primary shape")
	assert.DeepEqual(t, nodes.instances[0].file.Node.Services,
		[]string{instance.Auth, instance.Secrets, instance.Discovery, instance.DNS, instance.Gateway, instance.Builder, instance.Proxy})
}

func TestEveryNodeRunsDiscovery(t *testing.T) {
	_, p := fixtures.VirtConfig()
	assert.NilError(t, p.Shapes().Shape("shape1").Services().Set("tns"))
	assert.NilError(t, p.Shapes().Shape("shape2").Services().Set("hoarder", "substrate"))

	nodes, err := driveFor(p).nodesFor("host1")
	assert.NilError(t, err)
	assert.DeepEqual(t, nodes.instances[0].file.Node.Services,
		[]string{instance.Register, instance.Database, instance.Primitives, instance.Worker, instance.Gateway, instance.Discovery})
}

func TestAnUnknownServiceIsRefused(t *testing.T) {
	_, p := fixtures.VirtConfig()
	assert.NilError(t, p.Shapes().Shape("shape2").Services().Set("gatewya"))
	_, err := driveFor(p).nodesFor("host1")
	assert.ErrorContains(t, err, `"gatewya"`)
}

func TestWhatHasNoV2HomeIsReported(t *testing.T) {
	_, p := fixtures.VirtConfig()
	assert.NilError(t, p.Cloud().Domain().SetHost("admin.test.com", "gateway"))

	nodes, err := driveFor(p).nodesFor("host1")
	assert.NilError(t, err)
	for _, want := range []string{
		"hosts.host1.location (a v2 node reads no location)",
		"shapes.shape1.ports.lite",
		"shapes.shape2.ports.lite",
		"shapes.shape2.plugins",
		"service accounts",
		"cloud.domain.hosts",
	} {
		assert.Assert(t, slices.Contains(nodes.ignored, want), "%q not in %v", want, nodes.ignored)
	}
	assert.Assert(t, !slices.Contains(nodes.ignored, "shapes.shape1.ports.main"))
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

func TestAMissingKeyFileIsAnError(t *testing.T) {
	fs, p := fixtures.VirtConfig()
	assert.NilError(t, fs.Remove("/keys/ca.key"))
	_, err := driveFor(p).nodesFor("host1")
	assert.ErrorContains(t, err, "the cloud's CA")
}

func TestAddedServicesConfigurationReachesEveryNode(t *testing.T) {
	fs, p := fixtures.VirtConfig()
	assert.NilError(t, afero.WriteFile(fs, "/added.yaml", []byte("svc:\n    host: example.com\n    admins: [a, b]\n"), 0600))
	assert.NilError(t, p.Sync())
	p, err := config.New(fs, "/")
	assert.NilError(t, err)

	nodes, err := driveFor(p).nodesFor("host1")
	assert.NilError(t, err)
	assert.Assert(t, len(nodes.instances) == 1)
	want := map[string]any{"svc": map[string]any{"host": "example.com", "admins": []any{"a", "b"}}}
	for _, inst := range nodes.instances {
		assert.DeepEqual(t, inst.file.Node.Sections, want)
	}
}

func TestNodesIsThePlanOfOneHost(t *testing.T) {
	_, p := fixtures.VirtConfig()
	plan, err := driveFor(p).Nodes("host1")
	assert.NilError(t, err)
	assert.Equal(t, len(plan.Nodes), 1, "plm and the builder share the host's node")
	assert.Equal(t, plan.Nodes[0].Name, "shape1")
	assert.Assert(t, slices.Contains(plan.Nodes[0].Services, instance.PLM))
	assert.Assert(t, slices.Contains(plan.Nodes[0].Services, instance.Builder))
	assert.Assert(t, slices.Contains(plan.Ignored, "shapes.shape2.plugins"))

	assert.NilError(t, p.Shapes().Shape("shape2").Services().Set("gatewya"))
	_, err = driveFor(p).Nodes("host1")
	assert.ErrorContains(t, err, "gatewya")
}

func TestTheTenancyReachesEveryNode(t *testing.T) {
	fs, p := fixtures.VirtConfig()
	assert.NilError(t, afero.WriteFile(fs, "/tenancy.yaml", []byte("provider: github\nowner: Acme\nnamespaces: [GitLab.example.com/Team]\napp:\n    client-id: x\n"), 0600))
	p, err := config.New(fs, "/")
	assert.NilError(t, err)

	nodes, err := driveFor(p).nodesFor("host1")
	assert.NilError(t, err)
	assert.Assert(t, len(nodes.instances) == 1)
	for _, inst := range nodes.instances {
		assert.DeepEqual(t, inst.file.Node.Tenancy, []string{"github.com/acme", "gitlab.example.com/team"})
	}
	assert.Assert(t, slices.Contains(nodes.ignored, "tenancy.app (a v2 node checks the namespace, not membership)"))

	assert.NilError(t, afero.WriteFile(fs, "/tenancy.yaml", []byte("provider: git.internal:8443\nowner: ops\n"), 0600))
	p, err = config.New(fs, "/")
	assert.NilError(t, err)
	nodes, err = driveFor(p).nodesFor("host1")
	assert.NilError(t, err)
	assert.DeepEqual(t, nodes.instances[0].file.Node.Tenancy, []string{"git.internal:8443/ops"})

	assert.NilError(t, afero.WriteFile(fs, "/tenancy.yaml", []byte("namespaces: [acme]\n"), 0600))
	p, err = config.New(fs, "/")
	assert.NilError(t, err)
	_, err = driveFor(p).nodesFor("host1")
	assert.ErrorContains(t, err, "tenancy.yaml")
}

func TestNoTenancyFileServesEverything(t *testing.T) {
	_, p := fixtures.VirtConfig()
	nodes, err := driveFor(p).nodesFor("host1")
	assert.NilError(t, err)
	assert.Assert(t, nodes.instances[0].file.Node.Tenancy == nil)
}

func TestIssueRefusesAKeyThatIsNotOne(t *testing.T) {
	ca, err := sectls.NewCA("test.com", time.Hour)
	assert.NilError(t, err)
	_, err = issue(ca, "not base64!", "test.com")
	assert.ErrorContains(t, err, "decode private key")
	_, err = issue(ca, base64.StdEncoding.EncodeToString([]byte("not a key")), "test.com")
	assert.ErrorContains(t, err, "private key")
}

func TestAShapeMustNameAUnitAndADirectory(t *testing.T) {
	for shape, want := range map[string]string{
		"a b":   "letters, digits",
		"../up": "letters, digits",
	} {
		_, p := fixtures.VirtConfig()
		p.Shapes().Shape(shape).Ports().Set("main", 5242)
		assert.NilError(t, p.Hosts().Host("host1").Shapes().Instance(shape).GenerateKey())
		_, err := driveFor(p).nodesFor("host1")
		assert.ErrorContains(t, err, want, shape)
	}
}

func TestTheRootIsOneSetting(t *testing.T) {
	_, p := fixtures.VirtConfig()
	for _, bad := range []string{"relative", "/", "/a/../b", "/a b", "/a/"} {
		_, err := New(p, WithRoot(bad))
		assert.ErrorContains(t, err, "root", bad)
	}
	sd, err := New(p, WithRoot(""))
	assert.NilError(t, err)
	assert.Equal(t, sd.(*sporedrive).root, DefaultRoot)

	sd, err = New(p, WithRoot("/opt/tau"))
	assert.NilError(t, err)
	d := sd.(*sporedrive)
	assert.Equal(t, d.binary(), "/opt/tau/bin/tau")
	assert.Equal(t, d.configOf("shape1"), "/opt/tau/config/shape1.yaml")
	assert.Equal(t, d.storageOf("shape1"), "/opt/tau/storage/shape1")
}
