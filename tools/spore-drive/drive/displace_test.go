package drive

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"google.golang.org/protobuf/proto"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/mock"
	"github.com/taubyte/tau/node/boot"
	"github.com/taubyte/tau/p2p/sectls"
	host "github.com/taubyte/tau/pkg/mycelium/host"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/cm"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/tools/spore-drive/config/fixtures"
	"github.com/taubyte/tau/tools/spore-drive/course"
	"github.com/taubyte/tau/tools/spore-drive/drive/mocks"
	"gotest.tools/v3/assert"
)

func TestDisplace(t *testing.T) {
	_, p := fixtures.VirtConfig()
	sd, err := New(p)
	assert.NilError(t, err)
	sd.(*sporedrive).tauBinary = make([]byte, 1024)
	testDisplace(t, sd, DefaultRoot, nil)
}

func TestDisplaceWithoutUpdatingTau(t *testing.T) {
	_, p := fixtures.VirtConfig()
	sd, err := New(p)
	assert.NilError(t, err)
	testDisplace(t, sd, DefaultRoot, nil)
}

// TestDisplaceStreamsTheSecretsToInit: one bundle, sealed once, reaches every
// secrets node on stdin before it starts, under the configured root, and no
// file on any host holds a secret.
func TestDisplaceStreamsTheSecretsToInit(t *testing.T) {
	fs, p := fixtures.VirtConfig()
	assert.NilError(t, afero.WriteFile(fs, "/keys/acme.key", []byte("ACCOUNT KEY"), 0o600))
	p = withCloud(t, fs, p, signIn)
	sd, err := New(p, WithRoot("/opt/tau"))
	assert.NilError(t, err)
	testDisplace(t, sd, "/opt/tau", []string{"correct-horse", "$argon2id$", "gh-secret", "corp-secret", "ACCOUNT KEY"})
}

