//go:build linux

package containerd

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/containers/reserve"
)

// containerd cannot build images itself, so BuildKit is run as an ordinary
// container on this very backend and its output imported. Nothing has to be
// installed on the host and no daemon has to be kept alive for it: the build
// is one container that exits when it is done.
//
// Pinned rather than tracking latest — a builder that changes underneath you
// changes everyone's builds.
const buildkitImage = "docker.io/moby/buildkit:v0.32.2"

// Where the build's inputs and outputs are mounted inside that container.
const (
	buildContextPath = "/tau/context"
	buildOutputPath  = "/tau/out"
	buildOutputFile  = "image.tar"
	buildConfigPath  = "/tau/buildkitd.toml"
	cgroupPath       = "/sys/fs/cgroup"
)

// buildkitFlags configures the buildkitd that buildctl-daemonless.sh starts.
//
// The flags depend on how containerd itself runs, because they exist purely to
// cope with being unprivileged. Asking for them as real root does not merely
// waste them: buildkitd refuses to start at all, with "rootless mode requires
// to be executed as the mapped root in a user namespace".
//
// Under a rootful containerd the build container is real root, with cgroups and
// overlayfs available, and BuildKit's own defaults are the right ones.
//
// Under a rootless one it needs all three:
//
//   - no-process-sandbox: BuildKit would otherwise create a user namespace of
//     its own. We are already in one, and nesting fails with
//     "newuidmap: Operation not permitted".
//   - rootless worker: the runc BuildKit runs for each step must not expect a
//     cgroup of its own, or every RUN fails with "no cgroup mount found in
//     mountinfo".
//   - native snapshotter: overlayfs cannot be mounted from within a user
//     namespace, so the slower snapshotter that only copies is the one that works.
func buildkitFlags(rootless bool) []string {
	flags := "--config=" + buildConfigPath
	if rootless {
		flags += " --oci-worker-no-process-sandbox" +
			" --oci-worker-rootless" +
			" --oci-worker-snapshotter=native"
	}

	return []string{"BUILDKITD_FLAGS=" + flags}
}

// writeBuildKitConfig writes the buildkitd configuration the builder is started
// with, and returns its path on the host.
//
// It exists for one setting, and only for a builder that sees the host's cgroup
// hierarchy (see withOwnCgroupHierarchy for the one that does not). BuildKit
// runs every RUN step in a runc container of its own, under a "buildkit" group
// at the root of the hierarchy it sees unless it is told a parent — the host's
// root, outside the subtree the egress rule matches, so a Dockerfile could do
// what build.sh cannot. Naming the restricted parent puts every step inside it:
// the rule matches the ancestor at that level, so nesting depth below it does
// not matter. The hierarchy is bind mounted at the same paths, so the host path
// is the right one to name.
func writeBuildKitConfig(workDir string, restrictSteps bool) (string, error) {
	path := filepath.Join(workDir, "buildkitd.toml")

	config := "[worker.oci]\n"
	if restrictSteps {
		parent, err := restrictedCgroupParent()
		if err != nil {
			return "", err
		}
		config += fmt.Sprintf("  defaultCgroupParent = %q\n", parent)
	}

	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		return "", fmt.Errorf("failed to write the builder configuration: %w", err)
	}
	return path, nil
}

// buildPrivileges is what the builder needs loosened, and it differs by mode
// for the same reason the flags do.
//
// Rootless: the user namespace is the real boundary, so the narrow set is
// enough and stays inside it. SYS_ADMIN to mount layers, /dev/fuse for the
// snapshotter a rootless build falls back to.
//
// Rootful: the runc BuildKit runs per step attaches a BPF device filter to its
// cgroup, which SYS_ADMIN alone does not permit — "bpf_prog_query
// (BPF_CGROUP_DEVICE) failed: operation not permitted". The process is already
// real root on the host there, so dropping the container's confinement grants
// it nothing it did not already have.
func buildPrivileges(rootless bool) *core.Privileges {
	if !rootless {
		return &core.Privileges{Unconfined: true, Privileged: true}
	}

	return &core.Privileges{
		Capabilities: []string{"SYS_ADMIN"},
		Devices:      []string{"/dev/fuse"},
		Unconfined:   true,
	}
}

