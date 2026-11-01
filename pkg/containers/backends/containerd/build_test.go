//go:build linux

package containerd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/containers/reserve"
)

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
		), dir, reserve.Default))

		content, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
		require.NoError(t, err)
		assert.Equal(t, "content of Dockerfile", string(content))

		_, err = os.Stat(filepath.Join(dir, "sub", "app.go"))
		assert.NoError(t, err, "nested files must be extracted")
	})

	t.Run("a file with no directory entry still lands", func(t *testing.T) {
		// Not every tar writer emits directory headers.
		dir := t.TempDir()

		require.NoError(t, extractContext(tarOf(t, file("deep/nested/app.go")), dir, reserve.Default))

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
		} {
			err := extractContext(tarOf(t, file(name)), dir, reserve.Default)
			require.Error(t, err, "%q must be refused", name)
			assert.Contains(t, err.Error(), "escapes")
		}

		_, err := os.Stat(filepath.Join(filepath.Dir(dir), "escaped.txt"))
		assert.True(t, os.IsNotExist(err), "an escaping entry must not be written")
	})

	// An absolute path is rooted into the build directory rather than refused:
	// it cannot escape, so it is harmless.
	t.Run("an absolute name is rooted into the build directory", func(t *testing.T) {
		for name, landed := range map[string]string{
			"/etc/cron.d/escaped": "etc/cron.d/escaped",
			"//etc/cron.d/twice":  "etc/cron.d/twice",
		} {
			dir := t.TempDir()
			require.NoError(t, extractContext(tarOf(t, file(name)), dir, reserve.Default), name)

			content, err := os.ReadFile(filepath.Join(dir, landed))
			require.NoError(t, err, "%s must land at %s", name, landed)
			assert.Equal(t, "content of "+name, string(content))
		}
	})

	t.Run("a link pointing outside the context is refused", func(t *testing.T) {
		for _, link := range []string{"/etc/shadow", "../../../etc/shadow"} {
			dir := t.TempDir()

			err := extractContext(tarOf(t, &tar.Header{
				Name:     "secret",
				Typeflag: tar.TypeSymlink,
				Linkname: link,
			}), dir, reserve.Default)

			require.Error(t, err, "a link to %q must be refused", link)
			assert.Contains(t, err.Error(), "outside")
		}
	})

	// Whatever links the tar has made, nothing after them may be written
	// beside the context.
	t.Run("no entry lands outside the context, whatever links precede it", func(t *testing.T) {
		for _, last := range []struct {
			what   string
			header *tar.Header
		}{
			{"a file", file("y/escaped")},
			{"a directory", &tar.Header{Name: "y/escaped", Typeflag: tar.TypeDir, Mode: 0o755}},
			{"a symlink", &tar.Header{Name: "y/escaped", Typeflag: tar.TypeSymlink, Linkname: "anything"}},
			{"a hard link", &tar.Header{Name: "y/escaped", Typeflag: tar.TypeLink, Linkname: "real"}},
		} {
			parent := t.TempDir()
			dir := filepath.Join(parent, "context")
			require.NoError(t, os.Mkdir(dir, 0o755))

			err := extractContext(tarOf(t,
				file("real"),
				&tar.Header{Name: "x", Typeflag: tar.TypeSymlink, Linkname: "."},
				&tar.Header{Name: "x/y", Typeflag: tar.TypeSymlink, Linkname: ".."},
				last.header,
			), dir, reserve.Default)
			assert.Error(t, err, "%s must be refused", last.what)

			beside, err := os.ReadDir(parent)
			require.NoError(t, err)
			require.Len(t, beside, 1, "%s was written beside the build context", last.what)
		}
	})

	// Nor may an entry write through such a link, or make a hard link to what
	// lies beyond one.
	t.Run("no entry reaches outside the context through a link", func(t *testing.T) {
		for _, c := range []struct {
			what   string
			via    string
			header *tar.Header
		}{
			{"a file written over the link", "../victim", file("y")},
			{"a hard link made through it", "..", &tar.Header{Name: "h", Typeflag: tar.TypeLink, Linkname: "y/victim"}},
		} {
			parent := t.TempDir()
			dir := filepath.Join(parent, "context")
			require.NoError(t, os.Mkdir(dir, 0o755))
			victim := filepath.Join(parent, "victim")
			require.NoError(t, os.WriteFile(victim, []byte("untouched"), 0o644))

			err := extractContext(tarOf(t,
				&tar.Header{Name: "x", Typeflag: tar.TypeSymlink, Linkname: "."},
				&tar.Header{Name: "x/y", Typeflag: tar.TypeSymlink, Linkname: c.via},
				c.header,
			), dir, reserve.Default)
			assert.Error(t, err, "%s must be refused", c.what)

			content, err := os.ReadFile(victim)
			require.NoError(t, err)
			assert.Equal(t, "untouched", string(content), "%s changed a file outside the context", c.what)

			info, err := os.Stat(victim)
			require.NoError(t, err)
			assert.Equal(t, uint64(1), uint64(info.Sys().(*syscall.Stat_t).Nlink), "%s linked to a file outside the context", c.what)
		}
	})

	// A link is judged by where it leads from where it sits on disk, after
	// every entry is in place, not from where the tar names it.
	t.Run("a link that leads out of the context from where it lands is refused", func(t *testing.T) {
		directory := func(name string) *tar.Header {
			return &tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755}
		}
		symlink := func(name, target string) *tar.Header {
			return &tar.Header{Name: name, Typeflag: tar.TypeSymlink, Linkname: target}
		}
		hardlink := func(name, target string) *tar.Header {
			return &tar.Header{Name: name, Typeflag: tar.TypeLink, Linkname: target}
		}

		for _, c := range []struct {
			what    string
			entries []*tar.Header
		}{
			{"under a link to the context", []*tar.Header{symlink("x", "."), symlink("x/y", "..")}},
			{"through a link made after it", []*tar.Header{symlink("l", "x/.."), symlink("x", ".")}},
			{"hard linked into another directory", []*tar.Header{directory("a/b"), symlink("a/b/s", "../.."), hardlink("h", "a/b/s")}},
			{"named with a trailing slash", []*tar.Header{symlink("x", "."), symlink("x/z", ".."), symlink("a", "z/")}},
			{"in a loop", []*tar.Header{symlink("a", "b"), symlink("b", "a")}},
		} {
			err := extractContext(tarOf(t, c.entries...), t.TempDir(), reserve.Default)
			require.Error(t, err, "a link %s must be refused", c.what)
			assert.Contains(t, err.Error(), "does not resolve inside", c.what)
		}
	})

	t.Run("a link that leads nowhere is kept", func(t *testing.T) {
		dir := t.TempDir()

		links := map[string]string{"generated": "out/app", "through": "f/child", "slashed": "f/"}
		entries := []*tar.Header{file("f")}
		for name, target := range links {
			entries = append(entries, &tar.Header{Name: name, Typeflag: tar.TypeSymlink, Linkname: target})
		}
		require.NoError(t, extractContext(tarOf(t, entries...), dir, reserve.Default))

		for name, want := range links {
			target, err := os.Readlink(filepath.Join(dir, name))
			require.NoError(t, err, name)
			assert.Equal(t, want, target, name)
		}
	})

	t.Run("a hard link names an earlier entry from the root of the tar", func(t *testing.T) {
		dir := t.TempDir()

		require.NoError(t, extractContext(tarOf(t,
			file("sub/real"),
			&tar.Header{Name: "other/hard", Typeflag: tar.TypeLink, Linkname: "sub/real"},
			&tar.Header{Name: "rooted", Typeflag: tar.TypeLink, Linkname: "/sub/real"},
		), dir, reserve.Default))

		real, err := os.Stat(filepath.Join(dir, "sub", "real"))
		require.NoError(t, err)

		for _, name := range []string{"other/hard", "rooted"} {
			info, err := os.Lstat(filepath.Join(dir, name))
			require.NoError(t, err, name)
			assert.True(t, info.Mode().IsRegular(), "%s must be a hard link, not a symlink", name)
			assert.True(t, os.SameFile(real, info), "%s must be the entry it names", name)
		}
	})

	// A hard link may only name an entry of the tar: never a file of the
	// working directory, and never one outside the context.
	t.Run("a hard link to anything but an earlier entry is refused", func(t *testing.T) {
		cwd := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(cwd, "secret"), []byte("host"), 0o644))
		t.Chdir(cwd)

		for _, target := range []string{"secret", "../secret", "/etc/hostname"} {
			dir := t.TempDir()

			err := extractContext(tarOf(t,
				&tar.Header{Name: "stolen", Typeflag: tar.TypeLink, Linkname: target},
			), dir, reserve.Default)
			require.Error(t, err, "a hard link to %q must be refused", target)

			_, err = os.Lstat(filepath.Join(dir, "stolen"))
			assert.True(t, os.IsNotExist(err), "a hard link to %q must not be made", target)
		}
	})

	t.Run("setuid, setgid and sticky are dropped", func(t *testing.T) {
		dir := t.TempDir()

		// What this process's umask makes of an ordinary 0755.
		require.NoError(t, os.WriteFile(filepath.Join(dir, "probe"), nil, 0o755))
		probe, err := os.Stat(filepath.Join(dir, "probe"))
		require.NoError(t, err)

		special := []int64{0o4000, 0o2000, 0o1000, 0o7000}
		var entries []*tar.Header
		for i, bits := range special {
			entries = append(entries,
				&tar.Header{Name: "file" + strconv.Itoa(i), Typeflag: tar.TypeReg, Mode: 0o755 | bits},
				&tar.Header{Name: "dir" + strconv.Itoa(i), Typeflag: tar.TypeDir, Mode: 0o755 | bits},
			)
		}
		require.NoError(t, extractContext(tarOf(t, entries...), dir, reserve.Default))

		for i := range special {
			for _, name := range []string{"file" + strconv.Itoa(i), "dir" + strconv.Itoa(i)} {
				info, err := os.Stat(filepath.Join(dir, name))
				require.NoError(t, err, name)
				assert.Zero(t, info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky), "%s kept a special bit", name)
				assert.Equal(t, probe.Mode().Perm(), info.Mode().Perm(), "%s must keep its permission bits", name)
			}
		}
	})

	t.Run("ownership is not taken from the tar", func(t *testing.T) {
		dir := t.TempDir()

		owned := file("owned")
		owned.Uid, owned.Gid = 4242, 4242
		require.NoError(t, extractContext(tarOf(t, owned), dir, reserve.Default))

		info, err := os.Stat(filepath.Join(dir, "owned"))
		require.NoError(t, err)
		stat := info.Sys().(*syscall.Stat_t)
		assert.Equal(t, uint32(os.Geteuid()), stat.Uid)
		assert.Equal(t, uint32(os.Getegid()), stat.Gid)
	})

	t.Run("a link inside the context is kept", func(t *testing.T) {
		dir := t.TempDir()

		require.NoError(t, extractContext(tarOf(t,
			file("real.txt"),
			&tar.Header{Name: "link.txt", Typeflag: tar.TypeSymlink, Linkname: "real.txt"},
		), dir, reserve.Default))

		target, err := os.Readlink(filepath.Join(dir, "link.txt"))
		require.NoError(t, err)
		assert.Equal(t, "real.txt", target)
	})

	t.Run("device and fifo entries are skipped rather than created", func(t *testing.T) {
		for _, kind := range []byte{tar.TypeChar, tar.TypeBlock, tar.TypeFifo} {
			dir := t.TempDir()

			require.NoError(t, extractContext(tarOf(t,
				&tar.Header{Name: "dev/node", Typeflag: kind, Mode: 0o666},
				file("Dockerfile"),
			), dir, reserve.Default))

			_, err := os.Lstat(filepath.Join(dir, "dev", "node"))
			assert.True(t, os.IsNotExist(err), "type %q has no place in a build context", kind)

			_, err = os.Stat(filepath.Join(dir, "Dockerfile"))
			assert.NoError(t, err, "entries after a skipped one must still be extracted")
		}
	})

	t.Run("a truncated tar is an error", func(t *testing.T) {
		err := extractContext(strings.NewReader("this is not a tar"), t.TempDir(), reserve.Default)
		assert.Error(t, err)
	})

	t.Run("a corrupt gzip stream is an error", func(t *testing.T) {
		err := extractContext(bytes.NewReader([]byte{0x1f, 0x8b, 0xff, 0xff}), t.TempDir(), reserve.Default)
		assert.ErrorContains(t, err, "reading build context")
	})

	t.Run("a file cut short is an error", func(t *testing.T) {
		var buf bytes.Buffer
		writer := tar.NewWriter(&buf)
		require.NoError(t, writer.WriteHeader(&tar.Header{Name: "Dockerfile", Typeflag: tar.TypeReg, Mode: 0o644, Size: 100}))
		_, err := writer.Write(bytes.Repeat([]byte("x"), 100))
		require.NoError(t, err)
		require.NoError(t, writer.Close())

		// The header and the start of the body, as a transfer that died leaves it.
		err = extractContext(bytes.NewReader(buf.Bytes()[:512+10]), t.TempDir(), reserve.Default)
		assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
	})

	t.Run("a directory that does not exist is an error", func(t *testing.T) {
		err := extractContext(tarOf(t, file("Dockerfile")), filepath.Join(t.TempDir(), "missing"), reserve.Default)
		assert.Error(t, err)
	})

	t.Run("entries that contradict each other are refused", func(t *testing.T) {
		directory := func(name string) *tar.Header {
			return &tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755}
		}
		link := func(name string) *tar.Header {
			return &tar.Header{Name: name, Typeflag: tar.TypeSymlink, Linkname: "x"}
		}
		hardlink := func(name string) *tar.Header {
			return &tar.Header{Name: name, Typeflag: tar.TypeLink, Linkname: "a"}
		}

		for _, c := range []struct {
			what    string
			entries []*tar.Header
			kept    os.FileMode // the type the first entry gave a, which must survive
		}{
			{"a directory under a file", []*tar.Header{file("a"), directory("a/b")}, 0},
			{"a file under a file", []*tar.Header{file("a"), file("a/b")}, 0},
			{"a link under a file", []*tar.Header{file("a"), link("a/b")}, 0},
			{"a hard link under a file", []*tar.Header{file("a"), hardlink("a/b")}, 0},
			{"a file where a directory is", []*tar.Header{directory("a"), file("a")}, os.ModeDir},
			{"a link where a file is", []*tar.Header{file("a"), link("a")}, 0},
		} {
			dir := t.TempDir()
			assert.Error(t, extractContext(tarOf(t, c.entries...), dir, reserve.Default), c.what)

			info, err := os.Lstat(filepath.Join(dir, "a"))
			require.NoError(t, err, c.what)
			assert.Equal(t, c.kept, info.Mode().Type(), "%s: the entry already extracted must be left as it was", c.what)
		}
	})
}