func testDisplace(t *testing.T, sd Spore, root string, secrets []string) {
	sdrive := sd.(*sporedrive)
	sdrive.now = func() time.Time { return fixedNow }
	updatingTau := (sdrive.tauBinary != nil)
	var stdins [][]byte
	var stdinsLock sync.Mutex

	// domains.hosts has no v2 home: it is read and reported, not emitted.
	assert.NilError(t, sdrive.parser.Cloud().Domain().SetHost("admin.test.com", "gateway"))

	fses := make(map[host.Host]afero.Fs)
	var fsesLock sync.Mutex

	sdrive.hostWrapper = func(ctx context.Context, h host.Host) (remoteHost, error) {
		rh := mocks.NewRemoteHost(t)
		rh.On("Host").Maybe().Return(h)

		rhfs := afero.NewMemMapFs()

		// deps
		rh.On("Execute", ctx, "command", "-v", "systemctl").Once().Return(nil, nil)
		rh.On("Execute", ctx, "command", "-v", "apt").Once().Return(nil, nil)
		rh.On("Execute", ctx, "command", "-v", "docker").Once().Return(nil, errors.New("no docker"))
		rh.On("Execute", ctx, "curl", "-fsSL", "https://get.docker.com", "-o", "/tmp/get-docker.sh").Once().Return(nil, nil)
		rh.On("Sudo", ctx, "sh", "/tmp/get-docker.sh").Once().Return(nil, nil)
		rh.On("Sudo", ctx, "apt-get", "update").Once().Return(nil, nil)
		for cmd, pkg := range map[string]string{
			"dig":     "dnsutils",
			"netstat": "net-tools",
		} {
			rh.On("Execute", ctx, "command", "-v", cmd).Once().Return(nil, errors.New("no "+cmd))
			rh.On("Sudo", ctx, "apt-get", "install", "-y", pkg).Once().Return(nil, nil)
		}
		rh.On("Sudo", ctx, "netstat", "-lnp").Once().Return(nil, nil)
		// probeDNSResolvers iterates the full dnsResolvers list; mock each one.
		// First resolver returns multiple A records, the rest a single one — all valid.
		rh.On("Execute", ctx, "dig", "+short", "+timeout=5", "@1.1.1.1", "google.com").Once().Return([]byte(`142.250.115.100
142.250.115.102
142.250.115.113
142.250.115.138
142.250.115.101
142.250.115.139
		`), nil)
		for _, dns := range []string{"1.0.0.1", "8.8.8.8", "8.8.4.4", "9.9.9.9", "149.112.112.112", "208.67.222.222", "208.67.220.220"} {
			rh.On("Execute", ctx, "dig", "+short", "+timeout=5", "@"+dns, "google.com").Once().Return([]byte("142.250.115.100\n"), nil)
		}

		if updatingTau {
			rh.On("Execute", ctx, "md5sum", "-bz", root+"/bin/tau").Once().Return(nil, nil)
		}

		// upload tau
		if updatingTau {
			tauf, _ := rhfs.OpenFile("/tmp/tau", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0750)
			rh.On("OpenFile", "/tmp/tau", os.O_CREATE|os.O_RDWR|os.O_TRUNC, fs.FileMode(0750)).Once().Return(tauf, nil)
		}

		// the unit is written on every deploy
		rh.On("Open", "/lib/systemd/system/tau@.service").Once().Return(nil, os.ErrNotExist)
		sdcf, _ := rhfs.OpenFile("/tmp/tau@.service", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
		rh.On("OpenFile", "/tmp/tau@.service", os.O_CREATE|os.O_RDWR|os.O_TRUNC, fs.FileMode(0644)).Once().Return(sdcf, nil)
		rh.On("Sudo", ctx, "cp", "-f", "/tmp/tau@.service", "/lib/systemd/system/tau@.service").Return(nil, nil)
		rh.On("Sudo", ctx, "systemctl", "daemon-reload").Return(nil, nil)

		rh.On("Sudo", ctx, "bash", "-c", "mkdir -p "+root+"/{bin,config,storage}").Return(nil, nil)

		// one file per node, keys inline
		for _, inst := range []string{"shape1", "build-frontend"} {
			cf, _ := rhfs.OpenFile("/tmp/"+inst+".yaml", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
			rh.On("OpenFile", "/tmp/"+inst+".yaml", os.O_CREATE|os.O_RDWR|os.O_TRUNC, fs.FileMode(0600)).Once().Return(cf, nil)
			rh.On("Sudo", ctx, "cp", "-f", "/tmp/"+inst+".yaml", root+"/config/").Return(nil, nil)
			rh.On("Remove", "/tmp/"+inst+".yaml").Return(nil)
		}
		if updatingTau {
			rh.On("Sudo", ctx, "cp", "-f", "/tmp/tau", root+"/bin/").Return(nil, nil)
		}

		// v1's shape1 left its data where the v2 node named shape1 keeps its
		// own, under a config that is not a v2 node file: it is moved aside.
		rh.On("Sudo", ctx, "test", "-d", root+"/storage/shape1").Once().Return(nil, nil)
		rh.On("Sudo", ctx, "grep", "-qs", "^node:", root+"/config/shape1.yaml").Once().Return(nil, errors.New("exit 1"))
		rh.On("Sudo", ctx, "mv", root+"/storage/shape1", root+"/storage/shape1.v1-20261022T093000").Once().Return(nil, nil)
		rh.On("Sudo", ctx, "test", "-d", root+"/storage/build-frontend").Once().Return(nil, errors.New("exit 1"))

		if secrets != nil {
			rh.On("SudoInput", ctx, mock.Anything, root+"/bin/tau", "init", "--config", root+"/config/shape1.yaml", "--root", root+"/storage/shape1", "-").
				Once().Run(func(args mock.Arguments) {
				stdinsLock.Lock()
				stdins = append(stdins, args.Get(1).([]byte))
				stdinsLock.Unlock()
			}).Return(nil, nil)
		}

		// v1 left a unit per shape, and an earlier v2 deploy a unit named node.
		rh.On("Sudo", ctx, "systemctl", "list-units", "--type=service", "--quiet", "--no-pager", "--all", "tau@*.service").Once().Return([]byte(`  tau@shape1.service   loaded inactive dead Description of shape1
  tau@shape2.service    loaded inactive dead Description of shape2
  tau@node.service       loaded inactive dead Description of node`), nil)

		for _, inst := range []string{"shape1", "shape2", "node"} {
			rh.On("Sudo", ctx, "systemctl", "stop", "tau@"+inst).Return(nil, nil)
		}
		for _, inst := range []string{"shape2", "node"} {
			rh.On("Sudo", ctx, "systemctl", "disable", "tau@"+inst).Return(nil, nil)
		}
		rh.On("Sudo", ctx, "systemctl", "enable", "tau@build-frontend").Return(nil, nil)
		for _, inst := range []string{"shape1", "build-frontend"} {
			rh.On("Sudo", ctx, "systemctl", "start", "tau@"+inst).Return(nil, nil)
		}

		// clean up
		rh.On("Remove", "/tmp/tau").Return(nil)
		rh.On("Remove", "/tmp/tau@.service").Return(nil)

		// firewall
		rh.On("Execute", ctx, "command", "-v", "ufw").Once().Return(nil, nil)
		rh.On("Sudo", ctx, "ufw", "status", "numbered").Return(nil, nil)
		rh.On("Sudo", ctx, "ufw", "status", "|", "grep", mock.Anything).Return(nil, errors.New("not found"))
		rh.On("Sudo", ctx, "ufw", "allow", mock.Anything).Return(nil, nil)

		fsesLock.Lock()
		fses[h] = rhfs
		fsesLock.Unlock()

		return rh, nil
	}

	c, err := course.New(sd.Network(), course.Shapes("shape1"))
	assert.NilError(t, err)

	assert.Equal(t, c.Hyphae().Size(), 2)

	pCh := sd.Displace(context.Background(), c)

	steps := make([]Progress, 0)
	for p := range pCh {
		steps = append(steps, p)
	}

	for _, s := range steps {
		assert.NilError(t, s.Error(), s.Name())
	}
	if updatingTau {
		assert.Equal(t, len(steps), 66)
	} else {
		assert.Equal(t, len(steps), 60)
	}

	if secrets != nil {
		assert.Equal(t, len(stdins), 2, "every secrets node is initialized")
		assert.DeepEqual(t, stdins[0], stdins[1])
		var bundle secretsv2.InitBundle
		assert.NilError(t, proto.Unmarshal(stdins[0], &bundle))
		var names []string
		for _, s := range bundle.GetSecrets() {
			names = append(names, s.GetRef().GetName())
		}
		assert.DeepEqual(t, names, []string{auth.AdminSecret, auth.OAuthSecretName("github.com"), auth.OIDCSecretName("corp"), cm.AccountSecret})
	}

	for h, mfs := range fses {
		read := func(path string) []byte {
			f, err := mfs.Open(path)
			assert.NilError(t, err)
			defer f.Close()
			data, err := io.ReadAll(f)
			assert.NilError(t, err)
			return data
		}

		var main, frontend boot.File
		assert.NilError(t, yaml.Unmarshal(read("/tmp/shape1.yaml"), &main))
		assert.NilError(t, yaml.Unmarshal(read("/tmp/build-frontend.yaml"), &frontend))

		// the host's shapes are one node, keyed by its first shape
		assert.Equal(t, main.PrivateKey, sdrive.parser.Hosts().Host(h.Name()).Shapes().Instance("shape1").Key())
		assert.DeepEqual(t, main.Node.Services, []string{"auth", "secrets", "discovery", "dns", "gateway", "build-backend"})
		assert.Equal(t, main.Node.Cloud, "test.com")
		assert.Equal(t, main.Node.Gateway.CloudFqdn, "gtest.com")
		assert.Equal(t, main.HTTP, ":80")
		assert.Equal(t, main.HTTPS, ":443")
		assert.DeepEqual(t, main.Listen, []string{"/ip4/0.0.0.0/tcp/4242"})
		assert.Assert(t, !strings.Contains(string(read("/tmp/shape1.yaml")), "PRIVATE KEY"), "no domain key reaches a node: none signs a domain claim")
		assert.NilError(t, afero.Walk(mfs, "/", func(path string, info fs.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			for _, secret := range secrets {
				assert.Assert(t, !strings.Contains(string(read(path)), secret), "%s holds %s", path, secret)
			}
			return nil
		}))

		// every node's certificate admits it, checked as the node checks it at boot
		for _, f := range []boot.File{main, frontend} {
			raw, err := base64.StdEncoding.DecodeString(f.PrivateKey)
			assert.NilError(t, err)
			key, err := crypto.UnmarshalPrivateKey(raw)
			assert.NilError(t, err)
			_, err = sectls.NewCredentials([]byte(f.CA), []byte(f.Certificate), f.Node.Cloud, key)
			assert.NilError(t, err)
		}
		assert.Equal(t, main.CA, frontend.CA, "one CA for the cloud")

		// build-frontend is split off, on the next port, with its own identity
		assert.DeepEqual(t, frontend.Node.Services, []string{"build-frontend", "discovery"})
		assert.DeepEqual(t, frontend.Listen, []string{"/ip4/0.0.0.0/tcp/4243"})
		assert.Assert(t, frontend.PrivateKey != main.PrivateKey)
		assert.Equal(t, frontend.HTTP, "")

		// the other host's node is the bootstrap peer
		other := "host1"
		if h.Name() == "host1" {
			other = "host2"
		}
		otherID := sdrive.parser.Hosts().Host(other).Shapes().Instance("shape1").Id()
		assert.Equal(t, len(main.Bootstrap), 2)
		for _, b := range main.Bootstrap {
			assert.Assert(t, strings.HasSuffix(b, "/tcp/4242/p2p/"+otherID), b)
		}

		// every deploy writes the v2 unit
		assert.Equal(t, string(read("/tmp/tau@.service")), `[Unit]
Description = Taubyte Tau Service Running %i

[Service]
Type           = simple
ExecStartPre   = -/usr/sbin/setenforce 0
ExecStart      = `+root+`/bin/tau start --config `+root+`/config/%i.yaml --root `+root+`/storage/%i
StandardOutput = journal
User           = root
Group          = root
LimitAS        = infinity
LimitRSS       = infinity
LimitCORE      = infinity
LimitNOFILE    = 65536
Restart        = always
RestartSec     = 1

[Install]
WantedBy = multi-user.target
`)

		if updatingTau {
			assert.DeepEqual(t, sdrive.tauBinary, read("/tmp/tau"))
		}
	}
}