// Build builds the image from a Dockerfile by running BuildKit in a container
// and importing what it produces.
func (i *containerdImage) Build(ctx context.Context, input *core.DockerfileBuild) error {
	if i.backend.client == nil {
		return fmt.Errorf("containerd client not initialized")
	}

	if input == nil || input.Context == nil {
		return fmt.Errorf("build requires a context")
	}

	workDir, err := os.MkdirTemp("", "tau-build-*")
	if err != nil {
		return fmt.Errorf("failed to create build directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	contextDir := filepath.Join(workDir, "context")
	outputDir := filepath.Join(workDir, "out")

	for _, dir := range []string{contextDir, outputDir} {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return fmt.Errorf("failed to create %s: %w", dir, err)
		}
		// Explicitly, because MkdirAll applies the umask: under a rootful
		// containerd the build runs as real root and writes its result back here.
		if err := os.Chmod(dir, 0o777); err != nil {
			return fmt.Errorf("failed to open up %s: %w", dir, err)
		}
	}

	if err := extractContext(input.Context, contextDir, reserve.Default); err != nil {
		return fmt.Errorf("failed to unpack build context: %w", err)
	}

	configPath, err := writeBuildKitConfig(workDir, input.RestrictEgress && i.backend.isRootlessMode())
	if err != nil {
		return err
	}

	if err := i.runBuildKit(ctx, contextDir, outputDir, configPath, input.DockerfileName(), input.RestrictEgress); err != nil {
		return err
	}

	return i.importBuilt(ctx, filepath.Join(outputDir, buildOutputFile))
}

// The moby/buildkit image sets BUILDKIT_SETUP_CGROUPV2_ROOT, and buildkitd then
// moves every process it finds at the root of the cgroup hierarchy it sees,
// itself included, into an "init" group there, and enables every controller at
// that root. Shown the host's hierarchy, that root is the host's: buildkitd left
// the cgroup its build was created in, with the build's limits and, restricted,
// the egress rule its own fetches had to pass, and it changed which controllers
// the host's root hands down.
//
// keepCgroup turns that off for the builder that sees the host's hierarchy.
const keepCgroup = "BUILDKIT_SETUP_CGROUPV2_ROOT=0"

// withOwnCgroupHierarchy gives the builder a cgroup namespace of its own, rooted
// at the cgroup containerd creates it in, and a writable cgroup2 mount of that
// namespace in place of the read-only one. The root buildkitd sees is then the
// build's own cgroup: it moves into an "init" group below it, enables the
// controllers there for the steps, and the runc it starts for each RUN step
// creates the step's cgroup below it too. Everything the build runs stays
// inside the cgroup its limits and the egress rule are attached to, each step
// in a cgroup of its own, and nothing above it is written.
func withOwnCgroupHierarchy() oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, spec *specs.Spec) error {
		if err := oci.WithLinuxNamespace(specs.LinuxNamespace{Type: specs.CgroupNamespace})(ctx, client, c, spec); err != nil {
			return err
		}

		mounts := []specs.Mount{}
		for _, m := range spec.Mounts {
			if m.Destination != cgroupPath {
				mounts = append(mounts, m)
			}
		}
		spec.Mounts = append(mounts, specs.Mount{
			Destination: cgroupPath,
			Type:        "cgroup",
			Source:      "cgroup",
			Options:     []string{"nosuid", "noexec", "nodev", "relatime", "rw"},
		})
		return nil
	}
}

// runBuildKit runs one build and returns the builder's own output on failure —
// that output is the compiler error, the missing package, the failed RUN.
func (i *containerdImage) runBuildKit(ctx context.Context, contextDir, outputDir, configPath, dockerfile string, restrictEgress bool) error {
	rootless := i.backend.isRootlessMode()

	env := buildkitFlags(rootless)
	volumes := []core.VolumeMount{
		{Source: contextDir, Destination: buildContextPath, ReadOnly: true},
		{Source: outputDir, Destination: buildOutputPath},
		{Source: configPath, Destination: buildConfigPath, ReadOnly: true},
	}
	var own []oci.SpecOpts
	if rootless {
		// BuildKit runs a runc of its own for every step, and that runc
		// refuses to start without a cgroup hierarchy it can see: "no cgroup
		// mount found in mountinfo". Rootless containers get no cgroup mount
		// of their own, so the host's is passed through.
		env = append(env, keepCgroup)
		volumes = append(volumes, core.VolumeMount{Source: cgroupPath, Destination: cgroupPath})
	} else {
		own = append(own, withOwnCgroupHierarchy())
	}

	config := &core.ContainerConfig{
		Image: buildkitImage,
		Command: []string{
			"buildctl-daemonless.sh", "build",
			"--frontend", "dockerfile.v0",
			"--local", "context=" + buildContextPath,
			"--local", "dockerfile=" + buildContextPath,
			"--opt", "filename=" + dockerfile,
			"--output", fmt.Sprintf("type=docker,name=%s,dest=%s",
				i.name, path.Join(buildOutputPath, buildOutputFile)),
		},
		Env: env,
		// The Dockerfile is repository content, so its RUN steps are untrusted
		// code and get the same egress policy as build.sh. Restricting the
		// builder is only half of it; the steps are the other, kept under the
		// rule by withOwnCgroupHierarchy, or by writeBuildKitConfig where the
		// builder sees the host's hierarchy.
		Network:    &core.NetworkConfig{RestrictEgress: restrictEgress},
		Volumes:    volumes,
		Privileges: buildPrivileges(rootless),
	}

	id, err := i.backend.create(ctx, config, own...)
	if err != nil {
		return fmt.Errorf("failed to create builder for image %s: %w", i.name, err)
	}
	defer i.backend.Remove(context.WithoutCancel(ctx), id)

	if err := i.backend.Start(ctx, id); err != nil {
		return fmt.Errorf("failed to start builder for image %s: %w", i.name, err)
	}

	if err := i.backend.Wait(ctx, id); err != nil {
		return fmt.Errorf("failed to wait for builder of image %s: %w", i.name, err)
	}

	info, err := i.backend.Inspect(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to inspect builder of image %s: %w", i.name, err)
	}

	if info.ExitCode != 0 {
		return fmt.Errorf("building %s failed with exit code %d: %s",
			i.name, info.ExitCode, i.builderOutput(ctx, id))
	}

	return nil
}

