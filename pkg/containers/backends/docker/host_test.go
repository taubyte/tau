package docker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

func writeJSON(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func TestResolveDockerHost(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")

	t.Run("config wins", func(t *testing.T) {
		t.Setenv("DOCKER_HOST", "unix:///env.sock")
		host, use := resolveDockerHost("unix:///config.sock")
		assert.True(t, use)
		assert.Equal(t, "unix:///config.sock", host)
	})

	t.Run("environment", func(t *testing.T) {
		t.Setenv("DOCKER_HOST", "unix:///env.sock")
		host, use := resolveDockerHost("")
		assert.True(t, use)
		assert.Equal(t, "unix:///env.sock", host)
	})

	type testCase struct {
		name  string
		files map[string]string
		host  string
	}
	cases := []testCase{
		{name: "no config file"},
		{name: "malformed config", files: map[string]string{"config.json": "{"}},
		{name: "no current context", files: map[string]string{"config.json": `{}`}},
		{name: "default context", files: map[string]string{"config.json": `{"currentContext":"default"}`}},
		{
			name: "legacy context layout",
			files: map[string]string{
				"config.json":               `{"currentContext":"remote"}`,
				"contexts/remote/meta.json": `{"Endpoints":{"docker":{"Host":"tcp://legacy:2375"}}}`,
			},
			host: "tcp://legacy:2375",
		},
		{
			name: "legacy context without a host",
			files: map[string]string{
				"config.json":               `{"currentContext":"remote"}`,
				"contexts/remote/meta.json": `{"Endpoints":{}}`,
			},
		},
		{
			name:  "no context store",
			files: map[string]string{"config.json": `{"currentContext":"remote"}`},
		},
		{
			name: "hashed context store",
			files: map[string]string{
				"config.json":                  `{"currentContext":"remote"}`,
				"contexts/meta/stray":          "a file, not a context",
				"contexts/meta/aaa/meta.json":  `not json`,
				"contexts/meta/bbb/meta.json":  `{"Name":"other","Endpoints":{"docker":{"Host":"tcp://other:2375"}}}`,
				"contexts/meta/ccc/other.json": `{}`,
				"contexts/meta/ddd/meta.json":  `{"Name":"remote","Endpoints":{"docker":{"Host":"tcp://hashed:2375"}}}`,
			},
			host: "tcp://hashed:2375",
		},
		{
			name: "hashed context store without a match",
			files: map[string]string{
				"config.json":                 `{"currentContext":"remote"}`,
				"contexts/meta/bbb/meta.json": `{"Name":"other","Endpoints":{"docker":{"Host":"tcp://other:2375"}}}`,
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("DOCKER_CONFIG", dir)
			for name, body := range c.files {
				writeJSON(t, filepath.Join(dir, name), body)
			}

			host, use := resolveDockerHost("")
			assert.Equal(t, c.host != "", use)
			assert.Equal(t, c.host, host)
		})
	}

	t.Run("home directory", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("DOCKER_CONFIG", "")
		t.Setenv("HOME", home)
		writeJSON(t, filepath.Join(home, ".docker", "config.json"), `{"currentContext":"remote"}`)
		writeJSON(t, filepath.Join(home, ".docker", "contexts", "remote", "meta.json"),
			`{"Endpoints":{"docker":{"Host":"tcp://home:2375"}}}`)

		host, use := resolveDockerHost("")
		assert.True(t, use)
		assert.Equal(t, "tcp://home:2375", host)
	})
}

func TestInitClientRejectsABadHost(t *testing.T) {
	_, err := New(core.DockerConfig{Host: "not a url"})
	assert.ErrorContains(t, err, "failed to initialize Docker client")
}

func TestApplyPrivileges(t *testing.T) {
	hostConfig := &container.HostConfig{}
	applyPrivileges(hostConfig, nil)
	assert.Equal(t, &container.HostConfig{}, hostConfig)

	applyPrivileges(hostConfig, &core.Privileges{
		Capabilities: []string{"SYS_ADMIN"},
		Devices:      []string{"/dev/fuse"},
		Unconfined:   true,
		Privileged:   true,
	})
	assert.Equal(t, []string{"CAP_SYS_ADMIN"}, []string(hostConfig.CapAdd))
	assert.Equal(t, []container.DeviceMapping{{PathOnHost: "/dev/fuse", PathInContainer: "/dev/fuse", CgroupPermissions: "rwm"}}, hostConfig.Devices)
	assert.Equal(t, []string{"seccomp=unconfined", "apparmor=unconfined"}, hostConfig.SecurityOpt)
	assert.True(t, hostConfig.Privileged)
}
