package app

import (
	"io"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/config"
	seer "github.com/taubyte/tau/pkg/yaseer"
	"gopkg.in/yaml.v3"
	"gotest.tools/v3/assert"
)

var migrateTestConfig = `
privatekey: fakeKey
swarmkey: keys/swarm.key
protocols:
    - auth
    - patrick
    - monkey
    - tns
    - hoarder
    - substrate
    - seer
p2p-listen:
    - /ip4/0.0.0.0/tcp/4242
p2p-announce:
    - /ip4/1.2.3.4/tcp/4242
ports:
    main: 4242
    lite: 4247
    ipfs: 4252
location:
    lat: 40.076897
    long: -109.33771
network-fqdn: enterprise.starships.ws
domains:
    key:
        private: keys/dv_private.pem
        public: keys/dv_public.pem
    generated: e.ftll.ink
plugins: {}
`

var migrateTestConfigNoProtos = `
privatekey: fakeKey
swarmkey: keys/swarm.key
p2p-listen:
    - /ip4/0.0.0.0/tcp/4242
p2p-announce:
    - /ip4/1.2.3.4/tcp/4242
ports:
    main: 4242
    lite: 4247
    ipfs: 4252
location:
    lat: 40.076897
    long: -109.33771
network-fqdn: enterprise.starships.ws
domains:
    key:
        private: keys/dv_private.pem
        public: keys/dv_public.pem
    generated: e.ftll.ink
plugins: {}
`

var migrateTestConfigJustServices = `
privatekey: fakeKey
swarmkey: keys/swarm.key
services:
    - auth
    - patrick
    - monkey
    - tns
    - hoarder
    - substrate
    - seer
p2p-listen:
    - /ip4/0.0.0.0/tcp/4242
p2p-announce:
    - /ip4/1.2.3.4/tcp/4242
ports:
    main: 4242
    lite: 4247
    ipfs: 4252
location:
    lat: 40.076897
    long: -109.33771
network-fqdn: enterprise.starships.ws
domains:
    key:
        private: keys/dv_private.pem
        public: keys/dv_public.pem
    generated: e.ftll.ink
plugins: {}
`

func prepMigrationFS(t *testing.T, data string) (afero.Fs, *config.Source) {
	fs := afero.NewMemMapFs()

	fs.Mkdir("/config", 0540)

	cnf, err := fs.Create("/config/shape.yaml")
	assert.NilError(t, err)
	defer cnf.Close()

	_, err = io.WriteString(cnf, data)
	assert.NilError(t, err)

	cnf.Seek(0, 0)

	org := &config.Source{}
	err = yaml.NewDecoder(cnf).Decode(&org)
	assert.NilError(t, err)

	return fs, org
}

func TestConfigMigration(t *testing.T) {
	fs, org := prepMigrationFS(t, migrateTestConfig)

	err := configMigration(seer.VirtualFS(fs, "/config"), "shape")
	assert.NilError(t, err)

	cnf, err := fs.Open("/config/shape.yaml")
	assert.NilError(t, err)
	defer cnf.Close()

	src := &config.Source{}
	err = yaml.NewDecoder(cnf).Decode(&src)
	assert.NilError(t, err)

	// make sure protos are moved to services
	assert.Equal(t, len(src.Services), 7)

	org.Services = src.Services

	// make sure eveything else stayed the same
	assert.DeepEqual(t, org, src)
}

func TestConfigMigrationNoProtos(t *testing.T) {
	fs, _ := prepMigrationFS(t, migrateTestConfigNoProtos)
	err := configMigration(seer.VirtualFS(fs, "/config"), "shape")
	assert.NilError(t, err)
}

func TestConfigMigrationJustServices(t *testing.T) {
	fs, org := prepMigrationFS(t, migrateTestConfigJustServices)

	err := configMigration(seer.VirtualFS(fs, "/config"), "shape")
	assert.NilError(t, err)

	cnf, err := fs.Open("/config/shape.yaml")
	assert.NilError(t, err)
	defer cnf.Close()

	src := &config.Source{}
	err = yaml.NewDecoder(cnf).Decode(&src)
	assert.NilError(t, err)

	// make sure eveything else stayed the same
	assert.DeepEqual(t, org, src)
}