func TestCheckLinks(t *testing.T) {
	t.Run("a context that cannot be walked is an error", func(t *testing.T) {
		root, err := os.OpenRoot(t.TempDir())
		require.NoError(t, err)
		require.NoError(t, root.Close())

		assert.Error(t, checkLinks(root))
	})
}

func TestConfine(t *testing.T) {
	for name, want := range map[string]string{
		"Dockerfile": "Dockerfile",
		"sub/app.go": "sub/app.go",
		"/rooted":    "rooted",
		"//rooted":   "rooted",
		"./x":        "x",
	} {
		got, err := confine(name)
		require.NoError(t, err, "%q", name)
		assert.Equal(t, want, got, "%q", name)
	}

	for _, name := range []string{"../x", "sub/../../x", "../../../etc/passwd"} {
		_, err := confine(name)
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

func TestBuildKitRecipe(t *testing.T) {
	rootless := buildkitFlags(true)
	require.Len(t, rootless, 1)

	assert.Contains(t, rootless[0], "--oci-worker-no-process-sandbox",
		"BuildKit must not nest a user namespace inside the one it runs in")
	assert.Contains(t, rootless[0], "--oci-worker-rootless",
		"its per-step runc must not expect a cgroup of its own")
	assert.Contains(t, rootless[0], "--oci-worker-snapshotter=native",
		"overlayfs cannot be mounted from a user namespace")

	rootful := buildkitFlags(false)
	require.Len(t, rootful, 1)
	assert.NotContains(t, rootful[0], "rootless",
		"as real root BuildKit's defaults are right, and asking for rootless mode makes it refuse to start")

	for _, flags := range [][]string{rootless, rootful} {
		assert.Contains(t, flags[0], "--config="+buildConfigPath,
			"without its config the per-step runc gets no cgroup parent, and the steps escape the egress rule")
	}

	assert.NotContains(t, buildkitImage, ":latest",
		"the builder must be pinned so builds do not change underneath us")
}

func TestTheBuilderGetsItsOwnCgroupHierarchy(t *testing.T) {
	spec := &specs.Spec{
		Linux: &specs.Linux{},
		Mounts: []specs.Mount{
			{Destination: "/proc", Type: "proc", Source: "proc"},
			{Destination: cgroupPath, Type: "cgroup", Source: "cgroup", Options: []string{"ro"}},
		},
	}
	require.NoError(t, withOwnCgroupHierarchy()(context.Background(), nil, nil, spec))

	assert.Contains(t, spec.Linux.Namespaces, specs.LinuxNamespace{Type: specs.CgroupNamespace})

	var cgroupMounts []specs.Mount
	for _, m := range spec.Mounts {
		if m.Destination == cgroupPath {
			cgroupMounts = append(cgroupMounts, m)
		}
	}
	require.Len(t, cgroupMounts, 1)
	assert.Equal(t, "cgroup", cgroupMounts[0].Type)
	assert.Contains(t, cgroupMounts[0].Options, "rw")
	assert.NotContains(t, cgroupMounts[0].Options, "ro")
	assert.Equal(t, "/proc", spec.Mounts[0].Destination)
}

func TestBuildPrivileges(t *testing.T) {
	rootless := buildPrivileges(true)
	assert.Contains(t, rootless.Capabilities, "SYS_ADMIN", "BuildKit mounts layers to assemble them")
	assert.Contains(t, rootless.Devices, "/dev/fuse", "the rootless snapshotter needs it")
	assert.True(t, rootless.Unconfined)
	assert.False(t, rootless.Privileged,
		"under a user namespace the narrow set is enough, and privileged would not reach past it anyway")

	rootful := buildPrivileges(false)
	assert.True(t, rootful.Privileged,
		"the per-step runc attaches a BPF device filter, which SYS_ADMIN alone does not permit")
	assert.True(t, rootful.Unconfined)
	assert.Empty(t, rootful.Devices,
		"a device the host may not have must not be demanded where it is not needed")
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

	t.Run("privileged does not depend on this process's own capabilities", func(t *testing.T) {
		// This test runs unprivileged. containerd's own WithPrivileged copies
		// the caller's capability set, which would be empty here — and empty is
		// exactly what an unprivileged tau would hand a root daemon, producing
		// a container that claims to be privileged and cannot mount anything.
		spec := applySpecOpts(t, &core.ContainerConfig{
			Privileges: &core.Privileges{Privileged: true},
		})

		require.NotNil(t, spec.Process.Capabilities)
		assert.Contains(t, spec.Process.Capabilities.Bounding, "CAP_SYS_ADMIN")
		assert.Contains(t, spec.Process.Capabilities.Effective, "CAP_SYS_ADMIN")
		assert.Greater(t, len(spec.Process.Capabilities.Bounding), 20,
			"privileged means every capability, not the caller's")
	})

	t.Run("a device comes with the rule that lets the container open it", func(t *testing.T) {
		spec := applySpecOpts(t, &core.ContainerConfig{
			Privileges: &core.Privileges{Devices: []string{"/dev/null"}},
		})

		var device *specs.LinuxDevice
		for i := range spec.Linux.Devices {
			if spec.Linux.Devices[i].Path == "/dev/null" {
				device = &spec.Linux.Devices[i]
			}
		}
		require.NotNil(t, device, "the device must be in the container")

		allowed := false
		for _, rule := range spec.Linux.Resources.Devices {
			if rule.Allow && rule.Access == "rwm" &&
				rule.Major != nil && *rule.Major == device.Major &&
				rule.Minor != nil && *rule.Minor == device.Minor {
				allowed = true
			}
		}
		assert.True(t, allowed, "a device node without a cgroup rule cannot be opened")
	})

	t.Run("a container asks for no privileges by default", func(t *testing.T) {
		spec := applySpecOpts(t, &core.ContainerConfig{Command: []string{"true"}})

		if spec.Process.Capabilities != nil {
			assert.NotContains(t, spec.Process.Capabilities.Bounding, "CAP_SYS_ADMIN",
				"only a container that asks for it may have SYS_ADMIN")
		}
	})
}

func TestExtractContextAcceptsGzip(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		dir := t.TempDir()

		var raw bytes.Buffer
		writer := tar.NewWriter(&raw)
		require.NoError(t, writer.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0o644, Size: 4}))
		_, err := writer.Write([]byte("FROM"))
		require.NoError(t, err)
		require.NoError(t, writer.Close())

		source := io.Reader(bytes.NewReader(raw.Bytes()))
		if compressed {
			var zipped bytes.Buffer
			gz := gzip.NewWriter(&zipped)
			_, err := gz.Write(raw.Bytes())
			require.NoError(t, err)
			require.NoError(t, gz.Close())
			source = bytes.NewReader(zipped.Bytes())
		}

		require.NoError(t, extractContext(source, dir, reserve.Default), "gzipped=%v", compressed)

		content, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
		require.NoError(t, err, "gzipped=%v", compressed)
		assert.Equal(t, "FROM", string(content))
	}
}

