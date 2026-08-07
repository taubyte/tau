//go:build linux

package containerd

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

// tarOf builds a context tarball from a list of entries.
func tarOf(t *testing.T, entries ...*tar.Header) io.Reader {
	t.Helper()

	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)

	for _, header := range entries {
		body := ""
		if header.Typeflag == tar.TypeReg {
			body = "content of " + header.Name
			header.Size = int64(len(body))
		}
		require.NoError(t, writer.WriteHeader(header))
		if body != "" {
			_, err := io.WriteString(writer, body)
			require.NoError(t, err)
		}
	}

	require.NoError(t, writer.Close())

	return &buf
}

func file(name string) *tar.Header {
	return &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644}
}

func TestExtractContext(t *testing.T) {
	t.Run("files and directories", func(t *testing.T) {
		dir := t.TempDir()

		require.NoError(t, extractContext(tarOf(t,
			&tar.Header{Name: "sub", Typeflag: tar.TypeDir, Mode: 0o755},
			file("Dockerfile"),
			file("sub/app.go"),
		), dir))

		content, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
		require.NoError(t, err)
		assert.Equal(t, "content of Dockerfile", string(content))

		_, err = os.Stat(filepath.Join(dir, "sub", "app.go"))
		assert.NoError(t, err, "nested files must be extracted")
	})

	t.Run("a file with no directory entry still lands", func(t *testing.T) {
		// Not every tar writer emits directory headers.
		dir := t.TempDir()

		require.NoError(t, extractContext(tarOf(t, file("deep/nested/app.go")), dir))

		_, err := os.Stat(filepath.Join(dir, "deep", "nested", "app.go"))
		assert.NoError(t, err)
	})

	// The context comes from a user's repository. These are the entries a
	// malicious one would use to write outside the build or read the host.
	t.Run("a path escaping the build directory is refused", func(t *testing.T) {
		dir := t.TempDir()

		for _, name := range []string{
			"../escaped.txt",
			"sub/../../escaped.txt",
			"/etc/cron.d/escaped",
		} {
			err := extractContext(tarOf(t, file(name)), dir)
			if name == "/etc/cron.d/escaped" {
				// An absolute path is rooted into the build directory rather
				// than refused: it cannot escape, so it is harmless.
				require.NoError(t, err, "%s", name)
				continue
			}
			require.Error(t, err, "%q must be refused", name)
			assert.Contains(t, err.Error(), "escapes")
		}

		// Nothing may exist above the build directory.
		_, err := os.Stat(filepath.Join(filepath.Dir(dir), "escaped.txt"))
		assert.True(t, os.IsNotExist(err), "an escaping entry must not be written")
	})

	t.Run("a link pointing outside the context is refused", func(t *testing.T) {
		for _, link := range []string{"/etc/shadow", "../../../etc/shadow"} {
			dir := t.TempDir()

			err := extractContext(tarOf(t, &tar.Header{
				Name:     "secret",
				Typeflag: tar.TypeSymlink,
				Linkname: link,
			}), dir)

			require.Error(t, err, "a link to %q must be refused", link)
			assert.Contains(t, err.Error(), "outside")
		}
	})

	t.Run("a link inside the context is kept", func(t *testing.T) {
		dir := t.TempDir()

		require.NoError(t, extractContext(tarOf(t,
			file("real.txt"),
			&tar.Header{Name: "link.txt", Typeflag: tar.TypeSymlink, Linkname: "real.txt"},
		), dir))

		target, err := os.Readlink(filepath.Join(dir, "link.txt"))
		require.NoError(t, err)
		assert.Equal(t, "real.txt", target)
	})

	t.Run("device entries are skipped rather than created", func(t *testing.T) {
		dir := t.TempDir()

		require.NoError(t, extractContext(tarOf(t,
			&tar.Header{Name: "dev/null", Typeflag: tar.TypeChar, Mode: 0o666},
			file("Dockerfile"),
		), dir))

		_, err := os.Stat(filepath.Join(dir, "dev", "null"))
		assert.True(t, os.IsNotExist(err), "a device node has no place in a build context")

		_, err = os.Stat(filepath.Join(dir, "Dockerfile"))
		assert.NoError(t, err, "entries after a skipped one must still be extracted")
	})

	t.Run("a truncated tar is an error", func(t *testing.T) {
		err := extractContext(strings.NewReader("this is not a tar"), t.TempDir())
		assert.Error(t, err)
	})
}

func TestConfine(t *testing.T) {
	dir := "/build"

	for _, name := range []string{"Dockerfile", "sub/app.go", "/rooted", "./x"} {
		target, err := confine(dir, name)
		require.NoError(t, err, "%q", name)
		assert.True(t, strings.HasPrefix(target, dir+"/"), "%q -> %q", name, target)
	}

	for _, name := range []string{"../x", "sub/../../x", "../../../etc/passwd"} {
		_, err := confine(dir, name)
		assert.Error(t, err, "%q must be refused", name)
	}
}

func TestBuildRequiresContext(t *testing.T) {
	image := (&ContainerdBackend{client: &Client{}}).Image("x:latest")

	assert.Error(t, image.Build(t.Context(), nil), "a nil input must not panic")
	assert.Error(t, image.Build(t.Context(), &core.DockerfileBuild{}), "a nil context must be refused")
}

func TestBuildWithoutClient(t *testing.T) {
	err := (&ContainerdBackend{}).Image("x:latest").
		Build(t.Context(), &core.DockerfileBuild{Context: strings.NewReader("")})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

// The recipe is what makes BuildKit work inside an already-unprivileged
// container; each part of it is load-bearing and easy to lose in a refactor.
func TestBuildKitRecipe(t *testing.T) {
	assert.Contains(t, buildkitFlags, "--oci-worker-no-process-sandbox",
		"BuildKit must not nest a user namespace inside the one it runs in")
	assert.Contains(t, buildkitFlags, "--oci-worker-snapshotter=native",
		"overlayfs cannot be mounted from a user namespace")

	assert.NotContains(t, buildkitImage, ":latest",
		"the builder must be pinned so builds do not change underneath us")
}

func TestPrivilegeSpecOpts(t *testing.T) {
	t.Run("nothing without privileges", func(t *testing.T) {
		assert.Empty(t, privilegeSpecOpts(nil))
	})

	t.Run("capabilities are prefixed and added", func(t *testing.T) {
		spec := applySpecOpts(t, &core.ContainerConfig{
			Privileges: &core.Privileges{Capabilities: []string{"SYS_ADMIN"}},
		})

		require.NotNil(t, spec.Process.Capabilities)
		assert.Contains(t, spec.Process.Capabilities.Bounding, "CAP_SYS_ADMIN",
			"the OCI spec names capabilities with the CAP_ prefix")
		assert.Contains(t, spec.Process.Capabilities.Effective, "CAP_SYS_ADMIN")
	})

	t.Run("unconfined drops seccomp", func(t *testing.T) {
		spec := applySpecOpts(t, &core.ContainerConfig{
			Privileges: &core.Privileges{Unconfined: true},
		})

		assert.Nil(t, spec.Linux.Seccomp)
	})

	t.Run("a container asks for no privileges by default", func(t *testing.T) {
		spec := applySpecOpts(t, &core.ContainerConfig{Command: []string{"true"}})

		if spec.Process.Capabilities != nil {
			assert.NotContains(t, spec.Process.Capabilities.Bounding, "CAP_SYS_ADMIN",
				"only a container that asks for it may have SYS_ADMIN")
		}
	})
}
