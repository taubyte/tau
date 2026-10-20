package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPathPrefersTheEnvironment(t *testing.T) {
	t.Setenv(EnvConfig, "/x/config.yaml")
	p, err := Path()
	require.NoError(t, err)
	require.Equal(t, "/x/config.yaml", p)

	t.Setenv(EnvConfig, "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	p, err = Path()
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/xdg", "tau", "config.yaml"), p)

	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	_, err = Path()
	require.Error(t, err)
}

func TestSaveIsPrivateAndLoadReadsItBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tau", "config.yaml")
	c, err := Load(path)
	require.NoError(t, err)
	require.Empty(t, c.Names())

	c.Profiles["b"] = Profile{Cloud: "https://b", Token: "tb"}
	c.Profiles["a"] = Profile{Cloud: "https://a", Token: "ta"}
	c.Current = "a"
	require.NoError(t, c.Save())

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	back, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, back.Names())
	require.Equal(t, "tb", back.Profiles["b"].Token)
	require.Equal(t, path, back.File())
}

func TestLoadRefusals(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(bad, []byte("profiles: [not, a, map]"), 0o600))
	_, err := Load(bad)
	require.Error(t, err)

	empty := filepath.Join(dir, "empty.yaml")
	require.NoError(t, os.WriteFile(empty, []byte("current: x\n"), 0o600))
	c, err := Load(empty)
	require.NoError(t, err)
	require.NotNil(t, c.Profiles)

	_, err = Load(dir)
	require.Error(t, err, "a directory is not a config")
}

func TestSaveFailsWhereItCannotWrite(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	c := &Config{path: filepath.Join(file, "config.yaml"), Profiles: map[string]Profile{}}
	require.Error(t, c.Save())

	dir := t.TempDir()
	c = &Config{path: dir, Profiles: map[string]Profile{}}
	require.Error(t, c.Save(), "renaming over a directory fails")
}

func TestResolveLaysOverridesOverTheProfile(t *testing.T) {
	t.Setenv(EnvCloud, "")
	t.Setenv(EnvToken, "")
	c := &Config{Profiles: map[string]Profile{"a": {Cloud: "https://a", Token: "ta"}, "nocloud": {Token: "t"}}}

	_, _, err := c.Resolve("", "")
	require.ErrorContains(t, err, "tau login")

	c.Current = "a"
	name, p, err := c.Resolve("", "")
	require.NoError(t, err)
	require.Equal(t, "a", name)
	require.Equal(t, "ta", p.Token)

	_, p, err = c.Resolve("a", "https://flag")
	require.NoError(t, err)
	require.Equal(t, "https://flag", p.Cloud)

	t.Setenv(EnvCloud, "https://env")
	t.Setenv(EnvToken, "tenv")
	_, p, err = c.Resolve("missing", "")
	require.NoError(t, err, "the environment alone is a profile, for CI")
	require.Equal(t, Profile{Cloud: "https://env", Token: "tenv"}, p)

	t.Setenv(EnvCloud, "")
	_, _, err = c.Resolve("nocloud", "")
	require.ErrorContains(t, err, "names no cloud")
}

func TestLinks(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadLink(dir)
	require.ErrorIs(t, err, ErrNotLinked)

	require.NoError(t, WriteLink(dir, Link{ProjectID: "p", Name: "n"}))
	l, err := ReadLink(dir)
	require.NoError(t, err)
	require.Equal(t, "p", l.ProjectID)

	require.NoError(t, os.WriteFile(filepath.Join(dir, LinkFile), []byte("name: x\n"), 0o644))
	_, err = ReadLink(dir)
	require.ErrorIs(t, err, ErrNotLinked)

	require.NoError(t, os.WriteFile(filepath.Join(dir, LinkFile), []byte("[nope"), 0o644))
	_, err = ReadLink(dir)
	require.Error(t, err)

	require.NoError(t, os.Remove(filepath.Join(dir, LinkFile)))
	require.NoError(t, os.Mkdir(filepath.Join(dir, LinkFile), 0o755))
	_, err = ReadLink(dir)
	require.Error(t, err)

	file := filepath.Join(t.TempDir(), "f")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	require.Error(t, WriteLink(file, Link{ProjectID: "p"}))
}

// A token from the environment is used as it is: no refresh rides with it.
func TestAnEnvironmentTokenCarriesNoRefresh(t *testing.T) {
	t.Setenv(EnvCloud, "")
	t.Setenv(EnvToken, "tenv")
	c := &Config{Current: "a", Profiles: map[string]Profile{"a": {Cloud: "https://a", Token: "ta", Expires: 5, Refresh: "taur_x"}}}
	_, p, err := c.Resolve("", "")
	require.NoError(t, err)
	require.Equal(t, Profile{Cloud: "https://a", Token: "tenv"}, p)
}

func TestSaveWhereNoDirectoryCanBe(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	c := &Config{path: filepath.Join(file, "sub", "config.yaml"), Profiles: map[string]Profile{}}
	require.Error(t, c.Save())
	ro := t.TempDir()
	require.NoError(t, os.Chmod(ro, 0o500))
	t.Cleanup(func() { os.Chmod(ro, 0o700) })
	c = &Config{path: filepath.Join(ro, "config.yaml"), Profiles: map[string]Profile{}}
	require.Error(t, c.Save(), "no temp file in a read-only directory")
}

func TestLoadAndLinkWhereAFileIs(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(dir)
	require.Error(t, err, "a directory is not a config file")
	file := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	require.Error(t, WriteLink(file, Link{}), "a link cannot go under a file")
}

// A file that names no profiles at all still loads as one that can take one.
func TestLoadANullProfileMap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("profiles: null\n"), 0o600))
	c, err := Load(path)
	require.NoError(t, err)
	require.NotNil(t, c.Profiles)
	c.Profiles["dev"] = Profile{}
	require.NoError(t, c.Save())
}