// filled is a filesystem holding whatever is under dir, out of the bytes and
// inodes it was given.
type filled struct {
	dir           string
	bytes, inodes int64
}

func (f filled) measure(string) (reserve.Usage, error) {
	var used, entries int64
	err := filepath.WalkDir(f.dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == f.dir {
			return err
		}
		entries++
		info, err := entry.Info()
		if err == nil && info.Mode().IsRegular() {
			used += info.Size()
		}
		return err
	})
	return reserve.Usage{
		Size: 100 << 20, Free: uint64(max(f.bytes-used, 0)),
		Files: 100, FreeFiles: uint64(max(f.inodes-entries, 0)),
	}, err
}

func TestExtractContextLeavesTheDiskItsReserve(t *testing.T) {
	big := &tar.Header{Name: "big", Typeflag: tar.TypeReg, Mode: 0o644, Size: 2 << 20}
	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	require.NoError(t, writer.WriteHeader(big))
	_, err := writer.Write(bytes.Repeat([]byte("x"), 2<<20))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	for _, c := range []struct {
		name    string
		context io.Reader
		inodes  int64
		refused reserve.Error
	}{
		{"bytes", &buf, 100, reserve.Error{Unit: "bytes", Free: 50 << 20, Want: 1 << 20, Reserve: 50 << 20}},
		{"inodes", tarOf(t, file("a"), file("b"), file("c")), 7, reserve.Error{Unit: "inodes", Free: 5, Want: 1, Reserve: 5}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			floor := reserve.Floor{Space: 0.5, Inodes: 0.05, Measure: filled{dir: dir, bytes: 51 << 20, inodes: c.inodes}.measure}

			var refused *reserve.Error
			require.ErrorAs(t, extractContext(c.context, dir, floor), &refused)
			refused.Path = ""
			require.Equal(t, c.refused, *refused)
		})
	}
}

func TestRestrictedResources(t *testing.T) {
	if !resourceLimitsAvailable() {
		t.Skip("no cgroup subtree here to carry limits")
	}

	restricted := &core.ContainerConfig{Network: &core.NetworkConfig{RestrictEgress: true}}
	assert.Equal(t, int64(defaultRestrictedPIDs), restrictedResources(restricted).PIDs)

	assert.Nil(t, restrictedResources(&core.ContainerConfig{}),
		"a container that asked for nothing and is not restricted keeps nothing")

	chosen := &core.ContainerConfig{
		Network:   &core.NetworkConfig{RestrictEgress: true},
		Resources: &core.ResourceLimits{PIDs: 7},
	}
	assert.Equal(t, int64(7), restrictedResources(chosen).PIDs, "a caller's own limit wins")

	memoryOnly := &core.ContainerConfig{
		Network:   &core.NetworkConfig{RestrictEgress: true},
		Resources: &core.ResourceLimits{Memory: 1 << 20},
	}
	limits := restrictedResources(memoryOnly)
	assert.Equal(t, core.ResourceLimits{Memory: 1 << 20, PIDs: defaultRestrictedPIDs}, *limits)
	assert.Zero(t, memoryOnly.Resources.PIDs, "the caller's config is not modified")
}
