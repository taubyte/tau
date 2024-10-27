package drive

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"sync"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/config"
	host "github.com/taubyte/tau/pkg/mycelium/host"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/pkg/spore-drive/config/fixtures"
	"github.com/taubyte/tau/pkg/spore-drive/course"
	"github.com/taubyte/tau/pkg/spore-drive/drive/mocks"
	"gotest.tools/v3/assert"
)

func TestDisplace(t *testing.T) {
	_, p := fixtures.VirtConfig()
	sd, err := New(p)
	assert.NilError(t, err)
	sd.(*sporedrive).tauBinary = make([]byte, 1024)
	testDisplace(t, sd)
}

func TestDisplaceWithoutUpdatingTau(t *testing.T) {
	_, p := fixtures.VirtConfig()
	sd, err := New(p)
	assert.NilError(t, err)
	testDisplace(t, sd)
}

func testDisplace(t *testing.T, sd Spore) {
	sdrive := sd.(*sporedrive)
	updatingTau := (sdrive.tauBinary != nil)

	fses := make(map[host.Host]afero.Fs)
	var fsesLock sync.Mutex

	sdrive.hostWrapper = func(ctx context.Context, h host.Host) (remoteHost, error) {
		rh := mocks.NewRemoteHost(t)
		rh.On("Host").Return(h)
		fsesLock.Lock()
		fses[h] = afero.NewMemMapFs()
		fsesLock.Unlock()
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
		rh.On("Execute", ctx, "dig", "+short", "+timeout=5", "@1.1.1.1", "google.com").Once().Return([]byte(`142.250.115.100
142.250.115.102
142.250.115.113
142.250.115.138
142.250.115.101
142.250.115.139
		`), nil)

		if updatingTau {
			rh.On("Execute", ctx, "md5sum", "-bz", "/tb/bin/tau").Once().Return(nil, nil)
		}

		// upload tau
		if updatingTau {
			tauf, _ := fses[h].OpenFile("/tmp/tau", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0750)
			rh.On("OpenFile", "/tmp/tau", os.O_CREATE|os.O_RDWR|os.O_TRUNC, fs.FileMode(0750)).Once().Return(tauf, nil)
		}

		// upload tau files
		if updatingTau {
			rh.On("Open", "/lib/systemd/system/tau@.service").Once().Return(nil, os.ErrNotExist)
			sdcf, _ := fses[h].OpenFile("/tmp/tau@.service", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
			rh.On("OpenFile", "/tmp/tau@.service", os.O_CREATE|os.O_RDWR|os.O_TRUNC, fs.FileMode(0644)).Once().Return(sdcf, nil)
			rh.On("Sudo", ctx, "cp", "-f", "/tmp/tau@.service", "/lib/systemd/system/tau@.service").Return(nil, nil)
			rh.On("Sudo", ctx, "systemctl", "daemon-reload").Return(nil, nil)
		}

		rh.On("Sudo", ctx, "bash", "-c", "mkdir -p /tb/{bin,scripts,priv,cache,logs,storage,config/keys,plugins}").Return(nil, nil)

		// setup tau
		swarmk, _ := fses[h].OpenFile("/tmp/swarm.key", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
		rh.On("OpenFile", "/tmp/swarm.key", os.O_CREATE|os.O_RDWR|os.O_TRUNC, fs.FileMode(0600)).Once().Return(swarmk, nil)

		dprivk, _ := fses[h].OpenFile("/tmp/dv_private.key", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
		rh.On("OpenFile", "/tmp/dv_private.key", os.O_CREATE|os.O_RDWR|os.O_TRUNC, fs.FileMode(0600)).Once().Return(dprivk, nil)

		dpubk, _ := fses[h].OpenFile("/tmp/dv_public.key", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
		rh.On("OpenFile", "/tmp/dv_public.key", os.O_CREATE|os.O_RDWR|os.O_TRUNC, fs.FileMode(0600)).Once().Return(dpubk, nil)

		rh.On("Sudo", ctx, "cp", "-f", "/tmp/swarm.key", "/tb/config/keys/").Return(nil, nil)
		rh.On("Sudo", ctx, "cp", "-f", "/tmp/dv_private.key", "/tb/config/keys/").Return(nil, nil)
		rh.On("Sudo", ctx, "cp", "-f", "/tmp/dv_public.key", "/tb/config/keys/").Return(nil, nil)

		sh1cf, _ := fses[h].OpenFile("/tmp/shape1.yaml", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0750)
		rh.On("OpenFile", "/tmp/shape1.yaml", os.O_CREATE|os.O_RDWR|os.O_TRUNC, fs.FileMode(0750)).Once().Return(sh1cf, nil)
		rh.On("Sudo", ctx, "cp", "-f", "/tmp/shape1.yaml", "/tb/config/").Return(nil, nil)
		if updatingTau {
			rh.On("Sudo", ctx, "cp", "-f", "/tmp/tau", "/tb/bin/").Return(nil, nil)
		}

		rh.On("Sudo", ctx, "systemctl", "list-units", "--type=service", "--quiet", "--no-pager", "--all", "tau@*.service").Once().Return([]byte(`  tau@compute.service   loaded inactive dead Description of compute
  tau@storage.service   loaded inactive dead Description of storage
  tau@shape2.service    loaded inactive dead Description of shape2
  tau@gpu.service       loaded inactive dead Description of gpu`), nil)

		if updatingTau {
			for _, shape := range []string{"shape1", "shape2", "compute", "storage", "gpu"} {
				rh.On("Sudo", ctx, "systemctl", "stop", "tau@"+shape).Return(nil, nil)
			}
		} else {
			for _, shape := range []string{"shape1", "compute", "storage", "gpu"} {
				rh.On("Sudo", ctx, "systemctl", "stop", "tau@"+shape).Return(nil, nil)
			}
		}
		for _, shape := range []string{"compute", "storage", "gpu"} {
			rh.On("Sudo", ctx, "systemctl", "disable", "tau@"+shape).Return(nil, nil)
		}

		for _, shape := range []string{"shape1"} {
			rh.On("Sudo", ctx, "systemctl", "enable", "tau@"+shape).Return(nil, nil)
		}
		if updatingTau {
			for _, shape := range []string{"shape1", "shape2"} {
				rh.On("Sudo", ctx, "systemctl", "start", "tau@"+shape).Return(nil, nil)
			}
		} else {
			for _, shape := range []string{"shape1"} {
				rh.On("Sudo", ctx, "systemctl", "start", "tau@"+shape).Return(nil, nil)
			}
		}

		// clean up
		rh.On("Remove", "/tmp/tau").Return(nil)
		rh.On("Remove", "/tmp/tau@.service").Return(nil)
		rh.On("Remove", "/tmp/shape1.yaml").Return(nil)

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

	if updatingTau {
		assert.Equal(t, len(steps), 56)
	} else {
		assert.Equal(t, len(steps), 50)
	}

	for h, mfs := range fses {
		// check config
		f, err := mfs.Open("/tmp/shape1.yaml")
		assert.NilError(t, err)
		data, err := io.ReadAll(f)
		assert.NilError(t, err)
		f.Close()
		var cnf config.Source
		assert.NilError(t, yaml.Unmarshal(data, &cnf))
		assert.Equal(t, cnf.Privatekey, sdrive.parser.Hosts().Host(h.Name()).Shapes().Instance("shape1").Key())

		if updatingTau {
			// check tau
			f, err = mfs.Open("/tmp/tau")
			assert.NilError(t, err)
			data, err := io.ReadAll(f)
			assert.NilError(t, err)
			f.Close()
			assert.DeepEqual(t, sdrive.tauBinary, data)

			// check systemd file
			f, err = mfs.Open("/tmp/tau@.service")
			assert.NilError(t, err)
			data, err = io.ReadAll(f)
			assert.NilError(t, err)
			f.Close()
			assert.Equal(t, string(data), `[Unit]
Description = Taubyte Tau Service Running %i

[Service]
Type           = simple
ExecStartPre   = -/usr/sbin/setenforce 0
ExecStart      = /tb/bin/tau start -s %i --root /tb
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
		}
	}
}