// builderOutput reads what the builder said, for an error message. A build that
// failed without explaining why is worse than a slow one, so this reports the
// reason it could not read the output rather than returning nothing.
func (i *containerdImage) builderOutput(ctx context.Context, id core.ContainerID) string {
	logs, err := i.backend.Logs(ctx, id)
	if err != nil {
		return fmt.Sprintf("(builder output unavailable: %s)", err)
	}
	defer logs.Close()

	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, logs); err != nil {
		return fmt.Sprintf("(builder output unreadable: %s)", err)
	}

	// BuildKit reports progress and errors on stderr.
	out := strings.TrimSpace(stderr.String() + "\n" + stdout.String())
	if out == "" {
		return "(builder produced no output)"
	}

	return out
}

// importBuilt loads the tarball BuildKit wrote and makes it usable.
func (i *containerdImage) importBuilt(ctx context.Context, tarPath string) error {
	ctx = namespaces.WithNamespace(ctx, i.backend.config.Namespace)

	tarball, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("builder produced no image for %s: %w", i.name, err)
	}
	defer tarball.Close()

	imported, err := i.backend.client.Import(ctx, tarball)
	if err != nil {
		return fmt.Errorf("failed to import built image %s: %w", i.name, err)
	}

	if len(imported) == 0 {
		return fmt.Errorf("builder produced no image for %s", i.name)
	}

	// containerd stores images under the name inside the tarball, normalised —
	// "foo:latest" becomes "docker.io/library/foo:latest". Callers look the
	// image up by the exact name they asked to build, so that name has to
	// resolve too.
	if err := i.backend.nameImage(ctx, i.name, imported[0].Target); err != nil {
		return fmt.Errorf("failed to name built image %s: %w", i.name, err)
	}

	image, err := i.backend.client.GetImage(ctx, i.name)
	if err != nil {
		return fmt.Errorf("failed to read back built image %s: %w", i.name, err)
	}

	// Unpack: an imported image has content but no snapshot, and a container
	// cannot be created from it until it does.
	if err := image.Unpack(ctx, ""); err != nil {
		return fmt.Errorf("failed to unpack built image %s: %w", i.name, err)
	}

	return nil
}

// nameImage points name at target, creating the image record or repointing an
// existing one, so that rebuilding under a name already in use is not an error.
func (b *ContainerdBackend) nameImage(ctx context.Context, name string, target ocispec.Descriptor) error {
	service := b.client.ImageService()

	record := images.Image{Name: name, Target: target}

	if _, err := service.Create(ctx, record); err == nil {
		return nil
	} else if !errdefs.IsAlreadyExists(err) {
		return err
	}

	_, err := service.Update(ctx, record, "target")

	return err
}

// extractContext unpacks a build context tar into dir.
//
// The tar comes from a user's repository, so every entry is confined to dir: an
// entry named ../../etc/cron.d/x, or a symlink pointing at /etc/shadow, would
// otherwise write outside the build or pull host files into the image.
//
// Checking names is not enough on its own, because a link the tar has already
// made sits in the path of every entry after it, and an entry's name says
// nothing about where that link leads. So every entry is created through an
// os.Root, which refuses to resolve a path out of dir, and once the tar is
// unpacked every link in dir is followed from where it actually sits.
//
// The disk it is unpacked onto keeps its reserve, because a context says
// nothing in advance about how much it unpacks to: the filesystem is read
// before each entry is made and each chunk is written, and a context that would
// leave less free than floor is refused. A run of zeros is left as a hole.
func extractContext(r io.Reader, dir string, floor reserve.Floor) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()

	// The tarball a builder hands over is gzipped (utils/bundle), while the one
	// a test or an API caller writes usually is not. The docker daemon sniffs
	// this for itself; here it has to be done by hand.
	buffered := bufio.NewReader(r)
	var source io.Reader = buffered
	if magic, err := buffered.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		unzipped, err := gzip.NewReader(buffered)
		if err != nil {
			return fmt.Errorf("reading build context: %w", err)
		}
		defer unzipped.Close()
		source = unzipped
	}

	reader := tar.NewReader(source)

	for {
		header, err := reader.Next()
		if err == io.EOF {
			return checkLinks(root)
		}
		if err != nil {
			return fmt.Errorf("reading build context: %w", err)
		}

		name, err := confine(header.Name)
		if err != nil {
			return err
		}

		// Ownership, times and extended attributes, file capabilities among
		// them, are not carried over: what is unpacked belongs to this process.
		switch header.Typeflag {
		case tar.TypeDir, tar.TypeReg, tar.TypeSymlink:
			if err := floor.Take(dir, 0, 1); err != nil {
				return err
			}
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				return err
			}

		case tar.TypeReg:
			if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				return err
			}
			// Permission bits only: setuid, setgid and sticky have no place in a
			// build context.
			if err := writeFile(root, name, reader, header.FileInfo().Mode().Perm(), floor); err != nil {
				return err
			}

		case tar.TypeSymlink:
			if err := checkLink(header.Name, header.Linkname); err != nil {
				return err
			}
			if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				return err
			}
			if err := root.Symlink(header.Linkname, name); err != nil {
				return err
			}

		case tar.TypeLink:
			// A hard link names an earlier entry, by its name in the tar: from
			// the root of the archive, not from the link's own directory.
			target, err := confine(header.Linkname)
			if err != nil {
				return err
			}
			if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				return err
			}
			if err := root.Link(target, name); err != nil {
				return err
			}

		default:
			// Devices, fifos and the like have no place in a build context.
			continue
		}
	}
}

// checkLinks follows every symlink in the unpacked context from where it sits,
// and refuses one that leads out of it. checkLink cannot settle this as entries
// arrive: it reads a target against the link's name, but the components of
// that target can themselves be links, made before or after it, and a hard link
// to a symlink is the same link in another directory.
//
// A link that leads nowhere, to a name that does not exist or through a file,
// is kept: one to a file the build generates is ordinary, and nothing writes
// into the context once it is unpacked. One the root stops following, past its
// limit on links, is refused, since where it would lead is not known.
func checkLinks(root *os.Root) error {
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type() != fs.ModeSymlink {
			return nil
		}
		_, err = root.Stat(name)
		if err == nil || errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return nil
		}
		return fmt.Errorf("build context link %q does not resolve inside it: %w", name, err)
	})
}

// checkLink refuses a link that would resolve outside the build context, as
// far as the tar can tell.
//
// Unlike an entry's name, an absolute link target cannot be rooted into the
// context and made safe: it is read where it points, so /etc/shadow means the
// host's. A relative one is resolved against the directory the tar names for
// the link, and refused if it climbs out. Where it really leads is for
// checkLinks to settle, once everything is on disk.
func checkLink(name, linkname string) error {
	outside := fmt.Errorf("build context link %q points outside it, at %q", name, linkname)

	if filepath.IsAbs(linkname) {
		return outside
	}

	resolved := filepath.Clean(filepath.Join(filepath.Dir(name), linkname))
	if resolved == ".." || strings.HasPrefix(resolved, ".."+string(os.PathSeparator)) {
		return outside
	}

	return nil
}

// confine turns an entry's name into a path relative to the build directory,
// and refuses one that climbs out of it. Anything it does not anticipate, the
// os.Root the path is resolved in still refuses.
//
// An absolute name is rooted into the directory rather than refused — it cannot
// escape, and tars written from an absolute path are ordinary. A name that walks
// up with ".." is refused outright instead of being clamped: cleaning it against
// the root would silently turn "../evil" into "evil" and build something other
// than what the tar described.
func confine(name string) (string, error) {
	cleaned := filepath.Clean(strings.TrimLeft(filepath.ToSlash(name), "/"))

	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("build context entry %q escapes the build directory", name)
	}

	return cleaned, nil
}

func writeFile(root *os.Root, name string, r io.Reader, mode os.FileMode, floor reserve.Floor) error {
	file, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := floor.Copy(file, r); err != nil {
		return err
	}

	return file.Close()
}
